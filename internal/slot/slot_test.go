package slot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HTMLGuyLLC/HometownDiscount/internal/llm"
)

func TestAcquire(t *testing.T) {
	dir := t.TempDir()
	release, err := Acquire(context.Background(), dir, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(context.Background(), dir, 300*time.Millisecond, nil); !errors.Is(err, ErrBusy) {
		t.Errorf("second holder got %v, want ErrBusy", err)
	}
	release()
	again, err := Acquire(context.Background(), dir, time.Second, nil)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again()
}

func TestAcquireCancel(t *testing.T) {
	dir := t.TempDir()
	release, _ := Acquire(context.Background(), dir, 0, nil)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	waited := false
	if _, err := Acquire(ctx, dir, 0, func() { waited = true }); !errors.Is(err, context.DeadlineExceeded) || !waited {
		t.Errorf("err = %v, waited = %v", err, waited)
	}
}

// A batch caller that wants the lock right after releasing it must let a
// waiting interactive caller go first.
func TestInteractiveGoesFirst(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	release, _ := Acquire(ctx, dir, 0, nil) // batch holds the model
	got := make(chan bool)
	go func() {
		r, err := Acquire(ctx, dir, 5*time.Second, nil) // interactive waits
		if err == nil {
			time.Sleep(100 * time.Millisecond)
			r()
		}
		got <- err == nil
	}()
	time.Sleep(400 * time.Millisecond)
	release()
	// The batch immediately wants the model back; it must yield.
	start := time.Now()
	again, err := Acquire(ctx, dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	again()
	if !<-got {
		t.Fatal("the interactive caller never got the model")
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Error("the batch caller didn't step aside")
	}
}

type nop struct{}

func (nop) Chat(context.Context, []llm.Message, []llm.Tool) (llm.Message, llm.Usage, error) {
	return llm.Message{Content: "ok"}, llm.Usage{}, nil
}

// Time spent waiting for another process's turn is reported, so a replay
// can leave it out of its own time.
func TestLockedCountsWait(t *testing.T) {
	dir := t.TempDir()
	release, _ := Acquire(context.Background(), dir, 0, nil)
	go func() { time.Sleep(300 * time.Millisecond); release() }()
	var waited time.Duration
	if _, _, err := (Locked{Inner: nop{}, Dir: dir, Waited: &waited}).Chat(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if waited < 250*time.Millisecond {
		t.Errorf("waited = %v, want about 300ms", waited)
	}
}
