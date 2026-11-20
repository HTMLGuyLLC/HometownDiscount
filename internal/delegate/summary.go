package delegate

import (
	"bufio"
	"encoding/json"
	"os"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/speed"
)

// LogName is the file in ~/.htd each ask_local call is logged to.
const LogName = "delegations.jsonl"

// answerGuess is the bytes an answer took before answers were logged.
const answerGuess = 600

// Load reads the ask_local log in the order calls were made. A missing log
// is no calls; unreadable lines are skipped.
func Load(path string) []Delegation {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var ds []Delegation
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var d Delegation
		if json.Unmarshal(sc.Bytes(), &d) == nil {
			ds = append(ds, d)
		}
	}
	return ds
}

// Summary is ask_local use so far.
type Summary struct {
	Answered int
	Refused  int
	// KeptBytes is the file contents kept out of Claude's context, net of
	// the answers that went back.
	KeptBytes int
}

// Summarize totals the calls.
func Summarize(ds []Delegation) Summary {
	var s Summary
	for _, d := range ds {
		switch d.Outcome {
		case "answered":
			s.Answered++
			s.KeptBytes += d.Kept()
		case "refused":
			s.Refused++
		}
	}
	return s
}

// Kept is the bytes one answered call kept out of Claude's context: the
// files minus the answer that went back.
func (d Delegation) Kept() int {
	if d.Outcome != "answered" {
		return 0
	}
	back := d.AnswerBytes
	if back == 0 {
		back = answerGuess
	}
	return max(0, d.Bytes-back)
}

// Tokens is KeptBytes in tokens, at about 4 bytes a token.
func (s Summary) Tokens() int { return s.KeptBytes / 4 }

// Worth is what tokens kept out of Claude's context would have cost at the
// API prices of the Claude model the user works with. A file Claude reads is
// written to its cache once and re-read on each later turn, typically
// RideAlong turns in the user's own transcripts. ok is false when the model
// or its price isn't known.
func Worth(tokens int, c speed.Claude, prices *pricing.Table) (float64, bool) {
	if tokens <= 0 || prices == nil || c.Model == "" {
		return 0, false
	}
	// Priced per 1,000 tokens and scaled, so the total never trips a model's
	// long-prompt price, which applies to single requests.
	unit := pricing.Usage{CacheWrite1h: 1000, CacheRead: int64(1000 * c.RideAlong)}
	per1k, ok := prices.Cost(c.Model, unit)
	if !ok {
		return 0, false
	}
	return per1k * float64(tokens) / 1000, true
}
