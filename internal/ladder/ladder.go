// Package ladder holds the statistics that decide when a cheaper rung has
// earned a job type.
package ladder

import "math"

// z for a two-sided 95% Wilson interval.
const z = 1.959963984540054

// Defaults from the plan.
const (
	DefaultMargin = 0.07 // the bar sits this far under the reference's self-agreement
	BarFloor      = 0.70
	DefaultST     = 0.93 // self-agreement when nothing better is known
	PoolWeight    = 0.3  // weight of the published s_t when pooling with the user's own control
	MinPublishedN = 40   // published s_t values from fewer jobs fall back to the default
	MaxLook       = 200
)

// Looks are the sample sizes at which proof is checked. Checking only at
// fixed sizes keeps the false-proof rate at the stated level.
var Looks = []int{24, 37, 50, 75, 100, 150, 200}

// WilsonLower is the lower bound of the 95% Wilson interval for k of n.
func WilsonLower(k, n int) float64 {
	lo, _ := wilson(k, n)
	return lo
}

// WilsonUpper is the upper bound of the 95% Wilson interval for k of n.
func WilsonUpper(k, n int) float64 {
	_, hi := wilson(k, n)
	return hi
}

func wilsonUpper(k, n int, z float64) float64 {
	_, hi := wilsonZ(k, n, z)
	return hi
}

func wilson(k, n int) (float64, float64) { return wilsonZ(k, n, z) }

func wilsonZ(k, n int, z float64) (float64, float64) {
	if n <= 0 {
		return 0, 1
	}
	p := float64(k) / float64(n)
	nf := float64(n)
	denom := 1 + z*z/nf
	center := (p + z*z/(2*nf)) / denom
	half := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	return math.Max(0, center-half), math.Min(1, center+half)
}

// Bar is the pass rate a rung must prove: the reference's self-agreement
// minus the margin, never below the floor.
func Bar(st, margin float64) float64 {
	return math.Max(BarFloor, st-margin)
}

// PooledST pools a user's own control (k of n re-runs on the reference model
// passing) with the published value. A published value from too few jobs is
// replaced by a pseudo-count at the default.
func PooledST(kUser, nUser, kPub, nPub int) float64 {
	pubK, pubN := float64(kPub), float64(nPub)
	if nPub < MinPublishedN {
		pubN = MinPublishedN
		pubK = DefaultST * pubN
	}
	num := float64(kUser) + PoolWeight*pubK
	den := float64(nUser) + PoolWeight*pubN
	if den == 0 {
		return DefaultST
	}
	return num / den
}

// MinPasses is the fewest passes out of n whose Wilson lower bound clears the
// bar, or -1 when even n of n doesn't.
func MinPasses(n int, bar float64) int {
	for k := 0; k <= n; k++ {
		if WilsonLower(k, n) >= bar {
			return k
		}
	}
	return -1
}

// State of a rung on one job type.
type State string

const (
	Learning  State = "learning"
	Proven    State = "proven"
	NotProven State = "not proven at 200"
	NotEnough State = "not good enough" // stopped early: it clearly can't reach the bar
)

// z for a two-sided 99% Wilson interval, used by the early stop.
const z99 = 2.5758293035489

// FutilityLooks are the sample sizes at which a tier can be stopped early.
// Checking at a few points with a 99% bound keeps a good rung (true 0.93,
// bar 0.86) from being stopped by bad luck: 0.34% of the time over 200 jobs,
// against 1.5% for a 95% bound checked after every job.
var FutilityLooks = []int{5, 10, 24, 37, 50, 75, 100, 150, 200}

// Futile reports whether a tier clearly can't reach the bar: at a futility
// look, even the 99% Wilson upper bound is under it.
func Futile(passes, n int, bar float64) bool {
	for _, l := range FutilityLooks {
		if n == l {
			return wilsonUpper(passes, n, z99) < bar
		}
	}
	return false
}

// Evaluate checks graded outcomes (in order, true = pass) against the bar:
// early stop after every job, proof at the fixed looks. Outcomes past the
// last look are ignored.
func Evaluate(outcomes []bool, bar float64) (State, int) {
	passes := 0
	look := 0
	for i, ok := range outcomes {
		if i >= MaxLook {
			break
		}
		if ok {
			passes++
		}
		n := i + 1
		if Futile(passes, n, bar) {
			return NotEnough, n
		}
		if look < len(Looks) && n == Looks[look] {
			look++
			if WilsonLower(passes, n) >= bar {
				return Proven, n
			}
		}
	}
	if len(outcomes) >= MaxLook {
		return NotProven, MaxLook
	}
	return Learning, len(outcomes)
}

// ShouldDemote applies the demotion rule to a proven rung's recent checks:
// the Wilson upper bound of the last 30 outcomes is under the bar, or 2 of
// the last 10 were severe failures (made-up paths, unparseable output).
// severe[i] marks outcome i as a severe failure.
func ShouldDemote(outcomes, severe []bool, bar float64, fastPath bool) bool {
	last := func(xs []bool, n int) []bool {
		if len(xs) > n {
			return xs[len(xs)-n:]
		}
		return xs
	}
	recent := last(outcomes, 30)
	if len(recent) == 30 {
		k := 0
		for _, ok := range recent {
			if ok {
				k++
			}
		}
		if WilsonUpper(k, 30) < bar {
			return true
		}
	}
	if fastPath {
		bad := 0
		for _, s := range last(severe, 10) {
			if s {
				bad++
			}
		}
		if bad >= 2 {
			return true
		}
	}
	return false
}

// BinomialTail is P(X >= k) for X ~ Binomial(n, p).
func BinomialTail(k, n int, p float64) float64 {
	if k <= 0 {
		return 1
	}
	sum := 0.0
	for i := k; i <= n; i++ {
		sum += binomPMF(i, n, p)
	}
	return math.Min(1, sum)
}

func binomPMF(k, n int, p float64) float64 {
	lg := func(x int) float64 { v, _ := math.Lgamma(float64(x) + 1); return v }
	return math.Exp(lg(n) - lg(k) - lg(n-k) + float64(k)*math.Log(p) + float64(n-k)*math.Log1p(-p))
}

// UndoThreshold is the smallest signal count in a window of n jobs at which a
// one-sided exact binomial test against the baseline rate rejects at alpha.
func UndoThreshold(baseline float64, n int, alpha float64) int {
	for k := 0; k <= n; k++ {
		if BinomialTail(k, n, baseline) <= alpha {
			return k
		}
	}
	return n + 1
}
