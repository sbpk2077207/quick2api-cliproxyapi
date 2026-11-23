package quick

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

// ESMessage 是一条 AWS event-stream 消息。
type ESMessage struct {
	Headers map[string]string
	Payload []byte
}

// DecodeEventStream 从 buf 中解出所有完整消息，并返回已消费字节数。
// 帧结构：12B prelude(total_len, headers_len, prelude_crc) + headers + payload + 4B crc。
// 注意：部分端点的 CRC 是 advisory 的，这里只校验长度不校验 CRC。
func DecodeEventStream(buf []byte) ([]ESMessage, int, error) {
	var msgs []ESMessage
	consumed := 0
	for len(buf) >= 16 {
		total := int(binary.BigEndian.Uint32(buf[0:4]))
		headersLen := int(binary.BigEndian.Uint32(buf[4:8]))
		if total < 16 || total > len(buf) {
			break
		}
		if headersLen < 0 || 12+headersLen > total-4 {
			return nil, consumed, fmt.Errorf("invalid headers_len=%d total=%d", headersLen, total)
		}
		headers := map[string]string{}
		pos := 12
		end := 12 + headersLen
		for pos < end {
			if pos >= end {
				break
			}
			nameLen := int(buf[pos])
			pos++
			if nameLen <= 0 || pos+nameLen > end {
				break
			}
			name := string(buf[pos : pos+nameLen])
			pos += nameLen
			if pos >= end {
				break
			}
			typ := buf[pos]
			pos++
			switch typ {
			case 0, 1: // bool / byte
				if pos+1 > end {
					pos = end
					break
				}
				headers[name] = fmt.Sprintf("%d", buf[pos])
				pos++
			case 2: // int16
				if pos+2 > end {
					pos = end
					break
				}
				headers[name] = fmt.Sprintf("%d", int16(binary.BigEndian.Uint16(buf[pos:pos+2])))
				pos += 2
			case 3, 4: // int32 / int64
				n := 4
				if typ == 4 {
					n = 8
				}
				if pos+n > end {
					pos = end
					break
				}
				headers[name] = fmt.Sprintf("%d", binary.BigEndian.Uint64(buf[pos:pos+n]))
				pos += n
			case 5, 6, 7: // binary / string / timestamp —— 均按 2B 长度前缀处理
				if pos+2 > end {
					pos = end
					break
				}
				l := int(binary.BigEndian.Uint16(buf[pos : pos+2]))
				pos += 2
				if pos+l > end {
					pos = end
					break
				}
				headers[name] = string(buf[pos : pos+l])
				pos += l
			default:
				pos = end
			}
		}
		payloadEnd := total - 4
		if payloadEnd < 12+headersLen {
			return nil, consumed, fmt.Errorf("invalid payloadEnd=%d", payloadEnd)
		}
		payload := append([]byte(nil), buf[12+headersLen:payloadEnd]...)
		msgs = append(msgs, ESMessage{Headers: headers, Payload: payload})
		consumed += total
		buf = buf[total:]
	}
	return msgs, consumed, nil
}

// InnerEvent 是 event-stream payload 解出来的 {type,payload} 信封。
type InnerEvent struct {
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	MessageID string         `json:"message_id,omitempty"`
	ThreadID  string         `json:"thread_id,omitempty"`
	Scope     string         `json:"scope,omitempty"`
}

// ExtractInner 兼容两种下行形态：
//   {"payload":"<b64>"} 与 {"eventType":..,"payload":{"payload":"<b64>"}}
func ExtractInner(data []byte) (*InnerEvent, error) {
	var outer struct {
		Payload jsonStringOrObject `json:"payload"`
	}
	if err := jsonUnmarshal(data, &outer); err != nil {
		return nil, err
	}
	innerB64 := outer.Payload.String
	if innerB64 == "" && outer.Payload.Object != nil {
		if v, ok := outer.Payload.Object["payload"].(string); ok {
			innerB64 = v
		}
	}
	if innerB64 == "" {
		return nil, fmt.Errorf("no base64 inner payload")
	}
	decoded, err := base64Decode(innerB64)
	if err != nil {
		return nil, err
	}
	var ev InnerEvent
	if err := jsonUnmarshal(decoded, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// NewUUID 生成 v4 UUID，避免引入额外依赖。
func NewUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
