package schedule

import (
	"fmt"
	"testing"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/ladder"
)

func jobs(typ, tier string, n, calls int) []Job {
	var out []Job
	for i := 0; i < n; i++ {
		out = append(out, Job{ID: fmt.Sprintf("%s-%s-%d", typ, tier, i), Type: typ, Tier: tier, Calls: calls + n - i})
	}
	return out
}

// drive runs the scheduler, answering every job with pass(job).
func drive(s *Scheduler, pass func(Job) bool) []Job {
	var seen []Job
	for {
		j, ok := s.Next()
		if !ok {
			return seen
		}
		seen = append(seen, j)
		s.Record(j, true, pass(j))
	}
}

func TestEasiestFirstAndSmallFirst(t *testing.T) {
	var all []Job
	all = append(all, jobs(jobtype.Review, jobtype.Small, 3, 10)...)
	all = append(all, jobs(jobtype.Classify, jobtype.Large, 3, 50)...)
	all = append(all, jobs(jobtype.Classify, jobtype.Small, 3, 5)...)
	s := New(Replays, all, nil, 0.86)
	seen := drive(s, func(Job) bool { return true })
	if seen[0].Type != jobtype.Classify || seen[0].Tier != jobtype.Small {
		t.Fatalf("first job = %+v, want a small classify job", seen[0])
	}
	// Small classify runs out without failing, so large classify opens next.
	if seen[3].Type != jobtype.Classify || seen[3].Tier != jobtype.Large {
		t.Errorf("4th job = %+v, want large classify", seen[3])
	}
	if len(seen) != 9 {
		t.Errorf("ran %d jobs, want all 9", len(seen))
	}
}

func TestEarlyStopSkipsTheRestOfTheType(t *testing.T) {
	var all []Job
	all = append(all, jobs(jobtype.Find, jobtype.Small, 30, 10)...)
	all = append(all, jobs(jobtype.Find, jobtype.Large, 30, 60)...)
	all = append(all, jobs(jobtype.Review, jobtype.Small, 3, 10)...)
	s := New(Replays, all, nil, 0.86)
	seen := drive(s, func(j Job) bool { return j.Type != jobtype.Find })
	finds := 0
	for _, j := range seen {
		if j.Type == jobtype.Find {
			finds++
			if j.Tier == jobtype.Large {
				t.Error("large find ran after small find failed")
			}
		}
	}
	if finds != 5 {
		t.Errorf("ran %d find jobs, want 5 (stopped early)", finds)
	}
	if s.State(Key{jobtype.Find, jobtype.Small}) != ladder.NotEnough {
		t.Error("small find should be not good enough")
	}
}

func TestVisitBudgetSharesTheRun(t *testing.T) {
	var all []Job
	all = append(all, jobs(jobtype.Classify, jobtype.Small, 100, 5)...)
	all = append(all, jobs(jobtype.Review, jobtype.Small, 100, 5)...)
	s := New(Replays, all, nil, 0.86)
	// Alternate pass/fail-ish so neither tier decides quickly: 3 of 4 pass.
	n := 0
	var first []Job
	for len(first) < 30 {
		j, ok := s.Next()
		if !ok {
			break
		}
		first = append(first, j)
		n++
		s.Record(j, true, n%4 != 0)
	}
	reviews := 0
	for _, j := range first {
		if j.Type == jobtype.Review {
			reviews++
		}
	}
	if reviews == 0 {
		t.Error("an undecided easy type shouldn't block harder types for the whole run")
	}
}

func TestPriorEvidence(t *testing.T) {
	prior := map[Key][]bool{{jobtype.Find, jobtype.Small}: {false, false, false, false, false}}
	s := New(Replays, jobs(jobtype.Find, jobtype.Small, 10, 5), prior, 0.86)
	if _, ok := s.Next(); ok {
		t.Error("a tier already stopped early shouldn't run again on the same setup")
	}
}

func TestThreeTiers(t *testing.T) {
	order := Order{Types: []string{"lookup"}, Tiers: []string{"small", "medium", "large"}}
	var all []Job
	all = append(all, jobs("lookup", "large", 3, 3000)...)
	all = append(all, jobs("lookup", "medium", 3, 500)...)
	all = append(all, jobs("lookup", "small", 5, 50)...)
	s := New(order, all, nil, 0.90)
	// Small fails, so medium and large never open.
	seen := drive(s, func(j Job) bool { return false })
	for _, j := range seen {
		if j.Tier != "small" {
			t.Fatalf("ran a %s job after small failed", j.Tier)
		}
	}
	// With small passing and exhausted, medium opens before large.
	s = New(order, all, nil, 0.90)
	seen = drive(s, func(j Job) bool { return true })
	if seen[5].Tier != "medium" || seen[8].Tier != "large" {
		t.Errorf("order = %v", seen)
	}
}
