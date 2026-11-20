package ui

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/card"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/delegate"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/history"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/pricing"
	"github.com/HTMLGuyLLC/HometownDiscount/internal/skills"
)

// paceWindow is how far back the "at your pace" estimate looks; paceFloor is
// the shortest span it scales from, so one busy day isn't taken for a month.
const (
	paceWindow = 30
	paceFloor  = 7
)

// Savings is what ask_local has handed to the local models so far, and what
// that's likely to be worth from here.
type Savings struct {
	Tracking   bool      `json:"tracking"` // [privacy] track_handoffs
	Answered   int       `json:"answered"`
	Refused    int       `json:"refused"`
	Failed     int       `json:"failed"`
	TokensKept int       `json:"tokens_kept"`
	Worth      float64   `json:"worth,omitempty"`
	WorthModel string    `json:"worth_model,omitempty"`
	First      time.Time `json:"first,omitzero"` // the first logged call
	ByModel    []Share   `json:"by_model"`
	ByTask     []Share   `json:"by_task"`
	Pace       *Estimate `json:"pace,omitempty"` // per month, at the recent pace
	Room       *Estimate `json:"room,omitempty"` // per month, the most it could be
	RoomStatus string    `json:"room_status"`    // ready, reading (transcripts still being read), error or none
	RoomError  string    `json:"room_error,omitempty"`
	// RefusedWhy counts the refused calls by reason: unproven, slow,
	// unreachable, too_big, context, and other for lines logged before
	// reasons were. Only reasons that happened are listed.
	RefusedWhy map[string]int `json:"refused_why,omitempty"`
}

// Share is one model's or one task's part of the handoffs.
type Share struct {
	Name   string  `json:"name"`
	Tasks  int     `json:"tasks"`
	Tokens int     `json:"tokens"`
	Worth  float64 `json:"worth,omitempty"`
}

// Estimate is a month of handoffs, estimated.
type Estimate struct {
	Tasks  int     `json:"tasks"`
	Tokens int     `json:"tokens"`
	Worth  float64 `json:"worth,omitempty"`
	Days   int     `json:"days"`             // the days it's based on
	Sizes  string  `json:"sizes,omitempty"`  // room: the file sizes counted, in words
	Skills string  `json:"skills,omitempty"` // room: the skills that go local, in words
}

func savings(in Input, rows []card.Row) Savings {
	s := Savings{Tracking: in.Tracking, ByModel: []Share{}, ByTask: []Share{}, RoomStatus: "none"}
	worth := func(tokens int) float64 {
		w, _ := delegate.Worth(tokens, in.Claude, in.Prices)
		return w
	}
	if _, ok := delegate.Worth(1000, in.Claude, in.Prices); ok {
		s.WorthModel = pricing.Display(in.Claude.Model)
	}
	// Bytes are summed and turned into tokens once per group, the way
	// `htd card` totals them, so the two never differ by rounding.
	byModel, byTask := map[string]*Share{}, map[string]*Share{}
	bytesOf := map[*Share]int{}
	add := func(m map[string]*Share, name string, kept int) {
		if m[name] == nil {
			m[name] = &Share{Name: name}
		}
		m[name].Tasks++
		bytesOf[m[name]] += kept
	}
	cutoff := in.Now.AddDate(0, 0, -paceWindow)
	var recentTasks, keptBytes, recentBytes int
	for _, d := range in.Delegations {
		if !d.At.IsZero() && (s.First.IsZero() || d.At.Before(s.First)) {
			s.First = d.At
		}
		switch d.Outcome {
		case "answered":
		case "refused":
			s.Refused++
			if s.RefusedWhy == nil {
				s.RefusedWhy = map[string]int{}
			}
			why := d.Reason
			switch why {
			case delegate.RefusedUnproven, delegate.RefusedSlow, delegate.RefusedUnreachable, delegate.RefusedTooBig, delegate.RefusedContext:
			default:
				why = "other"
			}
			s.RefusedWhy[why]++
			continue
		default:
			s.Failed++
			continue
		}
		kept := d.Kept()
		s.Answered++
		keptBytes += kept
		model := shortModel(d.Model)
		if model == "" {
			model = NotRecorded
		}
		add(byModel, model, kept)
		add(byTask, taskName(skills.TypePrefix+d.Skill)+" · "+sizeWords(true, d.Size), kept)
		if d.At.After(cutoff) {
			recentTasks++
			recentBytes += kept
		}
	}
	s.TokensKept = keptBytes / 4
	s.Worth = worth(s.TokensKept)
	s.ByModel, s.ByTask = shares(byModel, bytesOf, worth), shares(byTask, bytesOf, worth)

	if recentTasks > 0 {
		span := paceWindow
		if days := int(in.Now.Sub(s.First).Hours()/24) + 1; days < span {
			span = max(days, paceFloor)
		}
		scale := float64(paceWindow) / float64(span)
		tokens := int(float64(recentBytes/4) * scale)
		s.Pace = &Estimate{Tasks: int(float64(recentTasks)*scale + 0.5), Tokens: tokens, Worth: worth(tokens), Days: span}
	}

	switch {
	case !in.ReadsReady:
		s.RoomStatus = "reading"
	case in.ReadsErr != "":
		s.RoomStatus = "error"
		s.RoomError = "Couldn't read your Claude Code transcripts, so there's no estimate of what else could go local (" + in.ReadsErr + ")."
	case in.ReadsDays > 0:
		if room := roomToGrow(in.Reads, in.ReadsDays, rows); room != nil {
			room.Worth = worth(room.Tokens)
			s.Room = room
			s.RoomStatus = "ready"
		}
	}
	return s
}

func shares(m map[string]*Share, bytesOf map[*Share]int, worth func(int) float64) []Share {
	out := []Share{}
	for _, sh := range m {
		sh.Tokens = bytesOf[sh] / 4
		sh.Worth = worth(sh.Tokens)
		out = append(out, *sh)
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].Tasks != out[k].Tasks {
			return out[i].Tasks > out[k].Tasks
		}
		return out[i].Name < out[k].Name
	})
	return out
}

// roomToGrow is the most ask_local could take in a month: every file Claude
// read, in your transcripts, at a size some local model has proven a skill
// for and is fast enough at (proof at a size covers the smaller sizes). It's
// a ceiling: not every read is a task a skill check covers.
func roomToGrow(reads []history.ReadSample, days int, rows []card.Row) *Estimate {
	largest := -1
	var names []string
	for _, r := range rows {
		if strings.HasPrefix(r.Type, skills.TypePrefix) && result(r) == Local {
			largest = max(largest, sizeIndex(r.Tier))
			if n := taskName(r.Type); !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	if largest < 0 {
		return nil
	}
	var tasks, keptBytes int
	for _, rd := range reads {
		size := skills.SizeOfInput(rd.Lines, rd.Bytes)
		if size == "" || sizeIndex(size) > largest {
			continue
		}
		tasks++
		keptBytes += delegate.Delegation{Outcome: "answered", Bytes: rd.Bytes}.Kept()
	}
	scale := float64(paceWindow) / float64(max(days, 1))
	return &Estimate{
		Tasks: int(float64(tasks)*scale + 0.5), Tokens: int(float64(keptBytes/4) * scale), Days: days,
		Sizes: "files " + sizeWords(true, skills.SizeOrder[largest]), Skills: strings.Join(names, ", "),
	}
}

func sizeIndex(size string) int {
	for i, s := range skills.SizeOrder {
		if s == size {
			return i
		}
	}
	return -1
}
