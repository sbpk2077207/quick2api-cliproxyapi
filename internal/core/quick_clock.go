package core

import "time"

// NowUnix 统一时间源，便于测试注入。
var NowUnix = func() int64 { return time.Now().Unix() }
