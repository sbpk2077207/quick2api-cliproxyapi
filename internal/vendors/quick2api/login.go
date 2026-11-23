package quick2api

// 登录能力：让 EasyCLIProxyAPI 控制台的「添加账号」直接发起 Quick 浏览器登录。
//
// 说明：AWS OIDC 登录必须在真正的浏览器里完成（Cookie、密码输入、风控检测），
// Tauri webview 内嵌外部域名会被 X-Frame-Options / UA 检测拦住。因此这里只负责
// 把登录地址交给宿主，由宿主在系统浏览器打开；登录完成后宿主轮询本插件拿凭证。

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"quick2api-plugin/cpasdk/pluginapi"
	"quick2api-plugin/internal/core"
	"quick2api-plugin/internal/quick"
)

// loginFlows 保存进行中的登录会话，key 是宿主轮询用的 state。
var loginFlows sync.Map // map[string]*quick.LoginFlow

const loginFlowTTL = 10 * time.Minute

// LoginStart 发起一次登录，返回需要用户打开的授权地址。
func (a *adapter) LoginStart(ctx context.Context, _ map[string]any) (*pluginapi.AuthLoginStartResponse, error) {
	flow, err := quick.NewLoginFlow(nil, "prod")
	if err != nil {
		return nil, core.ToPluginError(err)
	}
	// 用 Background：宿主的请求 ctx 可能在长轮询前就结束，登录不能跟着中断。
	url, err := flow.Start(context.Background())
	if err != nil {
		return nil, core.ToPluginError(err)
	}

	state := quick.NewUUID()
	loginFlows.Store(state, flow)
	go func() {
		<-time.After(loginFlowTTL)
		if v, ok := loginFlows.LoadAndDelete(state); ok {
			v.(*quick.LoginFlow).Close()
		}
	}()

	return &pluginapi.AuthLoginStartResponse{
		Provider:  ProviderKey,
		URL:       url,
		State:     state,
		ExpiresAt: time.Now().Add(loginFlowTTL),
		Metadata:  map[string]any{"vendor": VendorID},
	}, nil
}

// LoginPoll 非阻塞查询登录进度；成功后返回可直接落盘的凭证。
func (a *adapter) LoginPoll(_ context.Context, state string) (*pluginapi.AuthLoginPollResponse, error) {
	v, ok := loginFlows.Load(state)
	if !ok {
		return &pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "登录会话不存在或已过期，请重新发起",
		}, nil
	}
	flow := v.(*quick.LoginFlow)

	sess, err, done := flow.Poll()
	if !done {
		return &pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "等待在浏览器中完成 Amazon 登录…",
		}, nil
	}
	loginFlows.Delete(state)

	if err != nil {
		return &pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: err.Error(),
		}, nil
	}

	cred := CredentialFromSession(sess)
	if cred == nil {
		return &pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "登录成功但未能构造凭证",
		}, nil
	}
	storage, errMarshal := json.Marshal(cred)
	if errMarshal != nil {
		return &pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "凭证序列化失败: " + errMarshal.Error(),
		}, nil
	}

	label := cred.Label
	if label == "" {
		label = cred.UID
	}
	return &pluginapi.AuthLoginPollResponse{
		Status:  pluginapi.AuthLoginStatusSuccess,
		Message: "登录成功",
		Auth: pluginapi.AuthData{
			Provider:    ProviderKey,
			ID:          cred.UID,
			FileName:    cred.UID + ".json",
			Label:       label,
			StorageJSON: storage,
		},
	}, nil
}

// OwnsLoginSession 让根层能按 state 找到本插件的登录会话。
func (a *adapter) OwnsLoginSession(sessionID string) bool {
	_, ok := loginFlows.Load(sessionID)
	return ok
}

var (
	_ core.LoginProvider      = (*adapter)(nil)
	_ core.LoginSessionOwner  = (*adapter)(nil)
)
