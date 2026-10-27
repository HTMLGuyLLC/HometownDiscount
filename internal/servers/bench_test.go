package servers

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock makes every speed run take step of wall time.
func fakeClock(t *testing.T, step time.Duration) {
	t.Helper()
	old := now
	t.Cleanup(func() { now = old })
	t0, calls := time.Unix(0, 0), 0
	now = func() time.Time {
		calls++
		return t0.Add(time.Duration(calls-1) * step)
	}
}

func near(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

// recorder keeps the JSON bodies a fake server received.
type recorder struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (r *recorder) add(req *http.Request) int {
	var body map[string]any
	_ = json.NewDecoder(req.Body).Decode(&body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, body)
	return len(r.bodies) - 1
}

func (r *recorder) all() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.bodies...)
}

func TestMeasureSpeedOllama(t *testing.T) {
	var rec recorder
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"POST /api/chat": func(w http.ResponseWriter, r *http.Request) {
			rec.add(r)
			reply(w, obj{"model": "qwen3:27b", "done": true, "eval_count": 200, "eval_duration": 4_000_000_000,
				"prompt_eval_count": 30, "load_duration": 9_000_000_000, "total_duration": 15_000_000_000})
		},
	})
	s := &Server{URL: srv.URL, Flavor: FlavorOllama}
	got, err := MeasureSpeed(t.Context(), s, "qwen3:27b")
	if err != nil {
		t.Fatal(err)
	}
	// Decode speed only: load and prompt time don't count.
	if !near(got, 50) {
		t.Errorf("speed = %v, want 50", got)
	}
	bodies := rec.all()
	if len(bodies) != 1 {
		t.Fatalf("got %d requests, want 1 (Ollama needs no warm-up)", len(bodies))
	}
	opts, _ := bodies[0]["options"].(map[string]any)
	if bodies[0]["stream"] != false || opts["num_predict"] == nil || opts["num_ctx"] != nil {
		t.Errorf("request = %v (must not stream, must cap tokens, must not set num_ctx)", bodies[0])
	}
}

func TestMeasureSpeedOpenAI(t *testing.T) {
	var rec recorder
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"POST /v1/chat/completions": func(w http.ResponseWriter, r *http.Request) {
			rec.add(r)
			// The warm-up asks for one token; the timed run for more.
			tokens := 100
			if bodies := rec.all(); bodies[len(bodies)-1]["max_tokens"] == float64(1) {
				tokens = 1
			}
			reply(w, obj{"choices": []obj{{"message": obj{"content": "1 2 3"}}}, "usage": obj{"completion_tokens": tokens}})
		},
	})
	// LM Studio and generic servers share the OpenAI path.
	for _, flavor := range []string{FlavorOpenAI, FlavorLMStudio} {
		fakeClock(t, 2*time.Second)
		s := &Server{URL: srv.URL, Flavor: flavor}
		got, err := MeasureSpeed(t.Context(), s, "m")
		if err != nil {
			t.Fatalf("%s: %v", flavor, err)
		}
		if !near(got, 50) { // 100 tokens in 2 s
			t.Errorf("%s: speed = %v, want 50", flavor, got)
		}
	}
	bodies := rec.all()
	if len(bodies) != 4 || bodies[0]["max_tokens"] != float64(1) || bodies[1]["max_tokens"] != float64(speedTokens) {
		t.Errorf("want a 1-token warm-up then a %d-token run per call, got %v", speedTokens, bodies)
	}
	if bodies[1]["model"] != "m" || bodies[1]["stream"] != false {
		t.Errorf("request = %v", bodies[1])
	}
}

func TestMeasureSpeedAnthropic(t *testing.T) {
	fakeClock(t, 4*time.Second)
	var rec recorder
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"POST /v1/messages": func(w http.ResponseWriter, r *http.Request) {
			rec.add(r)
			tokens := 160
			if bodies := rec.all(); bodies[len(bodies)-1]["max_tokens"] == float64(1) {
				tokens = 1
			}
			reply(w, obj{"type": "message", "content": []obj{{"type": "text", "text": "1 2 3"}}, "usage": obj{"output_tokens": tokens}})
		},
	})
	got, err := MeasureSpeed(t.Context(), &Server{URL: srv.URL, Flavor: FlavorAnthropic}, "m")
	if err != nil {
		t.Fatal(err)
	}
	if !near(got, 40) { // 160 tokens in 4 s
		t.Errorf("speed = %v, want 40", got)
	}
}

func TestMeasureSpeedErrors(t *testing.T) {
	fakeClock(t, time.Second)
	chat := func(body any, status int) *Server {
		srv := fakeServer(t, map[string]http.HandlerFunc{
			"POST /v1/chat/completions": func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				reply(w, body)
			},
			"POST /api/chat": func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				reply(w, body)
			},
		})
		return &Server{URL: srv.URL, Flavor: FlavorOpenAI}
	}
	usage := func(n int) obj { return obj{"choices": []obj{}, "usage": obj{"completion_tokens": n}} }

	tests := []struct {
		name    string
		server  *Server
		model   string
		wantErr string
	}{
		{"reply too short", chat(usage(3), 200), "m", "too short"},
		{"no usage reported", chat(obj{"choices": []obj{}}, 200), "m", "no generated tokens"},
		{"model not found", chat(obj{"error": "model 'x' not found"}, 404), "x", "HTTP 404"},
		{"ollama zero decode time", func() *Server {
			s := chat(obj{"eval_count": 100, "eval_duration": 0}, 200)
			s.Flavor = FlavorOllama
			return s
		}(), "m", "no decode time"},
		{"not probed", &Server{}, "m", "not probed"},
		{"nil server", nil, "m", "not probed"},
		{"empty model", chat(usage(100), 200), "", "model name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MeasureSpeed(t.Context(), tt.server, tt.model)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestCloudModelsAreRefused(t *testing.T) {
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("a cloud model must not reach the network: %s %s", r.Method, r.URL.Path)
		},
	})
	s := &Server{URL: srv.URL, Flavor: FlavorOllama, Models: []Model{{ID: "gpt-oss:120b", Cloud: true}}}
	for _, model := range []string{"gpt-oss:120b", "kimi-k2:cloud", "gpt-oss:120b-cloud", "Kimi-K2:CLOUD"} {
		if _, err := MeasureSpeed(t.Context(), s, model); !errors.Is(err, ErrCloudModel) {
			t.Errorf("MeasureSpeed(%q) err = %v", model, err)
		}
		if _, err := ToolCallTest(t.Context(), s, model, 3); !errors.Is(err, ErrCloudModel) {
			t.Errorf("ToolCallTest(%q) err = %v", model, err)
		}
	}
}

// scripted serves the given replies in order, one per request.
func scripted(t *testing.T, path string, rec *recorder, replies []func(http.ResponseWriter)) *Server {
	t.Helper()
	srv := fakeServer(t, map[string]http.HandlerFunc{
		"POST " + path: func(w http.ResponseWriter, r *http.Request) {
			i := rec.add(r)
			if i >= len(replies) {
				t.Errorf("unexpected request %d", i)
				return
			}
			replies[i](w)
		},
	})
	return &Server{URL: srv.URL, Flavor: FlavorOpenAI}
}

// oaTools builds an OpenAI-style reply with the given tool calls ({name, arguments}).
func oaTools(calls ...[2]any) func(http.ResponseWriter) {
	var tc []obj
	for _, c := range calls {
		tc = append(tc, obj{"id": "call_1", "type": "function", "function": obj{"name": c[0], "arguments": c[1]}})
	}
	return func(w http.ResponseWriter) {
		reply(w, obj{"choices": []obj{{"message": obj{"role": "assistant", "content": nil, "tool_calls": tc}}}})
	}
}

func TestToolCallTestOpenAI(t *testing.T) {
	var rec recorder
	s := scripted(t, "/v1/chat/completions", &rec, []func(http.ResponseWriter){
		oaTools([2]any{"get_weather", `{"city":"Paris"}`}),      // 1: valid
		oaTools([2]any{"get_weather", `{"city": "Tokyo"`}),      // malformed: cut-off JSON
		oaTools([2]any{"get_weather", ` { "city" : "Lima" } `}), // 2: valid, odd spacing
		func(w http.ResponseWriter) { // no tool call at all
			reply(w, obj{"choices": []obj{{"message": obj{"role": "assistant", "content": "It is sunny."}}}})
		},
		oaTools([2]any{"get_forecast", `{"city":"Sydney"}`}),                                         // wrong tool
		oaTools([2]any{"get_weather", `{}`}),                                                         // required arg missing
		oaTools([2]any{"get_weather", obj{"city": "Mumbai"}}),                                        // arguments not a JSON string
		oaTools([2]any{"get_weather", `{"city":"Paris"}`}, [2]any{"get_weather", `{"city":`}),        // one bad call spoils it
		oaTools([2]any{"get_weather", `{"city":"Oslo"}`}, [2]any{"get_weather", `{"city":"Cairo"}`}), // 3: two good calls
		oaTools([2]any{"get_weather", `{"city":42}`}),                                                // city has the wrong type
		oaTools([2]any{"get_weather", ``}),                                                           // empty arguments
	})
	got, err := ToolCallTest(t.Context(), s, "m", 11)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Errorf("valid = %d, want 3", got)
	}

	bodies := rec.all()
	b := bodies[0]
	tools, _ := b["tools"].([]any)
	if len(tools) != 1 || b["model"] != "m" || b["stream"] != false || b["max_tokens"] == nil {
		t.Fatalf("request = %v", b)
	}
	fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "get_weather" || fn["parameters"] == nil {
		t.Errorf("tool = %v", tools[0])
	}
	if p0, p1 := prompt(bodies[0]), prompt(bodies[1]); p0 == p1 || !strings.Contains(p0, "Paris") {
		t.Errorf("prompts should differ per call and name a city: %q, %q", p0, p1)
	}
}

func prompt(body map[string]any) string {
	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		return ""
	}
	s, _ := msgs[0].(map[string]any)["content"].(string)
	return s
}

func TestToolCallTestAnthropic(t *testing.T) {
	use := func(name string, input any) obj {
		return obj{"type": "tool_use", "id": "toolu_1", "name": name, "input": input}
	}
	msg := func(blocks ...obj) func(http.ResponseWriter) {
		return func(w http.ResponseWriter) { reply(w, obj{"type": "message", "content": blocks}) }
	}
	var rec recorder
	s := scripted(t, "/v1/messages", &rec, []func(http.ResponseWriter){
		msg(use("get_weather", obj{"city": "Paris"})),                                          // 1: valid
		msg(use("get_weather", obj{})),                                                         // missing city
		msg(obj{"type": "text", "text": "It is sunny."}),                                       // no tool call
		msg(use("get_forecast", obj{"city": "Oslo"})),                                          // wrong tool
		msg(obj{"type": "text", "text": "Checking."}, use("get_weather", obj{"city": "Lima"})), // 2: text + valid call
	})
	s.Flavor = FlavorAnthropic
	got, err := ToolCallTest(t.Context(), s, "m", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Errorf("valid = %d, want 2", got)
	}
	tools, _ := rec.all()[0]["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["input_schema"] == nil {
		t.Errorf("tools = %v", tools)
	}
}

func TestToolCallTestStopsOnHTTPError(t *testing.T) {
	var rec recorder
	s := scripted(t, "/v1/chat/completions", &rec, []func(http.ResponseWriter){
		oaTools([2]any{"get_weather", `{"city":"Paris"}`}),
		oaTools([2]any{"get_weather", `{"city":"Tokyo"}`}),
		func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			reply(w, obj{"error": obj{"message": "this model does not support tools"}})
		},
	})
	got, err := ToolCallTest(t.Context(), s, "m", 5)
	if got != 2 || err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "does not support tools") {
		t.Errorf("valid = %d, err = %v", got, err)
	}
	if n := len(rec.all()); n != 3 {
		t.Errorf("sent %d requests, want 3 (stop at the failure)", n)
	}
}

func TestToolCallTestBadArgs(t *testing.T) {
	s := &Server{URL: "http://127.0.0.1:1", Flavor: FlavorOpenAI}
	for _, n := range []int{0, -1} {
		if _, err := ToolCallTest(t.Context(), s, "m", n); err == nil {
			t.Errorf("n = %d: want an error", n)
		}
	}
	if _, err := ToolCallTest(t.Context(), nil, "m", 1); err == nil {
		t.Error("nil server: want an error")
	}
}

func TestValidArgs(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{`{"city":"Paris"}`, true},
		{`{"city":"Paris","units":"c"}`, true},
		{`{"city":" "}`, false},
		{`{"city":42}`, false},
		{`{"city":null}`, false},
		{`{}`, false},
		{`[]`, false},
		{`"Paris"`, false},
		{`{"city":"Paris"`, false},
		{``, false},
	}
	for _, tt := range tests {
		if got := validArgs([]byte(tt.in)); got != tt.want {
			t.Errorf("validArgs(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
