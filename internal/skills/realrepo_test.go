package skills

import (
	"os"
	"testing"
)

// Run with HTD_GEN_REPO=/path/to/repo to see how many checks a real repo yields.
func TestGenerateRealRepo(t *testing.T) {
	r := os.Getenv("HTD_GEN_REPO")
	if r == "" {
		t.Skip("set HTD_GEN_REPO")
	}
	tasks, err := Generate(r)
	if err != nil {
		t.Fatal(err)
	}
	cells := map[string]int{}
	for _, tk := range tasks {
		cells[tk.Skill+"/"+tk.Size]++
	}
	for _, s := range Order {
		t.Logf("%-7s small %2d  medium %2d  large %2d", s, cells[s+"/small"], cells[s+"/medium"], cells[s+"/large"])
	}
}
