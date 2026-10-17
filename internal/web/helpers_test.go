package web

import "time"

func timeAt(sec int) time.Time { return time.Unix(1_000_000+int64(sec), 0) }
