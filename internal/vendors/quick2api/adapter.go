package quick2api

// 适配器：把 Quick 会话凭证接入 core.Vendor。
// 凭证来自浏览器 OAuth（免桌面端），执行走 AIDP WSS 协议层。

import (
	"context"

	"quick2api-plugin/cpasdk/pluginapi"
	"quick2api-plugin/internal/core"
)

// adapter 实现 core.Vendor。
type adapter struct {
	*core.NativeAdapter[*Credential]
	proxy   string
	timeout string
}

// Instances 构造 Quick 实例（单实例，区域不区分）。
func Instances(env core.Env) []core.Vendor {
	return []core.Vendor{&adapter{
		NativeAdapter: core.NewNativeAdapter(core.NativeSpec[*Credential]{
			Meta: core.Meta{VendorID: VendorID, VendorName: VendorName},
			Env:  env,
			Extract: func(c *core.Credential) (*Credential, bool) {
				return c.Native.(*Credential), true
			},
			ParseSeed: func(raw []byte, fileName string) (core.CredentialSeed, *Credential, error) {
				c, err := ParseCredential(raw)
				if err != nil {
					return core.CredentialSeed{}, nil, err
				}
				seed := core.CredentialSeed{
					VendorID: VendorID,
					Label:    c.Label,
					FileID:   core.FileIDFromFileName(fileName),
					Token:    c.IDToken,
					AuthMode: "oauth",
				}
				return seed, c, nil
			},
		}),
	}}
}

// Configure 读取插件配置。
func (a *adapter) Configure(_ core.Settings, raw map[string]string) {
	a.proxy = raw["quick2api_proxy"]
	if v := raw["quick2api_timeout_seconds"]; v != "" {
		a.timeout = v
	}
}

// Match 严格判定凭证归属。
func (a *adapter) Match(fileName, provider string, raw map[string]any) bool {
	return LooksLikeCredential(raw, fileName, provider)
}

// StaticModels 返回对外注册的固定模型清单。
func (a *adapter) StaticModels(_ context.Context) ([]pluginapi.ModelInfo, error) {
	return staticModels(), nil
}

// ModelsForAuth 返回某个凭证可用的模型（与 StaticModels 同源）。
func (a *adapter) ModelsForAuth(_ context.Context, _ *core.Credential) ([]pluginapi.ModelInfo, error) {
	return staticModels(), nil
}

var (
	_ core.Vendor      = (*adapter)(nil)
	_ core.Configurable = (*adapter)(nil)
	_ core.ModelsProvider = (*adapter)(nil)
)
