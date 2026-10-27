package ladder

import (
	"math"
	"testing"
)

// Known proof thresholds at each look.
func TestMinPassesMatchesPlan(t *testing.T) {
	cases := []struct {
		bar  float64
		want map[int]int
	}{
		{0.86, map[int]int{24: 24, 37: 36, 50: 48, 75: 71, 100: 93, 150: 138, 200: 182}},
		{0.93 - 0.07 + 0.0027, map[int]int{24: -1, 37: 37, 50: 48, 75: 71, 100: 94, 150: 138, 200: 183}}, // 97/104 - 0.07 ≈ 0.8627
	}
	for _, c := range cases {
		for n, want := range c.want {
			if got := MinPasses(n, c.bar); got != want {
				t.Errorf("bar %.4f, n=%d: MinPasses = %d, want %d", c.bar, n, got, want)
			}
		}
	}
}

func TestWilson(t *testing.T) {
	if lo := WilsonLower(24, 24); math.Abs(lo-0.8619) > 0.0005 {
		t.Errorf("WilsonLower(24,24) = %.4f", lo)
	}
	if hi := WilsonUpper(0, 10); hi < 0.27 || hi > 0.29 {
		t.Errorf("WilsonUpper(0,10) = %.4f", hi)
	}
	if lo := WilsonLower(0, 0); lo != 0 {
		t.Errorf("WilsonLower(0,0) = %v", lo)
	}
}

func TestBarAndPooling(t *testing.T) {
	if b := Bar(0.93, DefaultMargin); math.Abs(b-0.86) > 1e-9 {
		t.Errorf("Bar(0.93) = %v", b)
	}
	if b := Bar(0.60, DefaultMargin); b != BarFloor {
		t.Errorf("Bar(0.60) = %v, want floor", b)
	}
	// Pooled with 97/104, a user at 26/30 moves the bar from 0.86 to 0.83.
	st := PooledST(26, 30, 97, 104)
	if b := Bar(st, DefaultMargin); math.Abs(b-0.83) > 0.005 {
		t.Errorf("pooled bar = %.4f, want ~0.83", b)
	}
	if b := Bar(PooledST(24, 30, 97, 104), DefaultMargin); math.Abs(b-0.80) > 0.005 {
		t.Errorf("pooled bar at 24/30 = %.4f, want ~0.80", b)
	}
	if b := Bar(PooledST(29, 30, 97, 104), DefaultMargin); math.Abs(b-0.88) > 0.005 {
		t.Errorf("pooled bar at 29/30 = %.4f, want ~0.88", b)
	}
	// A published value from too few jobs uses the default pseudo-count.
	if st := PooledST(0, 0, 5, 10); math.Abs(st-DefaultST) > 1e-9 {
		t.Errorf("PooledST with tiny published n = %v", st)
	}
}

func TestEvaluate(t *testing.T) {
	all := func(n int, fail ...int) []bool {
		out := make([]bool, n)
		for i := range out {
			out[i] = true
		}
		for _, f := range fail {
			out[f] = false
		}
		return out
	}
	if s, n := Evaluate(all(24), 0.86); s != Proven || n != 24 {
		t.Errorf("24/24 at 0.86: %v %d", s, n)
	}
	if s, _ := Evaluate(all(23), 0.86); s != Learning {
		t.Errorf("23 samples: %v", s)
	}
	// One miss among the first 24 delays proof to a later look.
	if s, n := Evaluate(all(37, 3), 0.86); s != Proven || n != 37 {
		t.Errorf("36/37: %v %d", s, n)
	}
	half := make([]bool, 200)
	for i := range half {
		half[i] = i%2 == 0
	}
	if s, n := Evaluate(half, 0.86); s != NotEnough || n != 10 {
		t.Errorf("50%% pass rate: %v at %d, want not good enough at 10 (3 of 5 isn't enough to stop)", s, n)
	}
	// A borderline rung (82%) is neither stopped early nor proven by 200.
	near := make([]bool, 200)
	for i := range near {
		near[i] = i%11 != 0 && i%11 != 5 // 9 of every 11 pass, misses spread out
	}
	if s, _ := Evaluate(near, 0.86); s != NotProven {
		t.Errorf("82%% over 200: %v, want not proven at 200", s)
	}
}

func TestShouldDemote(t *testing.T) {
	good := make([]bool, 30)
	for i := range good {
		good[i] = true
	}
	if ShouldDemote(good, make([]bool, 30), 0.86, true) {
		t.Error("all passes should not demote")
	}
	bad := make([]bool, 30) // 0 of 30
	if !ShouldDemote(bad, make([]bool, 30), 0.86, false) {
		t.Error("0 of 30 should demote")
	}
	severe := make([]bool, 10)
	severe[2], severe[7] = true, true
	if !ShouldDemote(good[:10], severe, 0.86, true) {
		t.Error("2 severe in last 10 should demote on the fast path")
	}
	if ShouldDemote(good[:10], severe, 0.86, false) {
		t.Error("fast path off: severe count alone should not demote")
	}
}

// Known undo thresholds and false-undo rates.
func TestUndoMatchesPlan(t *testing.T) {
	if k := UndoThreshold(0.05, 60, 0.01); k != 8 {
		t.Errorf("baseline 5%%: threshold %d, want 8", k)
	}
	if p := BinomialTail(8, 60, 0.05); math.Abs(p-0.0098) > 0.0005 {
		t.Errorf("false undo at 5%% = %.4f, want ~0.0098", p)
	}
	if k := UndoThreshold(0.10, 60, 0.01); k != 13 {
		t.Errorf("baseline 10%%: threshold %d, want 13", k)
	}
	if p := BinomialTail(13, 60, 0.10); math.Abs(p-0.0057) > 0.0005 {
		t.Errorf("false undo at 10%% = %.4f, want ~0.0057", p)
	}
	if p := BinomialTail(8, 60, 0.15); math.Abs(p-0.70) > 0.02 {
		t.Errorf("power at 3x of 5%% = %.3f, want ~0.70", p)
	}
}

func TestFutile(t *testing.T) {
	if !Futile(0, 5, 0.86) {
		t.Error("0 of 5 should stop early at a 0.86 bar")
	}
	if Futile(0, 4, 0.86) {
		t.Error("never stop before 5 jobs")
	}
	if Futile(3, 5, 0.86) {
		t.Error("3 of 5 must keep going")
	}
	if Futile(0, 7, 0.86) {
		t.Error("only check at the futility looks")
	}
	if s, n := Evaluate([]bool{false, false, false, false, false, true, true}, 0.86); s != NotEnough || n != 5 {
		t.Errorf("Evaluate = %v at %d, want not good enough at 5", s, n)
	}
}

// The plan claims early stop almost never ends a good rung. Check it exactly
// by walking every pass/fail path, for a rung at the plan's "true-0.93" level.
func TestFutilityRarelyStopsAGoodRung(t *testing.T) {
	const p, bar, horizon = 0.93, 0.86, 200
	// prob[k] = probability of k passes so far without having stopped.
	prob := map[int]float64{0: 1}
	stopped := 0.0
	for n := 1; n <= horizon; n++ {
		next := map[int]float64{}
		for k, pr := range prob {
			next[k+1] += pr * p
			next[k] += pr * (1 - p)
		}
		for k, pr := range next {
			if Futile(k, n, bar) {
				stopped += pr
				delete(next, k)
			}
		}
		prob = next
	}
	if stopped > 0.005 {
		t.Errorf("a true-%.2f rung is stopped early %.2f%% of the time over %d jobs", p, stopped*100, horizon)
	}
	t.Logf("true-%.2f rung stopped early: %.3f%% over %d jobs", p, stopped*100, horizon)
}
