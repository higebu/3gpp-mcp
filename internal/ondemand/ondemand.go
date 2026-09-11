// Package ondemand runs slow, deduplicated fetches that outlive the request
// asking for them.
//
// An MCP client times out well before a large document downloads and
// converts, so a fetch runs on a context detached from the caller's, the
// caller waits only for its budget, and a fetch still running when the
// budget expires is reported as ErrInProgress: repeating the same call later
// joins the running fetch and gets the result. Concurrent callers asking for
// the same key share one fetch.
package ondemand

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// ErrInProgress reports that a fetch did not finish within the caller's
// budget. The fetch keeps running in the background, so repeating the same
// call later returns the result.
var ErrInProgress = errors.New("fetch still in progress")

// DefaultBudget is how long a caller waits for a fetch before being told to
// come back.
const DefaultBudget = 60 * time.Second

// DefaultMaxDuration bounds a detached fetch. Large documents take a few
// minutes to download and convert; anything beyond this is a stalled
// transfer.
const DefaultMaxDuration = 30 * time.Minute

// Group deduplicates in-flight fetches by key.
type Group struct {
	// MaxDuration bounds each detached fetch; zero means DefaultMaxDuration.
	MaxDuration time.Duration

	mu       sync.Mutex
	inflight map[string]*call
}

// call tracks one in-progress fetch so concurrent callers share it.
type call struct {
	done chan struct{}
	err  error
}

// Do runs fn under key unless a fetch for key is already in flight, and
// waits up to budget for it. done, when non-nil, is consulted under the
// group's lock before a new fetch starts: a fetch that completed between
// the caller's own check and this call has already left inflight, and
// starting a fresh one would repeat minutes of work.
//
// fn runs on a goroutine with a context detached from ctx (bounded by
// MaxDuration), so a caller that gives up waiting does not throw the work
// away. A panic in fn is recovered and reported as the fetch's error: fn
// parses untrusted third-party binaries off any request, and a panic there
// would otherwise take down the whole server.
func (g *Group) Do(ctx context.Context, key string, budget time.Duration, done func() (bool, error), fn func(ctx context.Context) error) error {
	if budget <= 0 {
		budget = DefaultBudget
	}
	maxDuration := g.MaxDuration
	if maxDuration <= 0 {
		maxDuration = DefaultMaxDuration
	}

	c, running := g.join(key)
	if !running && done != nil {
		// done is a store query that may wait behind a running fetch's
		// insert transaction, so it runs outside the lock: holding the lock
		// meanwhile would stall every caller of every other key. The key is
		// re-checked afterwards, as another caller may have started the
		// fetch in between.
		if ok, err := done(); err != nil || ok {
			return err
		}
		c, running = g.join(key)
	}
	if !running {
		c = g.start(ctx, key, maxDuration, fn)
	}

	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-c.done:
		return c.err
	case <-timer.C:
		return fmt.Errorf("%w: %s", ErrInProgress, key)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// join returns the in-flight call for key, if any.
func (g *Group) join(key string) (*call, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.inflight[key]
	return c, ok
}

// start registers a fetch for key and runs fn detached from ctx, unless a
// fetch for key was registered meanwhile, which is then joined instead.
func (g *Group) start(ctx context.Context, key string, maxDuration time.Duration, fn func(ctx context.Context) error) *call {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.inflight[key]; ok {
		return c
	}
	if g.inflight == nil {
		g.inflight = map[string]*call{}
	}
	c := &call{done: make(chan struct{})}
	g.inflight[key] = c
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), maxDuration)
	go func() {
		defer cancel()
		g.run(fetchCtx, key, c, fn)
	}()
	return c
}

// run performs one fetch and publishes its outcome to everyone waiting.
func (g *Group) run(ctx context.Context, key string, c *call, fn func(ctx context.Context) error) {
	defer func() {
		// Set c.err before close(c.done) publishes the outcome, so waiters
		// never report success for work that did not complete.
		if r := recover(); r != nil {
			c.err = fmt.Errorf("fetch of %s panicked: %v", key, r)
			// The fetch is detached, so the last waiter is often gone by
			// the time a panic fires; log it or it is lost entirely.
			log.Printf("warning: %v", c.err)
		}
		g.mu.Lock()
		delete(g.inflight, key)
		g.mu.Unlock()
		close(c.done)
	}()
	c.err = fn(ctx)
}
