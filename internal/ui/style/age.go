package style

import (
	"fmt"
	"time"
)

// Age is the time since since (epoch seconds) in the popups' short form:
// "42s", "7m", "3h". since 0 (never set) is
// "", and a clock that went backwards shows "0s".
func Age(since int64, now time.Time) string {
	if since <= 0 {
		return ""
	}
	s := max(0, now.Unix()-since)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm", s/60)
	}
	return fmt.Sprintf("%dh", s/3600)
}
