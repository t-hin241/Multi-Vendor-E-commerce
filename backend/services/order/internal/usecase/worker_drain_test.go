package usecase

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDrainRunsBatchesWhileTheyComeBackFull(t *testing.T) {
	backlog := 47
	calls := 0
	drain(context.Background(), time.Minute, 20, func() (int, error) {
		calls++
		n := min(backlog, 20)
		backlog -= n
		return n, nil
	}, func(err error) { t.Fatal(err) })
	if backlog != 0 || calls != 3 {
		t.Fatalf("backlog %d left after %d batches, want 0 after 3", backlog, calls)
	}
}

func TestDrainStopsAtTheBudgetAndOnError(t *testing.T) {
	calls := 0
	drain(context.Background(), 20*time.Millisecond, 20, func() (int, error) {
		calls++
		time.Sleep(10 * time.Millisecond)
		return 20, nil // an endless backlog
	}, func(err error) { t.Fatal(err) })
	if calls > 4 {
		t.Fatalf("drain ran %d batches past its 20ms budget", calls)
	}

	var failed error
	calls = 0
	drain(context.Background(), time.Minute, 20, func() (int, error) {
		calls++
		return 0, errors.New("database unavailable")
	}, func(err error) { failed = err })
	if calls != 1 || failed == nil {
		t.Fatalf("an error must stop the drain after one batch and be reported: calls=%d err=%v", calls, failed)
	}
}
