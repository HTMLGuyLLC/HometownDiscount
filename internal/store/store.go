// Package store keeps replay results in ~/.htd as an append-only JSON-lines
// file: easy to inspect, back up or delete, and safe if a replay is killed.
package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Run is one graded replay of one job on one rung.
type Run struct {
	JobID     string    `json:"job_id"`   // the subagent's agent id
	JobTime   time.Time `json:"job_time"` // when the original job ran
	Type      string    `json:"type"`
	Tier      string    `json:"tier,omitempty"`  // small or large
	Reference string    `json:"reference_model"` // the model the original job ran on
	Rung      string    `json:"rung"`            // setup identity, see Rung
	Pass      bool      `json:"pass"`
	Graded    bool      `json:"graded"`
	Reason    string    `json:"reason,omitempty"` // why it wasn't graded or failed
	Recall    float64   `json:"recall"`
	Extra     int       `json:"extra,omitempty"`
	Made      []string  `json:"made_up,omitempty"` // fabricated anchors, URLs or facts (first few)
	Severe    bool      `json:"severe,omitempty"`  // made-up paths or unparseable output
	Seconds   float64   `json:"seconds"`
	Turns     int       `json:"turns"`
	At        time.Time `json:"at"` // when the replay ran
	// Regraded is set on a corrected copy of a run that `htd regrade --apply`
	// graded again with newer rules. It keeps the run's At, so its place in
	// the proof order doesn't change; the latest grade of a job wins.
	Regraded time.Time `json:"regraded,omitzero"`
}

// Rung is the setup identity evidence is kept under.
type Rung struct {
	Model   string `json:"model"`
	Quant   string `json:"quant"`
	Server  string `json:"server"` // flavor and version, e.g. "ollama 0.12.3"
	Context int    `json:"context"`
	Loop    string `json:"loop"`
	Effort  string `json:"effort,omitempty"`
}

// ID is the stable key evidence is kept under: model, quant, loop and
// effort. Server version and context are shown but not part of the key,
// because servers report them inconsistently (Ollama and LM Studio report a
// different context depending on whether the model is loaded). Commands
// check the context a task needs against the server's instead.
func (r Rung) ID() string {
	b, _ := json.Marshal(struct {
		Model  string `json:"model"`
		Quant  string `json:"quant"`
		Loop   string `json:"loop"`
		Effort string `json:"effort,omitempty"`
	}{r.Model, r.Quant, r.Loop, r.Effort})
	return string(b)
}

// CanonicalID rewrites a stored rung id in the current key format, so
// results stored under an older format still count.
func CanonicalID(id string) string {
	var r Rung
	if json.Unmarshal([]byte(id), &r) != nil {
		return id
	}
	return r.ID()
}

// Label is the rung in plain words, e.g. "Qwen3.8-27B · 4bit · 262,144 ctx · builtin".
func (r Rung) Label() string {
	s := r.Model
	if r.Quant != "" {
		s += " · " + r.Quant
	}
	if r.Context > 0 {
		s += " · " + commas(r.Context) + " ctx"
	}
	return s + " · " + r.Loop
}

// Store is the results file.
type Store struct{ path string }

// Open returns the store in dir, creating dir if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, "runs.jsonl")}, nil
}

// DefaultDir is ~/.htd, or $HTD_HOME when set.
func DefaultDir() string {
	if d := os.Getenv("HTD_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".htd")
}

// Add appends one run.
func (s *Store) Add(r Run) error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// All returns every run in the order they ran (statistics are evaluated in
// that order everywhere). Unreadable lines are skipped.
func (s *Store) All() ([]Run, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var runs []Run
	latest := map[[2]string]int{} // rung and job -> index in runs
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var r Run
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		r.Rung = CanonicalID(r.Rung)
		k := [2]string{r.Rung, r.JobID}
		if i, ok := latest[k]; ok {
			if r.Regraded.After(runs[i].Regraded) {
				runs[i] = r // a newer grade of the same run
			}
			continue
		}
		latest[k] = len(runs)
		runs = append(runs, r)
	}
	sort.SliceStable(runs, func(i, k int) bool { return runs[i].At.Before(runs[k].At) })
	return runs, sc.Err()
}

// LogJSON appends one JSON line to a named log in the store's folder, such as
// delegations.jsonl. Failures are ignored: logs are for summaries only.
func (s *Store) LogJSON(name string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(s.path), name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// SaveAnswer keeps a replay's answer next to the reference for hand review
// (`htd card --review`). Answers stay in ~/.htd and are never shared.
func (s *Store) SaveAnswer(rungID, jobID, reference, candidate string) error {
	p := s.AnswerPath(rungID, jobID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	body := "# Reference (the original answer)\n\n" + reference + "\n\n# Candidate (the replay)\n\n" + candidate + "\n"
	return os.WriteFile(p, []byte(body), 0o600)
}

// AnswerPath is where a setup's answer to a job is saved. The job id is
// hashed into the name, so an unusual id can't point outside the folder.
func (s *Store) AnswerPath(rungID, jobID string) string {
	name := jobID
	if !plainID.MatchString(jobID) {
		name = shortHash(jobID)
	}
	return filepath.Join(filepath.Dir(s.path), "answers", shortHash(rungID), name+".md")
}

var plainID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:6])
}

// Done reports which jobs already have a run on a rung.
func (s *Store) Done(rungID string) (map[string]bool, error) {
	rungID = CanonicalID(rungID)
	runs, err := s.All()
	if err != nil {
		return nil, err
	}
	done := map[string]bool{}
	for _, r := range runs {
		if r.Rung == rungID {
			done[r.JobID] = true
		}
	}
	return done, nil
}

func commas(n int) string {
	str := strconv.Itoa(n)
	for i := len(str) - 3; i > 0; i -= 3 {
		str = str[:i] + "," + str[i:]
	}
	return str
}
