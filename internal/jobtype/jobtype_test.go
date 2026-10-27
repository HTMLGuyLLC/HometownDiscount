package jobtype

import (
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
)

func job(agentType, desc string, tools ...string) *history.Job {
	j := &history.Job{AgentType: agentType, Description: desc, Tools: map[string]int{}}
	for _, t := range tools {
		j.Tools[t]++
	}
	return j
}

func TestOf(t *testing.T) {
	cases := []struct {
		job  *history.Job
		want string
	}{
		{job("Explore", "Find config loader", "Bash", "Read"), Find},
		{job("general-purpose", "Locate all callers of Save()", "Bash"), Find},
		{job("general-purpose", "List all env vars used", "Bash"), Extract},
		{job("general-purpose", "Triage failing tests by cause", "Bash"), Classify},
		{job("general-purpose", "Find all usages of the label component", "Bash"), Find},
		{job("general-purpose", "Where is it configured?", "Bash"), Find},
		{job("general-purpose", "Convert the fixtures to JSON", "Read"), Transform},
		{job("general-purpose", "Fix the login bug", "Read", "Edit"), Edit},
		{job("general-purpose", "Find the best library", "WebSearch"), Research},
		{job("general-purpose", "Check the page renders", "mcp__Claude_Browser__navigate"), Browser},
		{job("Plan", "Find a way to split the module", "Read"), Plan},
		{job("security-scanner", "Security review of auth", "Read"), Review},
		{job("Explore", "Get familiar with the codebase", "Read"), Explore},
		{job("general-purpose", "Find unused exports", "mcp__plugin_context-mode_context-mode__ctx_execute"), Find},
		{job("general-purpose", "Do the thing", "Bash"), Other},
	}
	for _, c := range cases {
		if got := Of(c.job); got != c.want {
			t.Errorf("Of(%q, %q) = %q, want %q", c.job.AgentType, c.job.Description, got, c.want)
		}
	}
}

func TestReplayable(t *testing.T) {
	cases := []struct {
		job  *history.Job
		typ  string
		want bool
	}{
		{job("Explore", "x", "Bash", "Read", "Grep"), Find, true},
		{job("Explore", "x", "Read", "Edit"), Find, false},
		{job("Explore", "x", "Read", "Agent"), Find, false},
		{job("general-purpose", "x", "Read"), Review, true},
		{job("general-purpose", "x", "WebSearch", "WebFetch"), Research, true},
		{job("general-purpose", "x", "WebSearch"), Find, false},
		{job("general-purpose", "x", "Read"), Plan, false},
		{job("general-purpose", "x", "Read", "mcp__plugin_context-mode_context-mode__ctx_execute"), Find, false},
	}
	for _, c := range cases {
		if got := Replayable(c.job, c.typ); got != c.want {
			t.Errorf("Replayable(%v, %q) = %v, want %v", c.job.Tools, c.typ, got, c.want)
		}
	}
}

func cmds(agentType, desc string, commands ...string) *history.Job {
	j := job(agentType, desc, "Bash")
	j.Commands = commands
	return j
}

func TestShellCommands(t *testing.T) {
	writes := []string{"sed -i 's/a/b/' x.ts", "echo hi > notes.md", "cat a >> b", "git commit -m x", "rm -rf build", "python3 -c \"open('f','w').write('x')\"", "ls | tee out.txt",
		"find . -name '*.orig' -delete", "sed --in-place 's/a/b/' f", "go build 2> errors.txt", "git clone https://x/y"}
	for _, c := range writes {
		if !WritesFiles(cmds("general-purpose", "Label the lessons", c)) {
			t.Errorf("%q should count as writing files", c)
		}
		if Of(cmds("general-purpose", "Label the lessons", c)) != Edit {
			t.Errorf("a job that ran %q is an edit job", c)
		}
	}
	reads := []string{"grep -rn foo . 2>&1", "cat a.go > /dev/null", "find . -name '*.ts' | head", "sed -n 1,20p a.go", "ls -la", "git log --oneline | head",
		`grep -rn "=>" src`, "awk '$1 > 5' data.txt", `grep -n "Array<string>" a.ts`, "ls 2>/dev/null"}
	for _, c := range reads {
		if WritesFiles(cmds("Explore", "Find x", c)) {
			t.Errorf("%q shouldn't count as writing files", c)
		}
	}
	if !UsesTooling(cmds("Explore", "Find x", "pnpm test --run")) || Replayable(cmds("Explore", "Find x", "pnpm test"), Find) {
		t.Error("jobs that ran project tooling can't be replayed")
	}
	if UsesTooling(cmds("Explore", "Find x", "grep -rn makeup .")) || UsesTooling(cmds("Explore", "Find x", "ls .github/workflows")) {
		t.Error("tooling words must match whole words")
	}
	if !UsesTooling(cmds("Explore", "Find x", "git log -S charge --oneline")) {
		t.Error("a replay copy has no git history, so git counts as tooling")
	}
}
