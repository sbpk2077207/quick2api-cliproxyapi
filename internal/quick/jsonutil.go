package quick

import (
	"encoding/base64"
	"encoding/json"
)

// jsonStringOrObject 兼容 payload 既可能是字符串也可能是对象。
type jsonStringOrObject struct {
	String string
	Object map[string]any
}

func (j *jsonStringOrObject) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		j.String = s
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err == nil {
		j.Object = m
		return nil
	}
	return json.Unmarshal(b, &j.String)
}

func jsonUnmarshal(b []byte, v any) error      { return json.Unmarshal(b, v) }
func base64Decode(s string) ([]byte, error)    { return base64.StdEncoding.DecodeString(s) }
func base64Encode(b []byte) string             { return base64.StdEncoding.EncodeToString(b) }
