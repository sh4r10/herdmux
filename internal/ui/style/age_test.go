package style

import (
	"testing"
	"time"
)

func TestAge(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000_000, 0)
	for ago, want := range map[int64]string{0: "0s", 59: "59s", 60: "1m", 3599: "59m", 3600: "1h", 86400: "24h", -5: "0s"} {
		if got := Age(now.Unix()-ago, now); got != want {
			t.Errorf("%ds ago: %q, want %q", ago, got, want)
		}
	}
	if Age(0, now) != "" {
		t.Error("unset since")
	}
}
