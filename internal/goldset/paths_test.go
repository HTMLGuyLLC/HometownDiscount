package goldset

import (
	"reflect"
	"testing"
)

func TestCommandPaths(t *testing.T) {
	cases := []struct {
		cmd, dir string
		want     []string
		after    string
	}{
		// From a real job: cd persists, patterns and scripts are quoted.
		{`cd /r/docs/business; wc -l plan.md setup.md; grep -n '^#' plan.md`, "/r",
			[]string{"/r/docs/business/plan.md", "/r/docs/business/setup.md", "/r/docs/business/plan.md"}, "/r/docs/business"},
		{`awk 'NR>=172 && NR<=207 {print NR": "$0}' plan.md | cut -c1-400 | head -60`, "/r/docs",
			[]string{"/r/docs/plan.md"}, "/r/docs"},
		{`sed -n 1,130p web/src/app.ts | cat -n`, "/r", []string{"/r/web/src/app.ts"}, "/r"},
		{`grep -rn "foo.bar" src/ 2>/dev/null > /dev/null`, "/r", []string{"/r/src"}, "/r"},
		{`jq .name package.json; ls -la; echo done`, "/r", []string{"/r/package.json"}, "/r"},
		{`cd ..; ls ./lib *.go`, "/r/web", []string{"/r/lib"}, "/r"},
		{`cat /etc/hosts`, "/r", []string{"/etc/hosts"}, "/r"},
		{`cd $HOME && ls`, "/r", nil, "/r"},
	}
	for _, c := range cases {
		got, after := CommandPaths(c.cmd, c.dir)
		if !reflect.DeepEqual(got, c.want) || after != c.after {
			t.Errorf("CommandPaths(%q, %q) = %v, %q; want %v, %q", c.cmd, c.dir, got, after, c.want, c.after)
		}
	}
}
