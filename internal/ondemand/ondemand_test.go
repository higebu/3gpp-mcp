package ondemand

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoRunsOnce(t *testing.T) {
	var g Group
	var runs atomic.Int32
	release := make(chan struct{})
	fn := func(context.Context) error {
		runs.Add(1)
		<-release
		return nil
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = g.Do(context.Background(), "k", time.Second, nil, fn)
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if runs.Load() != 1 {
		t.Errorf("fn ran %d times, want 1", runs.Load())
	}
}

func TestDoBudgetExceeded(t *testing.T) {
	var g Group
	release := make(chan struct{})
	fn := func(context.Context) error { <-release; return nil }
	err := g.Do(context.Background(), "k", 20*time.Millisecond, nil, fn)
	if !errors.Is(err, ErrInProgress) {
		t.Fatalf("err = %v, want ErrInProgress", err)
	}
	close(release)
	// The fetch kept running; joining it now returns its result.
	if err := g.Do(context.Background(), "k", time.Second, nil, fn); err != nil {
		t.Errorf("second call: %v", err)
	}
}

func TestDoSurvivesCallerCancellation(t *testing.T) {
	var g Group
	finished := make(chan struct{})
	fn := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
			close(finished)
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	if err := g.Do(ctx, "k", time.Second, nil, fn); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("fetch was cancelled along with the caller")
	}
}

func TestDoPropagatesErrorAndPanic(t *testing.T) {
	var g Group
	want := errors.New("boom")
	if err := g.Do(context.Background(), "e", time.Second, nil, func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Errorf("err = %v", err)
	}
	err := g.Do(context.Background(), "p", time.Second, nil, func(context.Context) error { panic("bad zip") })
	if err == nil || err.Error() != "fetch of p panicked: bad zip" {
		t.Errorf("err = %v", err)
	}
	// The key is free again afterwards.
	if err := g.Do(context.Background(), "p", time.Second, nil, func(context.Context) error { return nil }); err != nil {
		t.Errorf("after panic: %v", err)
	}
}

func TestDoSkipsWhenAlreadyDone(t *testing.T) {
	var g Group
	ran := false
	err := g.Do(context.Background(), "k", time.Second, func() (bool, error) { return true, nil }, func(context.Context) error { ran = true; return nil })
	if err != nil || ran {
		t.Errorf("err = %v, ran = %v", err, ran)
	}
	checkErr := errors.New("check failed")
	if err := g.Do(context.Background(), "k", time.Second, func() (bool, error) { return false, checkErr }, nil); !errors.Is(err, checkErr) {
		t.Errorf("err = %v", err)
	}
}

func TestDoMaxDuration(t *testing.T) {
	g := Group{MaxDuration: 20 * time.Millisecond}
	err := g.Do(context.Background(), "k", time.Second, nil, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}
