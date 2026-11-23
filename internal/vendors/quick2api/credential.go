package quick2api

// 凭证解析与归属判定。
//
// 凭证文件由浏览器 OAuth 登录产出（见 internal/quick/oauth.go 的会话结构），
// 形态是普通 JSON，包含 id_token / ws_host / refresh_token 等字段。
// 文件名前缀固定为 quick2api-，这是最可靠的归属证据。

import (
	"encoding/json"
	"fmt"
	"strings"

	"quick2api-plugin/internal/core"
)

const (
	VendorID   = "quick2api"
	VendorName = "Amazon Quick (quick2api)"
	// ProviderKey 必须与 core.ProviderKey 一致：宿主用它认领凭证文件。
	ProviderKey = core.ProviderKey
)

// Credential 是归一化后的 Quick 会话凭证。
type Credential struct {
	// 宿主认领凭证所需的三件套：type 必须等于 ProviderKey，vendor 等于 VendorID。
	Type     string `json:"type,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
	UID      string `json:"uid,omitempty"`
	AuthMode string `json:"auth_mode,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`

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
	Label        string `json:"label,omitempty"`
	Nickname     string `json:"nickname,omitempty"`
	Account      string `json:"account,omitempty"`
}

// CredentialFromSession 把 OAuth 会话转成可直接落盘的宿主凭证。
func CredentialFromSession(s *core.Session) *Credential {
	if s == nil {
		return nil
	}
	label := s.Email
	if label == "" {
		label = s.AccountID
	}
	return &Credential{
		Type: ProviderKey, Vendor: VendorID, UID: CredentialUID(s),
		AuthMode: "oauth",
		IDToken:  s.IDToken, AccessToken: s.AccessToken,
		WSHost: s.WSHost, TenantURL: s.TenantURL, ActorARN: s.ActorARN,
		AccountID: s.AccountID, Email: s.Email, UsableUntil: s.UsableUntil,
		RefreshToken: s.RefreshToken, ClientID: s.ClientID, ClientSecret: s.ClientSecret,
		IssuerURL: s.IssuerURL, IDCRegion: s.IDCRegion, DeviceID: s.DeviceID,
		Label: label, Nickname: label, Account: s.AccountID,
	}
}

// CredentialUID 生成稳定的凭证文件名（不含 .json）。
func CredentialUID(s *core.Session) string {
	base := s.Email
	if base == "" {
		base = s.AccountID
	}
	if base == "" {
		base = s.ActorARN
	}
	if base == "" {
		base = "account"
	}
	var b strings.Builder
	for i := 0; i < len(base); i++ {
		c := base[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '@', c == '.', c == '-', c == '_':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	return VendorID + "-" + b.String()
}

// Session 转成协议层需要的会话结构。
func (c *Credential) Session() *core.Session {
	return &core.Session{
		IDToken: c.IDToken, AccessToken: c.AccessToken, WSHost: c.WSHost,
		TenantURL: c.TenantURL, ActorARN: c.ActorARN, Provider: "social",
		AccountID: c.AccountID, Email: c.Email, UsableUntil: c.UsableUntil,
		RefreshToken: c.RefreshToken, ClientID: c.ClientID, ClientSecret: c.ClientSecret,
		IssuerURL: c.IssuerURL, IDCRegion: c.IDCRegion, DeviceID: c.DeviceID,
	}
}

func ParseCredential(raw []byte) (*Credential, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("credential is empty")
	}
	var c Credential
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("credential is not valid JSON: %w", err)
	}
	if c.IDToken == "" || c.WSHost == "" {
		return nil, fmt.Errorf("credential missing id_token or ws_host")
	}
	if c.Label == "" {
		c.Label = c.Email
	}
	if c.Label == "" {
		c.Label = c.AccountID
	}
	return &c, nil
}

// LooksLikeCredential 严格判定归属，避免误吞其它插件的凭证。
func LooksLikeCredential(raw map[string]any, fileName, provider string) bool {
	if core.FileNameMatchesConvention(fileName, VendorID, "quick", "quick2api") {
		return true
	}
	if raw == nil {
		return false
	}
	// 显式 vendor 字段：只认本插件的
	if v := strings.TrimSpace(core.GetString(raw, "vendor")); v != "" && v != VendorID {
		return false
	}
	// Quick 会话的特征字段组合
	_, hasID := raw["id_token"]
	_, hasWS := raw["ws_host"]
	_, hasRefresh := raw["refresh_token"]
	return hasID && hasWS && hasRefresh
}
