package main

// quick2api 凭证生成工具：走浏览器 OAuth（OIDC+PKCE），不需要安装 Amazon Quick 桌面端。
// 运行（在 src 目录）：go run ./cmd/q2a-login  或直接跑编译好的 q2a-login.exe
// 产出 quick2api-<标识>.json，直接放进 EasyCLIProxyAPI 的 oauth 目录即可被宿主认领。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"quick2api-plugin/internal/quick"
)

// credentialFile 是宿主 auth 目录要求的凭证结构。
// type 必须等于插件的 provider key，否则宿主不会把文件交给本插件。
type credentialFile struct {
	Type         string `json:"type"`
	Vendor       string `json:"vendor"`
	UID          string `json:"uid"`
	AuthMode     string `json:"auth_mode"`
	Disabled     bool   `json:"disabled"`
	Label        string `json:"label,omitempty"`
	Nickname     string `json:"nickname,omitempty"`
	Account      string `json:"account,omitempty"`
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token,omitempty"`
	WSHost       string `json:"ws_host"`
	TenantURL    string `json:"tenant_url,omitempty"`
	ActorARN     string `json:"actor_arn,omitempty"`
	AccountID    string `json:"aws_account_id,omitempty"`
	Email        string `json:"email,omitempty"`
	UsableUntil  int64  `json:"usable_until"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	IssuerURL    string `json:"issuer_url,omitempty"`
	IDCRegion    string `json:"idc_region,omitempty"`
	DeviceID     string `json:"device_id,omitempty"`
}

func main() {
	outDir := "."
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}

	store := quick.NewSessionStore("")
	flow, err := quick.NewLoginFlow(store, "prod")
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化登录失败:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	loginURL, err := flow.Start(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动本地回调服务失败:", err)
		os.Exit(1)
	}

	fmt.Println("===== Amazon Quick 浏览器登录（免桌面端）=====")
	fmt.Println("请在浏览器打开下面地址并完成 Amazon 登录：")
	fmt.Println()
	fmt.Println("  " + loginURL)
	fmt.Println()
	fmt.Println("等待登录完成（最长 10 分钟）...")

	sess, err := flow.Wait(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "登录失败:", err)
		os.Exit(1)
	}

	id := sess.Email
	if id == "" {
		id = sess.AccountID
	}
	if id == "" {
		sum := sha256.Sum256([]byte(sess.ActorARN + sess.WSHost))
		id = hex.EncodeToString(sum[:6])
	}
	uid := "quick2api-" + sanitize(id)

	cred := credentialFile{
		Type: "quick2api", Vendor: "quick2api", UID: uid,
		AuthMode: "oauth", Disabled: false,
		Label: sess.Email, Nickname: sess.Email, Account: sess.AccountID,
		IDToken: sess.IDToken, AccessToken: sess.AccessToken,
		WSHost: sess.WSHost, TenantURL: sess.TenantURL,
		ActorARN: sess.ActorARN, AccountID: sess.AccountID, Email: sess.Email,
		UsableUntil: sess.UsableUntil, RefreshToken: sess.RefreshToken,
		ClientID: sess.ClientID, ClientSecret: sess.ClientSecret,
		IssuerURL: sess.IssuerURL, IDCRegion: sess.IDCRegion, DeviceID: sess.DeviceID,
	}

	b, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "序列化失败:", err)
		os.Exit(1)
	}

	outPath := filepath.Join(outDir, uid+".json")
	if err := os.WriteFile(outPath, b, 0600); err != nil {
		fmt.Fprintln(os.Stderr, "写文件失败:", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("登录成功，凭证已写入：")
	fmt.Println("  " + outPath)
	fmt.Println("账号：", sess.Email)
	fmt.Println("有效期至：", time.Unix(sess.UsableUntil, 0).Format("2006-01-02 15:04:05"))
	fmt.Println()
	fmt.Println("下一步：把该文件复制到 EasyCLIProxyAPI 的 oauth 目录，然后重启核心即可。")
}

// sanitize 把任意标识转成可安全用于文件名的片段。
func sanitize(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '@', c == '.', c == '-', c == '_':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	if len(b) == 0 {
		return "account"
	}
	return string(b)
}
