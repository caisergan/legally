package jobs

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrQueueClosed = errors.New("token queue is closed")
	ErrWorkerLimit = errors.New("token worker limit reached")
)

const defaultMaxTokenWorkers = 32

type Result struct {
	Err error
}

type Work func(context.Context) error

type queuedWork struct {
	ctx    context.Context
	work   Work
	result chan Result
}

type tokenWorker struct {
	items chan queuedWork
}

type TokenQueue struct {
	mu         sync.Mutex
	workers    map[string]*tokenWorker
	closed     bool
	maxWorkers int
	submitWG   sync.WaitGroup
	workerWG   sync.WaitGroup
}

func NewTokenQueue() *TokenQueue {
	return NewTokenQueueWithLimit(defaultMaxTokenWorkers)
}

func NewTokenQueueWithLimit(maxWorkers int) *TokenQueue {
	if maxWorkers <= 0 {
		maxWorkers = defaultMaxTokenWorkers
	}
	return &TokenQueue{
		workers:    make(map[string]*tokenWorker),
		maxWorkers: maxWorkers,
	}
}

func (q *TokenQueue) Submit(ctx context.Context, tokenID string, work Work) <-chan Result {
	result := make(chan Result, 1)
	if tokenID == "" {
		result <- Result{Err: errors.New("token ID is required")}
		close(result)
		return result
	}
	if work == nil {
		result <- Result{Err: errors.New("work is required")}
		close(result)
		return result
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		result <- Result{Err: ErrQueueClosed}
		close(result)
		return result
	}
	worker := q.workers[tokenID]
	if worker == nil {
		if len(q.workers) >= q.maxWorkers {
			q.mu.Unlock()
			result <- Result{Err: ErrWorkerLimit}
			close(result)
			return result
		}
		worker = &tokenWorker{items: make(chan queuedWork, 32)}
		q.workers[tokenID] = worker
		q.workerWG.Add(1)
		go q.run(worker)
	}
	q.submitWG.Add(1)
	q.mu.Unlock()

	item := queuedWork{ctx: ctx, work: work, result: result}
	select {
	case worker.items <- item:
	case <-ctx.Done():
		result <- Result{Err: ctx.Err()}
		close(result)
	}
	q.submitWG.Done()
	return result
}

func (q *TokenQueue) Close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	workers := make([]*tokenWorker, 0, len(q.workers))
	for _, worker := range q.workers {
		workers = append(workers, worker)
	}
	q.mu.Unlock()

	q.submitWG.Wait()
	for _, worker := range workers {
		close(worker.items)
	}
	q.workerWG.Wait()
}

func (q *TokenQueue) run(worker *tokenWorker) {
	defer q.workerWG.Done()
	for item := range worker.items {
		err := item.ctx.Err()
		if err == nil {
			err = item.work(item.ctx)
		}
		item.result <- Result{Err: err}
		close(item.result)
	}
}
