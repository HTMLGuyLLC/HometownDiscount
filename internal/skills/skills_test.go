package skills

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/ladder"
)

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

const goSrc = `package billing

// TODO: handle refunds
func Charge(amount int) error { return nil }

func helper() {}

type Invoice struct{}

func (i *Invoice) Total() int { return 0 } // FIXME rounding
`

func TestGenerate(t *testing.T) {
	dir := repo(t, map[string]string{
		"billing/charge.go": goSrc,
		"billing/refund.go": "package billing\n\nfunc Refund() {}\n",
		"billing/ledger.go": "package billing\n\nfunc Ledger() { Refund() }\n",
		"web/app.ts":        "export function render() {}\nexport const PORT = 8080\nfunction internal() {}\n",
		"config/app.json":   `{"server": {"port": 8080, "host": "localhost"}, "debug": false}`,
		"node_modules/x.js": "export function skipped() {}\n",
	})
	tasks, err := Generate(dir)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string][]Task{}
	for _, tk := range tasks {
		by[tk.Skill] = append(by[tk.Skill], tk)
		if tk.Size != Small || tk.ID == "" {
			t.Errorf("task %+v: want a small task with an id", tk)
		}
		if strings.Contains(tk.Prompt, "node_modules") {
			t.Error("dependency folders must be skipped")
		}
	}
	for _, s := range Order {
		if len(by[s]) == 0 {
			t.Errorf("no %s tasks generated", s)
		}
	}
	for _, tk := range by[Lines] {
		if strings.Contains(tk.Prompt, "word `Invoice`") && tk.Answer != "8,10" {
			t.Errorf("lines answer for Invoice = %q, want 8,10", tk.Answer)
		}
	}
	for _, tk := range by[List] {
		if strings.Contains(tk.Prompt, "charge.go") && tk.Answer != "Charge\nInvoice\nTotal" {
			t.Errorf("go list answer = %q", tk.Answer)
		}
		if strings.Contains(tk.Prompt, "app.ts") && tk.Answer != "PORT\nrender" {
			t.Errorf("ts list answer = %q", tk.Answer)
		}
	}
	again, _ := Generate(dir)
	if len(again) != len(tasks) || again[0].ID != tasks[0].ID {
		t.Error("generation must be deterministic for the same repo")
	}
}

func TestGrade(t *testing.T) {
	cases := []struct {
		task  Task
		reply string
		want  bool
	}{
		{Task{Skill: Lookup, Answer: "4"}, "4", true},
		{Task{Skill: Lookup, Answer: "4"}, "<think>line 3? no, 4</think>\nLine 4.", true},
		{Task{Skill: Lookup, Answer: "4"}, "5", false},
		{Task{Skill: Lines, Answer: "3,10"}, "10, 3", true},
		{Task{Skill: Lines, Answer: "3,10"}, "3", false},
		{Task{Skill: List, Answer: "Charge\nInvoice\nTotal"}, "- `Charge()`\n- Invoice\n- Total", true},
		{Task{Skill: List, Answer: "Charge\nInvoice"}, "Charge\nInvoice\nhelper", false},
		{Task{Skill: Files, Answer: "a/x.go\na/y.go"}, "```\na/y.go\na/x.go\n```", true},
		{Task{Skill: JSON, Answer: `8080`}, "8080", true},
		{Task{Skill: JSON, Answer: `"localhost"`}, `"localhost"`, true},
		{Task{Skill: JSON, Answer: `"localhost"`}, `localhost`, true},
		{Task{Skill: JSON, Answer: `false`}, `"false"`, false},
		{Task{Skill: JSON, Answer: `8080`}, "thinking...</think>8080", true},
	}
	for _, c := range cases {
		if got := Grade(c.task, c.reply); got != c.want {
			t.Errorf("Grade(%s %q, %q) = %v, want %v", c.task.Skill, c.task.Answer, c.reply, got, c.want)
		}
	}
}

func TestSizeOfInput(t *testing.T) {
	if SizeOfInput(10, 1000) != Small || SizeOfInput(10, 20<<10) != Medium || SizeOfInput(300, 1000) != Medium || SizeOfInput(10, 300<<10) != "" {
		t.Error("SizeOfInput should take the larger of the line and byte sizes")
	}
}

func TestSizeOf(t *testing.T) {
	if SizeOf(200) != Small || SizeOf(201) != Medium || SizeOf(1001) != Large || SizeOf(4001) != "" {
		t.Error("SizeOf thresholds wrong")
	}
}

func TestBarIsProvable(t *testing.T) {
	if ladder.WilsonLower(24, 24) < Bar {
		t.Errorf("a perfect 24 of 24 must clear the skill bar %.2f", Bar)
	}
	if PerCell < 75 {
		t.Error("each size needs room for the proof checkpoints up to 75")
	}
}

func TestTrickyDeclarations(t *testing.T) {
	dir := repo(t, map[string]string{
		"web/a.ts":                "export abstract class Shape {}\nexport const enum Mode { A }\nexport default class extends Base {}\nexport declare function ext(): void\n",
		"go/grouped.go":           "package g\n\ntype (\n\tA int\n\tB int\n)\n\nfunc Exported() {}\nfunc Other() {}\n",
		"py/m.py":                 "def handler():\n    pass\n\nclass Box:\n    def handler(self):\n        pass\n",
		".env":                    "SECRET=1\n",
		"config/credentials.json": `{"key": {"id": "abc", "secret": "xyz"}, "other": 1}`,
	})
	os.Symlink("/etc/hosts", filepath.Join(dir, "linked.ts"))
	exec.Command("git", "-C", dir, "add", "linked.ts").Run()
	tasks, err := Generate(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range tasks {
		if strings.Contains(tk.Prompt, "SECRET") || strings.Contains(tk.Prompt, "credentials.json") {
			t.Errorf("a secrets file became a %s check", tk.Skill)
		}
		if strings.Contains(tk.Prompt, "linked.ts") {
			t.Error("symlinks must not become checks")
		}
		if tk.Skill == List && strings.Contains(tk.Prompt, "a.ts") && tk.Answer != "Mode\nShape\next" {
			t.Errorf("ts exports = %q, want Mode, Shape, ext (no 'extends')", tk.Answer)
		}
		if tk.Skill == List && strings.Contains(tk.Prompt, "grouped.go") {
			t.Error("files with grouped type blocks are ambiguous for list checks")
		}
		if tk.Skill == Lookup && strings.Contains(tk.Prompt, "`handler`") {
			t.Error("a name also defined as a method is ambiguous for lookup checks")
		}
	}
}
