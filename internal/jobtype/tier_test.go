package jobtype

import (
	"strings"
	"testing"
)

func TestPredictTier(t *testing.T) {
	cases := []struct {
		prompt string
		want   string
	}{
		{"Find where the config loader reads env vars.", Small},
		{"Find every place that charges a card.", Large},
		{"Audit the whole billing module.", Large},
		{"Look across the codebase for unused flags.", Large},
		{"Check web/src/a.ts, web/src/b.ts, api/c.go and lib/d.py for nil checks.", Large},
		{"Check web/src/a.ts and api/c.go for nil checks.", Small},
		{strings.Repeat("context ", 300), Large},
		{"Is it all right to merge?", Large}, // "all" is a crude signal
	}
	for _, c := range cases {
		if got := PredictTier(c.prompt); got != c.want {
			t.Errorf("PredictTier(%.50q) = %s, want %s", c.prompt, got, c.want)
		}
	}
}

func TestActualTier(t *testing.T) {
	if ActualTier(40, 40) != Small || ActualTier(41, 40) != Large || ActualTier(3, 2) != Large {
		t.Error("ActualTier should split at the type's median")
	}
	if ToolCalls(map[string]int{"Bash": 3, "Read": 2}) != 5 {
		t.Error("ToolCalls wrong")
	}
}
