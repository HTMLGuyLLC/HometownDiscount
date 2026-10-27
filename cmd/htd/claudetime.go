package main

import (
	"fmt"
	"io"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
)

// claudeTimesMaxAge is how long Claude's measured times are reused before
// they're measured again from the transcripts.
const claudeTimesMaxAge = 7 * 24 * time.Hour

// claudeTimes returns how long the Claude model the user works with takes to
// answer after reading a file, which the speed limits compare against. want
// is the [speed] claude_model setting: "auto" picks the model used most in
// the last few days, so switching models re-measures. A history scan the
// caller already did is measured and saved; otherwise the saved times are
// used, measured again (about 20 seconds) when they're for another model,
// missing, or a week old.
func claudeTimes(dir, root string, h *history.History, days int, want string, w io.Writer) speed.Claude {
	model := pricing.Normalize(want)
	if model == "" || model == "auto" {
		model = history.RecentModel(root, 3*24*time.Hour)
	}
	if h != nil {
		c := speed.Estimate(h.Reads, days, model)
		_ = speed.Save(dir, c)
		return c
	}
	if c, ok := speed.Load(dir); ok && c.Model == model && time.Since(c.At) < claudeTimesMaxAge {
		return c
	}
	if w != nil {
		fmt.Fprintln(w, "Timing Claude from your transcripts (when your model changes, or weekly; about 20 seconds)...")
		fmt.Fprintln(w)
	}
	scanned, err := history.Scan(root, time.Now().AddDate(0, 0, -days))
	if err != nil {
		c, _ := speed.Load(dir) // older times beat none
		return c
	}
	c := speed.Estimate(scanned.Reads, days, model)
	_ = speed.Save(dir, c)
	return c
}
