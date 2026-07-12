package sync

import (
	"context"
	"fmt" // 🔥 اضافه شد
	"sync"

	"tgsc/internal/logger"
)

// ... بقیه کد بدون تغییر ...

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
	mu          sync.Mutex
	closed      bool // 🔥 برای idempotent Stop
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
		closed:      false,
	}
}

// Start launches the worker goroutines.
func (p *WorkerPool) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		p.logger.Warn("worker pool already closed, cannot start")
		return
	}
	for i := 0; i < p.workerCount; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
	p.logger.Info("worker pool started", "worker_count", p.workerCount)
}

// Stop gracefully shuts down the worker pool.
// این متد idempotent است (بار دوم اجرا شود panic نمی‌کند)
func (p *WorkerPool) Stop() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		p.logger.Warn("worker pool already stopped")
		return
	}
	p.closed = true
	p.mu.Unlock()

	p.cancel()
	p.wg.Wait()
	close(p.taskChan)
	p.logger.Info("worker pool stopped")
}

// Submit adds a task to the queue. Returns true if accepted, false if pool is stopped.
// 🔥 با select و default از panic روی کانال بسته جلوگیری می‌کند
func (p *WorkerPool) Submit(task Task) bool {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return false
	}
	p.mu.Unlock()

	select {
	case <-p.ctx.Done():
		return false
	case p.taskChan <- task:
		return true
	default:
		// اگر کانال پر باشد، باز هم false برمی‌گرداند (non-blocking)
		return false
	}
}

// SubmitWithContext submits a task but respects the given context as well.
func (p *WorkerPool) SubmitWithContext(ctx context.Context, task Task) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return fmt.Errorf("worker pool is closed")
	}
	p.mu.Unlock()

	select {
	case <-p.ctx.Done():
		return p.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	case p.taskChan <- task:
		return nil
	default:
		return fmt.Errorf("task queue is full")
	}
}

// worker is the goroutine that processes tasks.
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
			p.logger.Debug("worker executing task", "worker_id", id)
			if err := task(p.ctx); err != nil {
				p.logger.Error("task failed", "worker_id", id, "error", err)
			}
		}
	}
}

// PendingTasks returns the number of tasks currently in the queue.
func (p *WorkerPool) PendingTasks() int {
	return len(p.taskChan)
}