package app

// 契约版本协商与能力声明的一致性回归。
//
// 背景：宿主侧的校验是「插件声明的 schema_version > 宿主支持的版本就拒绝加载」。
// CPA 8.0.0 是 6，8.1.0 是 10；同一份插件要跨版本分发，必须按宿主降级，
// 不能无条件报最新。这组用例锁住协商规则，以及「声明的能力都有对应方法」。

import (
	"encoding/json"
	"strings"
	"testing"

	"quick2api-plugin/cpasdk/pluginabi"
)

// TestNegotiatedSchemaVersion 覆盖降级、封顶与字段缺失三种情形。
func TestNegotiatedSchemaVersion(t *testing.T) {
	cases := []struct {
		name string
		host uint32
		want uint32
	}{
		{"宿主字段缺失按最初契约", 0, 1},
		{"老宿主 6", 6, 6},
		{"中间版本 8", 8, 8},
		{"与当前契约相同", pluginabi.SchemaVersion, pluginabi.SchemaVersion},
		{"比插件新也不超过插件支持的上限", pluginabi.SchemaVersion + 5, pluginabi.SchemaVersion},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := negotiatedSchemaVersion(testCase.host); got != testCase.want {
				t.Fatalf("negotiatedSchemaVersion(%d) = %d, want %d", testCase.host, got, testCase.want)
			}
		})
	}
}

// TestHostSchemaFromRegisterRequest 确认注册请求里的版本被读出来。
func TestHostSchemaFromRegisterRequest(t *testing.T) {
	raw := []byte(`{"config_yaml":"","schema_version":7}`)
	if got := hostSchemaFrom(raw); got != 7 {
		t.Fatalf("hostSchemaFrom = %d, want 7", got)
	}
	if got := hostSchemaFrom(nil); got != 0 {
		t.Fatalf("hostSchemaFrom(nil) = %d, want 0", got)
	}
	if got := hostSchemaFrom([]byte(`not-json`)); got != 0 {
		t.Fatalf("hostSchemaFrom(garbage) = %d, want 0", got)
	}
}

// TestRegistrationNegotiatesDown 注册响应必须跟着宿主走，不能写死最新版本。
func TestRegistrationNegotiatesDown(t *testing.T) {
	a, errNew := New(Options{Version: "test"})
	if errNew != nil {
		t.Fatalf("new app: %v", errNew)
	}
	for _, host := range []uint32{6, 8, 10, 99} {
		a.hostSchema.Store(host)
		want := host
		if want > pluginabi.SchemaVersion {
			want = pluginabi.SchemaVersion
		}
		if got := a.Registration().SchemaVersion; got != want {
			t.Fatalf("host %d: schema_version = %d, want %d", host, got, want)
		}
	}
}

// TestDeclaredCapabilitiesHaveHandlers 声明的每项能力都要有对应方法。
//
// 反过来的错误更常见：能力声明了但方法没接（宿主调过来直接 unknown_method，
// 表现为「功能在面板上出现但一点就报错」）。这里只查方法存在，不查业务结果。
func TestDeclaredCapabilitiesHaveHandlers(t *testing.T) {
	a, errNew := New(Options{Version: "test"})
	if errNew != nil {
		t.Fatalf("new app: %v", errNew)
	}
	caps := a.Registration().Capabilities
	required := map[string]bool{}
	if caps.ModelProvider {
		required[pluginabi.MethodModelStatic] = true
	}
	if caps.AuthProvider {
		required[pluginabi.MethodAuthIdentifier] = true
	}
	if caps.Executor {
		required[pluginabi.MethodExecutorExecute] = true
	}
	if caps.QuotaProvider {
		required[pluginabi.MethodQuotaIdentifier] = true
	}
	if caps.ManagementAPI {
		required[pluginabi.MethodManagementRegister] = true
	}
	if caps.ResponseBeforeTranslator {
		required[pluginabi.MethodResponseNormalizeBefore] = true
	}
	if len(required) == 0 {
		t.Fatal("registration declares no capabilities at all")
	}
	for method := range required {
		raw, errCall := a.HandleMethod(method, nil)
		if errCall != nil {
			// 业务错误可以接受（缺凭证、缺参数），只要不是「方法不存在」。
			if strings.Contains(errCall.Error(), "unknown method") {
				t.Fatalf("%s declared but not handled: %v", method, errCall)
			}
			continue
		}
		var env struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if errUnmarshal := json.Unmarshal(raw, &env); errUnmarshal != nil {
			t.Fatalf("%s returned non-JSON: %v (%s)", method, errUnmarshal, raw)
		}
		if !env.OK && strings.Contains(env.Error, "unknown method") {
			t.Fatalf("%s declared but not handled: %s", method, raw)
		}
	}
}

// TestRegistrationHasNoConfigFields 锁住「宿主插件设置页不再渲染配置表单」的决定。
//
// 这些键（enabled_realms / state_dir / 各家 base_url 等）仍然从 config.yaml 读取，
// 只是不再由插件声明成表单字段——控制台和 YAML 才是配置入口，插件页那份表单没人用。
func TestRegistrationHasNoConfigFields(t *testing.T) {
	a, errNew := New(Options{Version: "test"})
	if errNew != nil {
		t.Fatalf("new app: %v", errNew)
	}
	raw, errMarshal := json.Marshal(a.Registration())
	if errMarshal != nil {
		t.Fatalf("marshal registration: %v", errMarshal)
	}
	if strings.Contains(string(raw), "config_fields") {
		t.Fatalf("registration must not declare config fields: %s", raw)
	}
}
