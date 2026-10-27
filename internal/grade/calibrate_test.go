package grade

import (
	"reflect"
	"testing"
)

func TestCalibrate(t *testing.T) {
	ref := []string{"missing path: a/gone.go", "quote not found: useX(y)", "cited line lacks `f`: a.go:3"}
	cand := []string{
		"missing path: a/gone.go",                             // shared with the reference: dropped
		"missing path: b/invented.go",                         // high precision: kept
		"quote not found: other(z)",                           // the reference trips quotes: dropped
		"cited line lacks `g`: b.go:9",                        // the reference trips this kind: dropped
		"line past end of file: a.go:900 (file has 10 lines)", // kept
	}
	want := []string{"missing path: b/invented.go", "line past end of file: a.go:900 (file has 10 lines)"}
	if got := calibrate(cand, ref); !reflect.DeepEqual(got, want) {
		t.Errorf("calibrate = %v, want %v", got, want)
	}
	if got := calibrate(cand, nil); len(got) != len(cand) {
		t.Errorf("a clean reference keeps every check: %v", got)
	}
}
