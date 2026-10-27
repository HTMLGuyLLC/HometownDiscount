package servers

import "testing"

func TestParseQuantFromName(t *testing.T) {
	tests := []struct {
		id   string
		want Quant
	}{
		// MLX
		{"mlx-community/Qwen3.8-27B-4bit", Quant{"mlx", 4, "4bit", SourceName}},
		{"mlx-community/Qwen3.8-27B-8bit", Quant{"mlx", 8, "8bit", SourceName}},
		{"mlx-community/Qwen3.8-27B-bf16", Quant{"mlx", 16, "bf16", SourceName}},
		{"mlx-community/Foo-3.5bit", Quant{"mlx", 3.5, "3.5bit", SourceName}},
		{"Qwen3.8-27B-4-bit", Quant{"", 4, "4bit", SourceName}},
		{"Qwen3.8-27B-8bit", Quant{"", 8, "8bit", SourceName}},
		// full precision
		{"Qwen3-32B-bf16", Quant{"", 16, "bf16", SourceName}},
		{"Llama-3.1-8B-fp16", Quant{"", 16, "fp16", SourceName}},
		// GGUF
		{"Qwen3-27B-Q4_K_M.gguf", Quant{"gguf", 4, "Q4_K_M", SourceName}},
		{"qwen3:27b-q8_0", Quant{"gguf", 8, "Q8_0", SourceName}},
		{"gemma-3-27b-it-q5_k_s", Quant{"gguf", 5, "Q5_K_S", SourceName}},
		{"hf.co/unsloth/Qwen3-Coder-GGUF:Q6_K", Quant{"gguf", 6, "Q6_K", SourceName}},
		{"unsloth/Qwen3-Coder-UD-Q4_K_XL", Quant{"gguf", 4, "Q4_K_XL", SourceName}},
		{"unsloth/gemma-UD-IQ2_XXS", Quant{"gguf", 2, "IQ2_XXS", SourceName}},
		{"model-F16.gguf", Quant{"gguf", 16, "f16", SourceName}},
		{"mistral-7b-Q4", Quant{"gguf", 4, "Q4", SourceName}},
		// AWQ, GPTQ, int, fp8
		{"Qwen3-32B-AWQ", Quant{"safetensors", 4, "awq", SourceName}},
		{"Qwen3-32B-GPTQ", Quant{"safetensors", 0, "gptq", SourceName}},
		{"Qwen3-32B-GPTQ-Int4", Quant{"safetensors", 4, "int4", SourceName}},
		{"Llama-3-8B-int8", Quant{"", 8, "int8", SourceName}},
		{"Qwen3-235B-FP8", Quant{"safetensors", 8, "fp8", SourceName}},
		{"openai/gpt-oss-20b-MXFP4", Quant{"", 4, "mxfp4", SourceName}},
		{"Qwen3-Next-W4A16", Quant{"safetensors", 4, "w4a16", SourceName}},
		// nothing to find
		{"", Quant{}},
		{"Qwen3.8-27B", Quant{}},
		{"Qwen3.5-27B-A3B", Quant{}},
		{"Qwen3.8-27B-GGUF", Quant{}},
		{"mlx-community/Qwen3.8-27B", Quant{}},
		{"phi-4", Quant{}},
		{"mtplx-bonsai-2-27b-optimized-speed", Quant{}},
	}
	for _, tt := range tests {
		if got := ParseQuantFromName(tt.id); got != tt.want {
			t.Errorf("ParseQuantFromName(%q) = %+v, want %+v", tt.id, got, tt.want)
		}
	}
}

func TestQuantKnown(t *testing.T) {
	if (Quant{}).Known() || ParseQuantFromName("Qwen3-32B-GPTQ").Known() {
		t.Error("zero Quant and bare GPTQ have no known bit width")
	}
	if !ParseQuantFromName("Qwen3-27B-4bit").Known() {
		t.Error("4bit should be known")
	}
}

func TestReportedQuant(t *testing.T) {
	tests := []struct {
		format, level string
		want          Quant
	}{
		{"gguf", "Q4_K_M", Quant{"gguf", 4, "Q4_K_M", SourceServer}},
		{"GGUF", "F16", Quant{"gguf", 16, "F16", SourceServer}},
		{"mlx", "4bit", Quant{"mlx", 4, "4bit", SourceServer}},
		{"", "BF16", Quant{"", 16, "BF16", SourceServer}},
		{"gguf", "MXFP4", Quant{"gguf", 4, "MXFP4", SourceServer}},
		{"gguf", "weird", Quant{"gguf", 0, "weird", SourceServer}}, // bits unknown, still the server's word
		{"gguf", "", Quant{}},
	}
	for _, tt := range tests {
		if got := reportedQuant(tt.format, tt.level); got != tt.want {
			t.Errorf("reportedQuant(%q, %q) = %+v, want %+v", tt.format, tt.level, got, tt.want)
		}
	}
}

func TestWithNameQuant(t *testing.T) {
	// A reported quant wins over the name.
	rep := reportedQuant("gguf", "Q8_0")
	if got := withNameQuant(rep, "m-4bit"); got != rep {
		t.Errorf("reported quant replaced: %+v", got)
	}
	// An unknown width falls back to the name.
	if got := withNameQuant(reportedQuant("", "weird"), "m-4bit"); got.Bits != 4 || got.Source != SourceName {
		t.Errorf("name fallback = %+v", got)
	}
	// Nothing anywhere stays unknown.
	if got := withNameQuant(Quant{}, "m"); got != (Quant{}) {
		t.Errorf("got %+v", got)
	}
}
