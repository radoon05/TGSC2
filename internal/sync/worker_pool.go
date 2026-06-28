package sync

import (
	"context"
	"sync"

	"tgsc/internal/logger"
)

// Task is a unit of work that can be executed by the worker pool.
type Task func(ctx context.Context) error

// WorkerPool manages a fixed pool of workers that process generic tasks.
type WorkerPool struct {
	workerCount int
	taskChan    chan Task
	wg          sync.WaitGroup
	logger      *logger.Logger
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewWorkerPool creates a new worker pool with the specified number of workers.
func NewWorkerPool(workerCount int, log *logger.Logger) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool{
		workerCount: workerCount,
		taskChan:    make(chan Task, workerCount*2),
		logger:      log,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start launches the worker goroutines.
func (p *WorkerPool) Start() {
	for i := 0; i < p.workerCount; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
	p.logger.Info("worker pool started", "worker_count", p.workerCount)
}

// Stop gracefully shuts down the worker pool.
func (p *WorkerPool) Stop() {
	p.cancel()
	p.wg.Wait()
	close(p.taskChan)
	p.logger.Info("worker pool stopped")
}

// Submit adds a task to the queue. Returns true if accepted.
func (p *WorkerPool) Submit(task Task) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.taskChan <- task:
		return true
	}
}

func (p *WorkerPool) worker(id int) {
	defer p.wg.Done()
	p.logger.Debug("worker started", "worker_id", id)
	for {
		select {
		case <-p.ctx.Done():
			p.logger.Debug("worker stopping", "worker_id", id)
			return
		case task, ok := <-p.taskChan:
			if !ok {
				return
			}
			if err := task(p.ctx); err != nil {
				p.logger.Error("task failed", "worker_id", id, "error", err)
			}
		}
	}
}

// PendingTasks returns the number of tasks in the queue.
func (p *WorkerPool) PendingTasks() int {
	return len(p.taskChan)
}