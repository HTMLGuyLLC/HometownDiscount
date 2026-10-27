// Package pricing turns token usage into API-equivalent dollars.
package pricing

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed prices.toml
var pricesTOML []byte

// Price is a model's price in USD per million tokens.
type Price struct {
	Family         string  `toml:"family"`
	Input          float64 `toml:"input"`
	Output         float64 `toml:"output"`
	CacheRead      float64 `toml:"cache_read"`
	FastMultiplier float64 `toml:"fast_multiplier"`
	LongThreshold  int64   `toml:"long_threshold"`
	LongInput      float64 `toml:"long_input"`
	LongOutput     float64 `toml:"long_output"`
	LongCacheRead  float64 `toml:"long_cache_read"`
}

// Usage is the token usage of one API request.
type Usage struct {
	Input        int64
	CacheWrite5m int64
	CacheWrite1h int64
	CacheRead    int64
	Output       int64
	Fast         bool
}

// PromptTokens is the full prompt size: uncached input plus cache writes and reads.
func (u Usage) PromptTokens() int64 {
	return u.Input + u.CacheWrite5m + u.CacheWrite1h + u.CacheRead
}

// Add returns the sum of two usages. Fast is kept if either was fast.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		Input:        u.Input + o.Input,
		CacheWrite5m: u.CacheWrite5m + o.CacheWrite5m,
		CacheWrite1h: u.CacheWrite1h + o.CacheWrite1h,
		CacheRead:    u.CacheRead + o.CacheRead,
		Output:       u.Output + o.Output,
		Fast:         u.Fast || o.Fast,
	}
}

// Table holds prices by normalized model id.
type Table struct {
	Models map[string]Price `toml:"models"`
}

// Default returns the embedded price table.
func Default() (*Table, error) {
	var t Table
	if _, err := toml.Decode(string(pricesTOML), &t); err != nil {
		return nil, fmt.Errorf("parse prices.toml: %w", err)
	}
	return &t, nil
}

var (
	bracketSuffix = regexp.MustCompile(`\[[^\]]*\]$`)
	dateSuffix    = regexp.MustCompile(`-\d{8}$`)
)

// Display is a model id in plain words: "claude-opus-5-5" -> "Opus 5.5",
// "claude-3-5-haiku" -> "Haiku 3.5". Unknown shapes come back unchanged.
func Display(model string) string {
	parts := strings.Split(strings.TrimPrefix(Normalize(model), "claude-"), "-")
	var family string
	var version []string
	for _, p := range parts {
		switch {
		case p == "":
		case p[0] >= '0' && p[0] <= '9':
			version = append(version, p)
		case family == "":
			family = strings.ToUpper(p[:1]) + p[1:]
		default:
			return model
		}
	}
	if family == "" {
		return model
	}
	if len(version) == 0 {
		return family
	}
	return family + " " + strings.Join(version, ".")
}

// Normalize strips context-window tags like "[1m]" and date snapshots like
// "-20251001" so transcript model names match the table.
func Normalize(model string) string {
	m := strings.TrimSpace(strings.ToLower(model))
	m = bracketSuffix.ReplaceAllString(m, "")
	m = dateSuffix.ReplaceAllString(m, "")
	return m
}

// Lookup finds a model's price.
func (t *Table) Lookup(model string) (Price, bool) {
	p, ok := t.Models[Normalize(model)]
	return p, ok
}

// Cost is the API-equivalent cost of one request on the given model.
// Fast mode is only billed at the fast rate on models that support it.
func (t *Table) Cost(model string, u Usage) (float64, bool) {
	p, ok := t.Lookup(model)
	if !ok {
		return 0, false
	}
	in, out, read := p.Input, p.Output, p.CacheRead
	if p.LongThreshold > 0 && u.PromptTokens() > p.LongThreshold {
		in, out, read = p.LongInput, p.LongOutput, p.LongCacheRead
	}
	usd := (float64(u.Input)*in +
		float64(u.CacheWrite5m)*in*1.25 +
		float64(u.CacheWrite1h)*in*2 +
		float64(u.CacheRead)*read +
		float64(u.Output)*out) / 1e6
	if u.Fast && p.FastMultiplier > 0 {
		usd *= p.FastMultiplier
	}
	return usd, true
}
