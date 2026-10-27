// Package schedule decides which job to replay next: easiest
// types first, small jobs before large, and a tier stops as soon as it's
// clearly not good enough.
package schedule

import (
	"hash/fnv"
	"sort"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/jobtype"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/ladder"
)

// Job is a replay candidate.
type Job struct {
	ID    string
	Type  string
	Tier  string
	Calls int // tool calls the original job made (or input lines); decides the tier
}

// Key is a job type and size tier.
type Key struct{ Type, Tier string }

// VisitBudget is how many jobs one tier takes in a row before the scheduler
// moves on to the next type, so one big type can't use up a whole run. It
// matches the first proof checkpoint.
const VisitBudget = 24

// Order says which types and tiers come first. Replays use the job types
// and two size tiers; skill checks use skills and three input sizes.
type Order struct {
	Types []string
	Tiers []string
}

// Replays is the order for real-job replays.
var Replays = Order{Types: jobtype.TypeOrder, Tiers: jobtype.Tiers}

// Scheduler hands out jobs in order and tracks each tier's evidence.
type Scheduler struct {
	order    Order
	bar      float64
	queues   map[Key][]Job
	outcomes map[Key][]bool
	visit    int // jobs taken in the current visit
	cur      int // index into jobtype.TypeOrder of the type being visited
}

// New builds a scheduler from candidate jobs and the evidence already stored
// for this setup. prior holds graded outcomes in job order.
func New(order Order, jobs []Job, prior map[Key][]bool, bar float64) *Scheduler {
	s := &Scheduler{order: order, bar: bar, queues: map[Key][]Job{}, outcomes: map[Key][]bool{}}
	for k, v := range prior {
		s.outcomes[k] = append([]bool(nil), v...)
	}
	for _, j := range jobs {
		k := Key{j.Type, j.Tier}
		s.queues[k] = append(s.queues[k], j)
	}
	// Within a tier, a fixed pseudo-random order: proof then rests on a fair
	// sample of the tier, not just its easiest jobs. Tiers themselves supply
	// the easy-to-hard progression.
	for k := range s.queues {
		q := s.queues[k]
		sort.SliceStable(q, func(i, n int) bool { return mix(q[i].ID) < mix(q[n].ID) })
	}
	return s
}

func mix(id string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(id))
	return h.Sum64()
}

// State is a tier's status from its evidence so far.
func (s *Scheduler) State(k Key) ladder.State {
	st, _ := ladder.Evaluate(s.outcomes[k], s.bar)
	return st
}

// open reports whether a tier still needs evidence and is allowed to run.
func (s *Scheduler) open(k Key) bool {
	if len(s.queues[k]) == 0 || s.State(k) != ladder.Learning {
		return false
	}
	// A tier opens once every easier tier of the type is proven, or has
	// nothing left to try without having failed.
	for _, easier := range s.order.Tiers {
		if easier == k.Tier {
			return true
		}
		e := Key{k.Type, easier}
		switch s.State(e) {
		case ladder.Proven:
			continue
		case ladder.NotEnough:
			return false
		}
		if len(s.queues[e]) > 0 {
			return false
		}
	}
	return false
}

// Next returns the next job to replay, or false when nothing is left.
func (s *Scheduler) Next() (Job, bool) {
	order := s.order.Types
	for tries := 0; tries <= len(order); tries++ {
		typ := order[s.cur%len(order)]
		if s.visit < VisitBudget {
			for _, tier := range s.order.Tiers {
				k := Key{typ, tier}
				if s.open(k) {
					j := s.queues[k][0]
					s.queues[k] = s.queues[k][1:]
					s.visit++
					return j, true
				}
			}
		}
		s.cur++
		s.visit = 0
	}
	return Job{}, false
}

// Record adds a graded outcome. Ungraded replays don't count either way.
func (s *Scheduler) Record(j Job, graded, pass bool) {
	if !graded {
		return
	}
	k := Key{j.Type, j.Tier}
	s.outcomes[k] = append(s.outcomes[k], pass)
	if s.State(k) != ladder.Learning {
		s.visit = VisitBudget // decided: move on to the next type
	}
}
