package main

import (
	"context"
	"fmt"
	"io"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/config"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/servers"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/store"
)

// runProbe checks a local model server: what it serves, its real context and
// quant, how fast it decodes, and whether its tool calls are well formed.
func runProbe(args []string, stdout, stderr io.Writer) error {
	fs := command("probe", "[--url URL] [--model NAME]", "Checks a model server: its models, context, quantization, speed and tool calls.", stderr)
	url := fs.String("url", "", "server URL (default: your first model's, from config.toml; else "+defaultURL+")")
	model := fs.String("model", "", "model to test (default: your model on that server, else the first one it serves)")
	calls := fs.Int("tool-calls", 20, "tool calls to test (19 of 20 must be valid)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	cfg, err := config.Load(store.DefaultDir())
	if err != nil {
		return err
	}
	// The same choice bench and replay make, minus running them all.
	target := localModels(cfg, *url, *model, false)[0]
	*url, *model = target.URL, target.Model

	if servers.IsRemote(*url) {
		fmt.Fprintln(stdout, "Warning: this server isn't on your machine or local network. Replays would send your code to it.")
	}
	s, err := servers.Probe(ctx, *url)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Server: %s (%s%s)\n", s.URL, s.Flavor, prefixed(" ", s.Version))
	for _, m := range s.Models {
		fmt.Fprintf(stdout, "  %s · %s · %s\n", m.ID, quantLabel(m.Quant), contextLabel(m))
	}
	if len(s.Models) == 0 {
		return fmt.Errorf("the server lists no chat models")
	}

	m := s.Models[0]
	if *model != "" {
		var ok bool
		if m, ok = s.Find(*model); !ok {
			return fmt.Errorf("the server doesn't serve %q", *model)
		}
	}
	if m.Cloud {
		return fmt.Errorf("%s: %w", m.ID, servers.ErrCloudModel)
	}
	if m.ContextLimited() {
		fmt.Fprintf(stdout, "Warning: %s runs with %d tokens of context but supports %d. Raise it to at least %d in your server's settings.\n", m.ID, m.Context, m.MaxContext, min(servers.WantContext, m.MaxContext))
	}

	fmt.Fprintf(stdout, "\nTesting %s...\n", m.ID)
	tps, err := servers.MeasureSpeed(ctx, s, m.ID)
	if err != nil {
		return fmt.Errorf("speed test: %w", err)
	}
	fmt.Fprintf(stdout, "  speed: %.0f tokens/sec\n", tps)
	valid, err := servers.ToolCallTest(ctx, s, m.ID, *calls)
	if err != nil {
		return fmt.Errorf("tool-call test (%d of %d valid so far): %w", valid, *calls, err)
	}
	verdict := "ok"
	if valid*20 < *calls*19 {
		verdict = "too unreliable for replays (needs 19 of 20)"
	}
	fmt.Fprintf(stdout, "  tool calls: %d of %d valid · %s\n", valid, *calls, verdict)
	return nil
}

func quantLabel(q servers.Quant) string {
	switch {
	case q.Detail != "":
		return q.Detail
	case q.Bits > 0:
		return fmt.Sprintf("%g-bit", q.Bits)
	}
	return "quant unknown"
}

func contextLabel(m servers.Model) string {
	if m.Context == 0 {
		return "context unknown"
	}
	return fmt.Sprintf("%d ctx (%s)", m.Context, m.ContextSource)
}

func prefixed(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}
