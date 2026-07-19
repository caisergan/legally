package jobs

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSameTokenWorkNeverOverlaps(t *testing.T) {
	queue := NewTokenQueue()
	defer queue.Close()

	var active int32
	var maximum int32
	start := make(chan struct{})
	results := make([]<-chan Result, 4)

	for index := range results {
		results[index] = queue.Submit(context.Background(), "token-a", func(context.Context) error {
			<-start
			current := atomic.AddInt32(&active, 1)
			for {
				observed := atomic.LoadInt32(&maximum)
				if current <= observed || atomic.CompareAndSwapInt32(&maximum, observed, current) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			return nil
		})
	}
	close(start)

	for _, result := range results {
		if err := (<-result).Err; err != nil {
			t.Fatalf("queued work failed: %v", err)
		}
	}
	if maximum != 1 {
		t.Fatalf("same-token work overlapped: maximum concurrency was %d", maximum)
	}
}

func TestFullTokenQueueDoesNotBlockOtherTokens(t *testing.T) {
	queue := NewTokenQueueWithLimit(2)
	defer queue.Close()

	releaseFirst := make(chan struct{})
	firstStarted := make(chan struct{})
	first := queue.Submit(context.Background(), "token-a", func(context.Context) error {
		close(firstStarted)
		<-releaseFirst
		return nil
	})
	<-firstStarted

	queued := make([]<-chan Result, 32)
	for index := range queued {
		queued[index] = queue.Submit(context.Background(), "token-a", func(context.Context) error { return nil })
	}

	blockedContext, cancelBlocked := context.WithCancel(context.Background())
	blockedSubmit := make(chan (<-chan Result), 1)
	go func() {
		blockedSubmit <- queue.Submit(blockedContext, "token-a", func(context.Context) error { return nil })
	}()
	for range 10 {
		runtime.Gosched()
	}

	otherStarted := make(chan struct{})
	other := queue.Submit(context.Background(), "token-b", func(context.Context) error {
		close(otherStarted)
		return nil
	})
	select {
	case <-otherStarted:
	case <-time.After(time.Second):
		cancelBlocked()
		close(releaseFirst)
		t.Fatal("a full token-a queue blocked token-b submission")
	}
	if err := (<-other).Err; err != nil {
		t.Fatalf("other-token work failed: %v", err)
	}

	cancelBlocked()
	blockedResult := <-blockedSubmit
	if err := (<-blockedResult).Err; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked submit error = %v, want context cancellation", err)
	}

	close(releaseFirst)
	if err := (<-first).Err; err != nil {
		t.Fatalf("first work failed: %v", err)
	}
	for _, result := range queued {
		if err := (<-result).Err; err != nil {
			t.Fatalf("queued work failed: %v", err)
		}
	}
}

func TestTokenWorkerCountIsBounded(t *testing.T) {
	queue := NewTokenQueueWithLimit(1)
	defer queue.Close()

	first := queue.Submit(context.Background(), "token-a", func(context.Context) error { return nil })
	if err := (<-first).Err; err != nil {
		t.Fatalf("first token failed: %v", err)
	}
	second := queue.Submit(context.Background(), "token-b", func(context.Context) error { return nil })
	if err := (<-second).Err; !errors.Is(err, ErrWorkerLimit) {
		t.Fatalf("second token error = %v, want ErrWorkerLimit", err)
	}
}

func TestDifferentTokensCanRunInParallel(t *testing.T) {
	queue := NewTokenQueue()
	defer queue.Close()

	release := make(chan struct{})
	started := make(chan string, 2)
	var once sync.Once
	work := func(token string) Work {
		return func(context.Context) error {
			started <- token
			<-release
			return nil
		}
	}

	first := queue.Submit(context.Background(), "token-a", work("token-a"))
	second := queue.Submit(context.Background(), "token-b", work("token-b"))

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case token := <-started:
			seen[token] = true
		case <-time.After(time.Second):
			once.Do(func() { close(release) })
			t.Fatal("different-token work did not start in parallel")
		}
	}
	once.Do(func() { close(release) })

	if err := (<-first).Err; err != nil {
		t.Fatalf("first work failed: %v", err)
	}
	if err := (<-second).Err; err != nil {
		t.Fatalf("second work failed: %v", err)
	}
}
