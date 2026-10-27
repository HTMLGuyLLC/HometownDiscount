package main

import (
	"fmt"
	"io"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
)

// claudeTimesMaxAge is how long Claude's measured times are reused before
// they're measured again from the transcripts.
const claudeTimesMaxAge = 7 * 24 * time.Hour

// claudeTimes returns Claude's typical times to answer after reading a file,
// which the speed limits compare against. A history scan the caller already
// did is measured and saved; otherwise the saved times are used, measured
// again (about 20 seconds) when they're missing or a week old.
func claudeTimes(dir string, h *history.History, days int, w io.Writer) speed.Claude {
	if h != nil {
		c := speed.Estimate(h.Reads, days)
		_ = speed.Save(dir, c)
		return c
	}
	if c, ok := speed.Load(dir); ok && time.Since(c.At) < claudeTimesMaxAge {
		return c
	}
	if w != nil {
		fmt.Fprintln(w, "Timing Claude from your transcripts (once a week, about 20 seconds)...")
		fmt.Fprintln(w)
	}
	scanned, err := history.Scan(history.DefaultRoot(), time.Now().AddDate(0, 0, -days))
	if err != nil {
		c, _ := speed.Load(dir) // older times beat none
		return c
	}
	c := speed.Estimate(scanned.Reads, days)
	_ = speed.Save(dir, c)
	return c
}
