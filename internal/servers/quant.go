package servers

import (
	"regexp"
	"strconv"
	"strings"
)

// Where a Quant came from.
const (
	SourceServer = "server" // the server reported it
	SourceName   = "name"   // parsed from the model id
)

// Quant describes how a model's weights are quantized.
type Quant struct {
	Format string  // "gguf", "mlx", "safetensors", ""
	Bits   float64 // 4, 8, 16... 0 = unknown
	Detail string  // e.g. "Q4_K_M", "4bit", "bf16"
	Source string  // "server", "name", or "" (unknown: the wizard asks)
}

// Known reports whether the bit width is known. When it isn't, the wizard
// asks the user once.
func (q Quant) Known() bool { return q.Bits > 0 }

// Patterns match whole tokens: the neighbors must not be letters or digits.
const (
	tokStart = `(?:^|[^a-z0-9])`
	tokEnd   = `(?:$|[^a-z0-9])`
)

var (
	// Q4_K_M, Q8_0, q5_k_s, Q4_K_XL, IQ2_XXS, bare Q4.
	ggufRe = regexp.MustCompile(`(?i)` + tokStart + `(i?q(\d)(?:_[0-9k])?(?:_(?:xxs|xs|xxl|xl|nl|s|m|l))?)` + tokEnd)
	// 4bit, 8-bit, 3.5bit (the number must not be the tail of a version like 3.8).
	bitRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9.])(\d+(?:\.\d+)?)[-_]?bit` + tokEnd)
	// int4, int8.
	intRe = regexp.MustCompile(`(?i)` + tokStart + `int(\d+)` + tokEnd)
	// fp8, mxfp4, nvfp4, bf16, fp16, f16, fp32, f32.
	fpRe = regexp.MustCompile(`(?i)` + tokStart + `((?:mx|nv)?fp[48]|bf16|fp16|f16|fp32|f32)` + tokEnd)
	// w4a16, w8a8.
	wxaRe = regexp.MustCompile(`(?i)` + tokStart + `(w(\d)a\d+)` + tokEnd)
	// AWQ is always 4-bit; GPTQ can be 2 to 8, so its width stays unknown.
	awqRe  = regexp.MustCompile(`(?i)` + tokStart + `awq` + tokEnd)
	gptqRe = regexp.MustCompile(`(?i)` + tokStart + `gptq` + tokEnd)
	// Name hints for formats that aren't GGUF or MLX.
	safetensorsRe = regexp.MustCompile(`(?i)` + tokStart + `(awq|gptq|bnb|fp8|nvfp4|w\da\d+)` + tokEnd)
)

var fpBits = map[string]float64{
	"fp4": 4, "mxfp4": 4, "nvfp4": 4, "fp8": 8,
	"bf16": 16, "fp16": 16, "f16": 16, "fp32": 32, "f32": 32,
}

// ParseQuantFromName reads the quantization out of a model id such as
// "mlx-community/Qwen3.8-27B-4bit" or "qwen3:27b-q4_K_M". It returns the zero
// Quant when the id says nothing about it.
func ParseQuantFromName(id string) Quant {
	detail, bits := quantDetail(id)
	if detail == "" {
		return Quant{}
	}
	return Quant{Format: formatOf(id), Bits: bits, Detail: detail, Source: SourceName}
}

// quantDetail finds the first quantization token in s.
func quantDetail(s string) (detail string, bits float64) {
	if m := ggufRe.FindStringSubmatch(s); m != nil {
		return strings.ToUpper(m[1]), atof(m[2])
	}
	if m := bitRe.FindStringSubmatch(s); m != nil {
		return m[1] + "bit", atof(m[1])
	}
	if m := intRe.FindStringSubmatch(s); m != nil {
		return "int" + m[1], atof(m[1])
	}
	if m := fpRe.FindStringSubmatch(s); m != nil {
		d := strings.ToLower(m[1])
		return d, fpBits[d]
	}
	if m := wxaRe.FindStringSubmatch(s); m != nil {
		return strings.ToLower(m[1]), atof(m[2])
	}
	if awqRe.MatchString(s) {
		return "awq", 4
	}
	if gptqRe.MatchString(s) {
		return "gptq", 0
	}
	return "", 0
}

// formatOf guesses the weight format from a model id.
func formatOf(id string) string {
	switch lower := strings.ToLower(id); {
	case strings.Contains(lower, "mlx"):
		return "mlx"
	case strings.Contains(lower, "gguf") || ggufRe.MatchString(lower):
		return "gguf"
	case safetensorsRe.MatchString(lower):
		return "safetensors"
	}
	return ""
}

// reportedQuant builds a Quant from what a server says: a format (may be
// empty) and a level such as "Q4_K_M" or "4bit". Detail keeps the server's
// own spelling; the bit width is read from it.
func reportedQuant(format, level string) Quant {
	level = strings.TrimSpace(level)
	if level == "" {
		return Quant{}
	}
	_, bits := quantDetail(level)
	q := Quant{Format: strings.ToLower(strings.TrimSpace(format)), Bits: bits, Detail: level, Source: SourceServer}
	if q.Format == "" {
		q.Format = formatOf(level)
	}
	return q
}

// withNameQuant fills in a quant the server left unknown from the model id.
func withNameQuant(q Quant, id string) Quant {
	if q.Known() {
		return q
	}
	if n := ParseQuantFromName(id); n.Known() {
		return n
	}
	return q
}

func atof(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
