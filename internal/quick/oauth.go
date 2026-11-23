package quick

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"quick2api-plugin/internal/core"
	"quick2api-plugin/internal/logger"
)

// 这些常量来自 magpie-aws-quick 对 Quick 1.2.x 的实测逆向。
// 即使走 Browser OAuth（免桌面端），WS 握手仍必须使用桌面端 surface。
const (
	AppVersion     = "1.2.4660"
	SurfaceDesktop = "QUICK_WORK_DESKTOP_WINDOWS"
	ScopeProd      = "quicksight:read_write"
	DefaultRegion  = "us-east-1"

	discoveryProd = "https://quick.aws.com/sn"
	dataPlaneProd = "https://dp.appintegrations.us-east-1.prod.plato.ai.aws.dev"
)

// SessionStore 持久化 OAuth 会话，文件权限 0600。
type SessionStore struct {
	dir string
	mu  sync.Mutex
}

func DefaultStoreDir() string {
	if v := os.Getenv("QUICK2API_STATE_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("APPDATA"); v != "" {
		return filepath.Join(v, "cliproxyapi-quick2api")
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "cliproxyapi-quick2api")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".cliproxyapi-quick2api")
}

func NewSessionStore(dir string) *SessionStore {
	if dir == "" {
		dir = DefaultStoreDir()
	}
	return &SessionStore{dir: dir}
}

func (s *SessionStore) file() string { return filepath.Join(s.dir, "oauth-sessions.json") }

func (s *SessionStore) load() map[string]*core.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*core.Session{}
	b, err := os.ReadFile(s.file())
	if err != nil {
		return out
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out
	}
	return out
}

func (s *SessionStore) Save(key string, sess *core.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	db := map[string]*core.Session{}
	if b, err := os.ReadFile(s.file()); err == nil {
		_ = json.Unmarshal(b, &db)
	}
	db[key] = sess
	b, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.file() + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file())
}

func (s *SessionStore) Get(key string) *core.Session { return s.load()[key] }

func (s *SessionStore) List() map[string]*core.Session { return s.load() }

func (s *SessionStore) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	db := map[string]*core.Session{}
	if b, err := os.ReadFile(s.file()); err == nil {
		_ = json.Unmarshal(b, &db)
	}
	delete(db, key)
	b, _ := json.MarshalIndent(db, "", "  ")
	return os.WriteFile(s.file(), b, 0600)
}

// KeyForSession 用邮箱/arn/tenant 生成稳定账号键。
func KeyForSession(sess *core.Session) string {
	for _, v := range []string{sess.Email, sess.ActorARN, sess.AccountID, sess.TenantURL} {
		if v != "" {
			return v
		}
	}
	return "default"
}

// ---------- OIDC + PKCE Browser OAuth ----------

type discovery struct {
	IssuerURL     string `json:"issuer_url"`
	TenantURL     string `json:"tenant_url"`
	Account       string `json:"account"`
	IDCInstanceID string `json:"idc_instance_id"`
	IDCRegion     string `json:"idc_region"`
}

// oidcClient 对应 AWS OIDC client/register 的响应。
// 注意：AWS 返回的是 camelCase（clientId / clientSecret），不是 snake_case。
type oidcClient struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type tokenResponse struct {
	AccessToken  string `json:"accessToken"`
	IDToken      string `json:"idToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
}

// UnmarshalJSON 同时兼容 camelCase 与 snake_case：
// AWS OIDC 实测返回 camelCase，但不同端点/版本存在 snake_case 形态，
// 只认一种会让 token 静默变成空串。
func (t *tokenResponse) UnmarshalJSON(b []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := raw[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	t.AccessToken = pick("accessToken", "access_token")
	t.IDToken = pick("idToken", "id_token")
	t.RefreshToken = pick("refreshToken", "refresh_token")
	for _, k := range []string{"expiresIn", "expires_in"} {
		if v, ok := raw[k].(float64); ok {
			t.ExpiresIn = int64(v)
			break
		}
	}
	return nil
}

// LoginFlow 是一次浏览器登录流程。管理页拿到 Start 返回的 URL 展示给用户即可。
type LoginFlow struct {
	store       *SessionStore
	port        int
	stage       string
	nonce       string
	state       string
	verifier    string
	redirectURI string
	server      *http.Server
	done        chan struct{}
	once        sync.Once
	mu          sync.Mutex
	result      *core.Session
	err         error
	disc        discovery
	client      oidcClient
}

func NewLoginFlow(store *SessionStore, stage string) (*LoginFlow, error) {
	if stage == "" {
		stage = "prod"
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	verifier, _, err := pkce()
	if err != nil {
		return nil, err
	}
	nonce, err := randomToken()
	if err != nil {
		return nil, err
	}
	state, err := randomToken()
	if err != nil {
		return nil, err
	}
	return &LoginFlow{
		store:       store,
		port:        port,
		stage:       stage,
		nonce:       nonce,
		state:       state,
		verifier:    verifier,
		redirectURI: fmt.Sprintf("http://127.0.0.1:%d", port),
		done:        make(chan struct{}),
	}, nil
}

// Start 启动本地回调服务并返回需要用户在浏览器打开的登录 URL。
func (f *LoginFlow) Start(ctx context.Context) (string, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", f.handleDiscovery)
	mux.HandleFunc("/", f.handleCallback)
	f.server = &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", f.port), Handler: mux}

	ln, err := net.Listen("tcp", f.server.Addr)
	if err != nil {
		return "", err
	}
	go func() {
		_ = f.server.Serve(ln)
	}()
	go func() {
		<-ctx.Done()
		_ = f.server.Close()
	}()

	u, _ := url.Parse(discoveryProd)
	q := u.Query()
	q.Set("qwSurface", fmt.Sprintf("desktop:%s:%d:%s", f.nonce, f.port, AppVersion))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Wait 阻塞直到登录完成或 ctx 结束。
func (f *LoginFlow) Wait(ctx context.Context) (*core.Session, error) {
	select {
	case <-f.done:
		return f.result, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Poll 非阻塞检查登录是否完成。done=false 表示仍在等待。
func (f *LoginFlow) Poll() (sess *core.Session, err error, done bool) {
	select {
	case <-f.done:
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.result, f.err, true
	default:
		return nil, nil, false
	}
}

// Close 主动终止登录流程（宿主取消或超时）。
func (f *LoginFlow) Close() {
	f.finish(nil, NewError("login_cancelled", "login flow closed", 400, false))
}

func (f *LoginFlow) finish(sess *core.Session, err error) {
	f.once.Do(func() {
		f.mu.Lock()
		f.result, f.err = sess, err
		f.mu.Unlock()
		close(f.done)
		if f.server != nil {
			_ = f.server.Close()
		}
	})
}

// handleDiscovery 处理第一跳：quick.aws.com/sn 回调，拿到 IDC discovery 后注册动态客户端。
func (f *LoginFlow) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.disc = discovery{
		IssuerURL:     first(q.Get("issuerUrl"), q.Get("issuer_url")),
		TenantURL:     first(q.Get("tenantUrl"), q.Get("tenant_url")),
		Account:       q.Get("account"),
		IDCInstanceID: q.Get("idc_instance_id"),
		IDCRegion:     first(q.Get("idcRegion"), q.Get("idc_region"), DefaultRegion),
	}
	if f.disc.IDCRegion == "" {
		f.disc.IDCRegion = DefaultRegion
	}
	logger.Info("quick2api 登录发现：region=%s issuer=%s tenant=%s account=%s",
		f.disc.IDCRegion, f.disc.IssuerURL, f.disc.TenantURL, f.disc.Account)

	cl, err := registerClient(f.disc.IDCRegion, f.redirectURI, f.disc.IssuerURL, f.stage)
	if err != nil {
		// 注意顺序：先把错误页写给浏览器，再关闭服务。反过来会掐断当前连接，
		// 用户只会看到 ERR_CONNECTION_REFUSED 而非真正的错误原因。
		logger.Info("quick2api 客户端注册失败：%v", err)
		writePage(w, "登录失败", "客户端注册失败："+err.Error())
		f.finish(nil, err)
		return
	}
	f.client = cl
	logger.Info("quick2api 客户端注册成功：client_id=%s", cl.ClientID)

	u, err := f.authorizeURL()
	if err != nil {
		writePage(w, "登录失败", err.Error())
		f.finish(nil, err)
		return
	}
	logger.Info("quick2api 跳转授权页")
	http.Redirect(w, r, u, http.StatusFound)
}

func (f *LoginFlow) authorizeURL() (string, error) {
	if f.client.ClientID == "" {
		return "", fmt.Errorf("empty client_id")
	}
	u, err := url.Parse(fmt.Sprintf("https://oidc.%s.amazonaws.com/authorize", f.disc.IDCRegion))
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", f.client.ClientID)
	q.Set("redirect_uri", f.redirectURI)
	q.Set("scope", f.scope())
	q.Set("state", f.state)
	_, challenge, err := pkceFromVerifier(f.verifier)
	if err != nil {
		return "", err
	}
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (f *LoginFlow) scope() string {
	if f.stage == "beta" || f.stage == "integ" || f.stage == "dev" {
		return "quicksight_test:read_write"
	}
	return ScopeProd
}

// handleCallback 处理第二跳：OIDC 回调 code，换取 token 并构建会话。
func (f *LoginFlow) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		err := fmt.Errorf("OIDC error: %s %s", e, q.Get("error_description"))
		logger.Info("quick2api 授权失败：%v", err)
		writePage(w, "登录失败", err.Error())
		f.finish(nil, err)
		return
	}
	code := q.Get("code")
	if code == "" {
		// 不带 code 的访问（浏览器预检/favicon）不应影响流程。
		writePage(w, "等待登录", "请在打开的浏览器中完成 Amazon 登录。此页面会在登录完成后自动跳转。")
		return
	}
	logger.Info("quick2api 收到授权码，state 校验=%v", q.Get("state") == f.state)
	if q.Get("state") != f.state {
		err := fmt.Errorf("state mismatch: got %q want %q", q.Get("state"), f.state)
		writePage(w, "登录失败", err.Error())
		f.finish(nil, err)
		return
	}
	tok, err := exchangeCode(f.disc.IDCRegion, f.client, code, f.redirectURI, f.verifier)
	if err != nil {
		logger.Info("quick2api 换取 token 失败：%v", err)
		writePage(w, "登录失败", "换取 token 失败："+err.Error())
		f.finish(nil, err)
		return
	}
	logger.Info("quick2api 换取 token 成功：id_token 长度=%d", len(tok.IDToken))
	sess, err := buildSession(tok, f.disc, f.client, f.stage)
	if err != nil {
		logger.Info("quick2api 构建会话失败：%v", err)
		writePage(w, "登录失败", err.Error())
		f.finish(nil, err)
		return
	}
	if f.store != nil {
		if err := f.store.Save(KeyForSession(sess), sess); err != nil {
			logger.Info("quick2api 保存会话失败：%v", err)
			writePage(w, "登录失败", "保存会话失败: "+err.Error())
			f.finish(nil, err)
			return
		}
	}
	logger.Info("quick2api 登录完成：account=%s email=%s", sess.AccountID, sess.Email)
	f.finish(sess, nil)
	who := sess.Email
	if who == "" {
		who = sess.AccountID
	}
	writePage(w, "登录成功", "账号 "+who+" 已接入。可以关闭此页面回到插件。")
}

// Refresh 用 refresh_token 续期，成功后写回存储。
func Refresh(sess *core.Session, store *SessionStore) (*core.Session, error) {
	if sess == nil || sess.RefreshToken == "" {
		return nil, NewError("no_refresh_token", "stored session has no refresh_token; log in again", 401, false)
	}
	region := sess.IDCRegion
	if region == "" {
		region = DefaultRegion
	}
	cl := oidcClient{ClientID: sess.ClientID, ClientSecret: sess.ClientSecret}
	tok := &tokenResponse{}
	err := postToken(region, map[string]any{
		"clientId":     cl.ClientID,
		"clientSecret": cl.ClientSecret,
		"grantType":    "refresh_token",
		"refreshToken": sess.RefreshToken,
	}, tok)
	if err != nil {
		return nil, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = sess.RefreshToken
	}
	disc := discovery{IssuerURL: sess.IssuerURL, TenantURL: sess.TenantURL, IDCRegion: region, Account: sess.AccountID}
	out, err := buildSession(tok, disc, cl, "prod")
	if err != nil {
		return nil, err
	}
	out.DeviceID = sess.DeviceID
	if out.DeviceID == "" {
		out.DeviceID = stableDeviceID(KeyForSession(sess))
	}
	if store != nil {
		_ = store.Save(KeyForSession(out), out)
	}
	return out, nil
}

// ---------- 内部辅助 ----------

func registerClient(region, redirectURI, issuerURL, stage string) (oidcClient, error) {
	body := map[string]any{
		"clientName":  "AmazonQuick-Desktop",
		"clientType":  "public",
		"scopes":      []string{map[bool]string{true: "quicksight_test:read_write", false: ScopeProd}[stage == "beta" || stage == "integ" || stage == "dev"]},
		"grantTypes":  []string{"authorization_code", "refresh_token"},
		"redirectUris": []string{redirectURI},
	}
	// issuerUrl 是 AWS Identity Center 的硬性要求：缺了它 client/register
	// 会返回 400 "Start URL is required"。
	if issuerURL != "" {
		body["issuerUrl"] = issuerURL
	}
	var out oidcClient
	if err := postJSON(fmt.Sprintf("https://oidc.%s.amazonaws.com/client/register", region), body, &out); err != nil {
		return out, err
	}
	if out.ClientID == "" {
		return out, fmt.Errorf("client registration returned no clientId")
	}
	return out, nil
}

func exchangeCode(region string, cl oidcClient, code, redirectURI, verifier string) (*tokenResponse, error) {
	var out tokenResponse
	err := postToken(region, map[string]any{
		"clientId":     cl.ClientID,
		"clientSecret": cl.ClientSecret,
		"grantType":    "authorization_code",
		"code":         code,
		"redirectUri":  redirectURI,
		"codeVerifier": verifier,
	}, &out)
	return &out, err
}

func postToken(region string, body map[string]any, out ...*tokenResponse) error {
	var target *tokenResponse
	if len(out) > 0 && out[0] != nil {
		target = out[0]
	} else {
		target = &tokenResponse{}
	}
	return postJSON(fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region), body, target)
}

func postJSON(endpoint string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 把上游错误体带出来：AWS OIDC 的 400 会写明缺什么字段，
		// 只报状态码会让排障变成猜谜。
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 800))
		return fmt.Errorf("OIDC %s returned %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func buildSession(tok *tokenResponse, disc discovery, cl oidcClient, stage string) (*core.Session, error) {
	jwt := first(tok.IDToken, tok.AccessToken)
	if jwt == "" {
		return nil, fmt.Errorf("OIDC response has no id_token")
	}
	tenant := strings.TrimRight(disc.TenantURL, "/")
	if tenant == "" {
		tenant = dataPlaneProd
	}
	u, err := url.Parse(tenant)
	if err != nil {
		return nil, err
	}
	claimsExp := jwtExp(jwt)
	usable := time.Now().Unix()
	if claimsExp > 0 {
		usable = claimsExp
	} else if tok.ExpiresIn > 0 {
		usable = time.Now().Unix() + tok.ExpiresIn
	} else {
		usable = time.Now().Add(time.Hour).Unix()
	}
	return &core.Session{
		IDToken:      jwt,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		WSHost:       u.Host,
		TenantURL:    tenant,
		Provider:     "social",
		ClientID:     cl.ClientID,
		ClientSecret: cl.ClientSecret,
		IssuerURL:    disc.IssuerURL,
		IDCRegion:    disc.IDCRegion,
		AccountID:    disc.Account,
		Email:        jwtClaim(jwt, "email"),
		UsableUntil:  usable,
		DeviceID:     stableDeviceID(disc.Account + cl.ClientID),
	}, nil
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkce() (verifier, challenge string, err error) {
	verifier, err = randomToken()
	if err != nil {
		return "", "", err
	}
	_, challenge, err = pkceFromVerifier(verifier)
	return verifier, challenge, err
}

func pkceFromVerifier(v string) (string, string, error) {
	sum := sha256.Sum256([]byte(v))
	return v, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func jwtExp(jwt string) int64 {
	v := jwtClaim(jwt, "exp")
	var n int64
	_, _ = fmt.Sscan(v, &n)
	return n
}

// jwtClaim 只做 base64 解码，不校验签名（token 由 OIDC 直接返回）。
func jwtClaim(jwt, key string) string {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(b, &claims); err != nil {
		return ""
	}
	switch v := claims[key].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	}
	return ""
}

func stableDeviceID(seed string) string {
	// 用 seed 的 sha256 前 16 字节构造一个稳定的 UUID v4 形态字符串。
	sum := sha256.Sum256([]byte(seed))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func writePage(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title>
<style>body{font-family:system-ui;max-width:640px;margin:80px auto;line-height:1.7;color:#222}</style>
</head><body><h2>%s</h2><p>%s</p></body></html>`, title, title, body)
}
