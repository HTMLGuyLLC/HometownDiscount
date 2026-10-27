// Package slot makes htd processes take turns with the local model: replays,
// skill checks and ask_local calls share one lock file, so the model never
// gets two htd requests at once and the machine isn't overloaded.
package slot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/llm"
)

// ErrBusy means another htd process held the model for the whole wait.
var ErrBusy = errors.New("the local model is busy with another htd job")

// Acquire waits up to wait for the model lock in dir and returns a release
// function. A zero wait means wait as long as it takes. onWait, if set, is
// called once when the lock is busy, so the user knows why nothing happens.
// Cancelling ctx (e.g. Ctrl-C) stops the wait.
//
// Interactive callers (a wait limit, like ask_local) jump the queue: while
// one waits it keeps a "want" file fresh, and batch callers (no limit, like
// bench and replay) step aside between turns until it has had its turn.
func Acquire(ctx context.Context, dir string, wait time.Duration, onWait func()) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	want := filepath.Join(dir, "model.want")
	interactive := wait > 0
	if interactive {
		defer os.Remove(want)
	} else if err := yield(ctx, want); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "model.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		if interactive {
			_ = os.WriteFile(want, nil, 0o600) // refreshes the time stamp
		}
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			return func() { unlock(f); f.Close() }, nil
		}
		if wait > 0 && time.Now().After(deadline) {
			f.Close()
			return nil, ErrBusy
		}
		if onWait != nil {
			onWait()
			onWait = nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// wantFresh is how long a waiting interactive caller's signal counts.
const wantFresh = 3 * time.Second

// yield makes a batch caller wait while an interactive caller is waiting.
func yield(ctx context.Context, want string) error {
	for {
		st, err := os.Stat(want)
		if err != nil || time.Since(st.ModTime()) > wantFresh {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Locked sends each chat turn through the model lock, so turns from
// different htd processes take turns instead of piling onto the model.
type Locked struct {
	Inner  llm.Chatter
	Dir    string
	Wait   time.Duration  // 0 = wait as long as it takes
	Waited *time.Duration // if set, time spent waiting for the lock is added here
}

// Chat waits for the lock, then runs one turn.
func (l Locked) Chat(ctx context.Context, msgs []llm.Message, tools []llm.Tool) (llm.Message, llm.Usage, error) {
	start := time.Now()
	release, err := Acquire(ctx, l.Dir, l.Wait, nil)
	if l.Waited != nil {
		*l.Waited += time.Since(start)
	}
	if err != nil {
		return llm.Message{}, llm.Usage{}, err
	}
	defer release()
	return l.Inner.Chat(ctx, msgs, tools)
}
