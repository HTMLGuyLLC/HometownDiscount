package memory

import "testing"

func TestLow(t *testing.T) {
	for _, c := range []struct {
		s    Status
		want bool
	}{
		{Status{FreePct: 78}, false},
		{Status{FreePct: MinFreePct}, false},
		{Status{FreePct: MinFreePct - 1}, true},
		{Status{FreePct: 40, Pressure: true}, true},
		{Status{FreePct: -1}, false}, // unknown never pauses
	} {
		if got := c.s.Low(); got != c.want {
			t.Errorf("%+v: Low = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestParseMeminfo(t *testing.T) {
	got := parseMeminfo("MemTotal:       16000000 kB\nMemFree:          500000 kB\nMemAvailable:    1200000 kB\n")
	if got.FreePct != 7 || !got.Low() {
		t.Errorf("got %+v, want 7%% free and low", got)
	}
	if got := parseMeminfo("MemTotal: 16000000 kB\nMemFree: 500000 kB\n"); got.FreePct != -1 {
		t.Errorf("no MemAvailable should be unknown, got %+v", got)
	}
}

// The real reading works on this machine and is in range.
func TestRead(t *testing.T) {
	s := Read()
	if s.FreePct < -1 || s.FreePct > 100 {
		t.Errorf("free share out of range: %+v", s)
	}
}
