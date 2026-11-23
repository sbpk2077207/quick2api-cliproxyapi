package quick

import "fmt"

// Error 描述上游失败，Status/Code 用于 executor 映射为下游错误。
type Error struct {
	Code      string
	Message   string
	Status    int
	Retryable bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("quick2api [%s]: %s", e.Code, e.Message)
}

func NewError(code, msg string, status int, retryable bool) *Error {
	return &Error{Code: code, Message: msg, Status: status, Retryable: retryable}
}

var (
	ErrAuthExpired   = NewError("auth_expired", "Quick session expired, re-login required", 401, false)
	ErrFrameTooLarge = NewError("frame_too_large", "user_message exceeded ~64KB frame budget", 400, false)
	ErrNoSession     = NewError("no_session", "no Quick session available; run browser OAuth login", 401, false)
	ErrTurnTimeout   = NewError("turn_timeout", "upstream turn timed out", 504, true)
)
