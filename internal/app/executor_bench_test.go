package app

// 出站请求体改写的正确性回归与性能基准。
//
// 改写曾经是「整份解成 map → 改值 → 序列化」：请求体越大越慢（64KB 实测
// 885µs、6.7k 次分配），而且所有数字都被过成 float64（超过 2^53 的整数静默
// 失真）、字段顺序被重排。现在走原地拼接，这些用例锁住行为。

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestRewritePayloadModelSplicesOnlyTopLevel 顶层改写、嵌套同名键不动、数字逐字保留。
func TestRewritePayloadModelSplicesOnlyTopLevel(t *testing.T) {
	payload := []byte(`{"model":"alias","seed":1234567890123456789,"messages":[{"role":"user","content":"hi","model":"nested"}],"tools":[{"function":{"name":"f","model":"deep"}}]}`)
	want := `{"model":"official/model","seed":1234567890123456789,"messages":[{"role":"user","content":"hi","model":"nested"}],"tools":[{"function":{"name":"f","model":"deep"}}]}`
	got := rewritePayloadModel(payload, "official/model")
	if string(got) != want {
		t.Fatalf("rewrite mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestRewritePayloadModelKeepsWhitespaceAndOrder 原地拼接不该重排字段或吃掉空白。
func TestRewritePayloadModelKeepsWhitespaceAndOrder(t *testing.T) {
	payload := []byte("{ \"model\" : \"old\" , \"b\":1, \"a\":2 }")
	want := "{ \"model\" : \"new\" , \"b\":1, \"a\":2 }"
	if got := string(rewritePayloadModel(payload, "new")); got != want {
		t.Fatalf("rewrite mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestRewritePayloadModelStringValueNamedModel 字符串值恰好是 "model" 时不能误判成键。
func TestRewritePayloadModelStringValueNamedModel(t *testing.T) {
	payload := []byte(`{"note":"model","model":"old"}`)
	got := rewritePayloadModel(payload, "new")
	want := `{"note":"model","model":"new"}`
	if string(got) != want {
		t.Fatalf("rewrite mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestRewritePayloadModelFallbacks 结构不合常规时回落到整份改写，结果仍然正确。
func TestRewritePayloadModelFallbacks(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		model   string
	}{
		{"缺少 model 键", `{"messages":[]}`, "m"},
		{"model 是 null", `{"model":null,"a":1}`, "m"},
		{"model 是数字", `{"model":1,"a":1}`, "m"},
		{"值需要转义", `{"model":"old"}`, `m"quote`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			out := rewritePayloadModel([]byte(testCase.payload), testCase.model)
			var obj map[string]any
			if errUnmarshal := json.Unmarshal(out, &obj); errUnmarshal != nil {
				t.Fatalf("output must stay valid JSON: %v (%s)", errUnmarshal, out)
			}
			if obj["model"] != testCase.model {
				t.Fatalf("model = %v, want %q", obj["model"], testCase.model)
			}
		})
	}
}

// TestRewritePayloadModelInvalidJSON 非法 JSON 原样返回（不改写、不 panic）。
func TestRewritePayloadModelInvalidJSON(t *testing.T) {
	payload := []byte(`{"model":`)
	if got := rewritePayloadModel(payload, "m"); !bytes.Equal(got, payload) {
		t.Fatalf("invalid payload must be returned unchanged, got %s", got)
	}
}

// benchRequestPayload 造一个带长上下文的请求体。
func benchRequestPayload(size int) []byte {
	var builder strings.Builder
	builder.WriteString(`{"model":"alias","stream":true,"seed":1234567890123456789,"messages":[`)
	for builder.Len() < size {
		builder.WriteString(`{"role":"user","content":"这是一段用于压测的较长中文对话内容，用来模拟真实的长上下文请求体。"},`)
	}
	builder.WriteString(`{"role":"user","content":"hi"}]}`)
	return []byte(builder.String())
}

// BenchmarkRewritePayloadModel 是当前实现（原地拼接）。
func BenchmarkRewritePayloadModel(b *testing.B) {
	payload := benchRequestPayload(64 * 1024)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = rewritePayloadModel(payload, "official/model")
	}
}

// BenchmarkRewritePayloadModelRoundTrip 是旧实现（整份 map 往返），留作对照。
func BenchmarkRewritePayloadModelRoundTrip(b *testing.B) {
	payload := benchRequestPayload(64 * 1024)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = rewritePayloadModelRoundTrip(payload, "official/model")
	}
}

// BenchmarkStreamEmitRequestMarshal 是每帧一次的出站信封序列化。
func BenchmarkStreamEmitRequestMarshal(b *testing.B) {
	payload := []byte(`{"id":"chatcmpl-9f2c1e","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"一段输出文本"},"finish_reason":null}],"usage":null}`)
	request := &streamEmitRequest{StreamID: "stream-abc", Payload: payload}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		raw, errMarshal := json.Marshal(request)
		if errMarshal != nil {
			b.Fatal(errMarshal)
		}
		_ = raw
	}
}
