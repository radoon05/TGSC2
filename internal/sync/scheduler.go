package sync

import (
	"context"
	"time"

	"tgsc/internal/logger"
)

// Scheduler runs periodic sync jobs based on a ticker.
type Scheduler struct {
	engine   *Engine
	interval time.Duration
	logger   *logger.Logger
	stopChan chan struct{}
	running  bool
}

// NewScheduler creates a new scheduler.
func NewScheduler(engine *Engine, interval time.Duration, log *logger.Logger) *Scheduler {
	return &Scheduler{
		engine:   engine,
		interval: interval,
		logger:   log,
		stopChan: make(chan struct{}),
	}
}

// Start begins the scheduler loop.
func (s *Scheduler) Start(ctx context.Context) {
	if s.running {
		return
	}
	s.running = true
	go s.run(ctx)
}

// Stop gracefully stops the scheduler.
func (s *Scheduler) Stop() {
	if !s.running {
		return
	}
	close(s.stopChan)
	s.running = false
}

func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.logger.Info("scheduler started", "interval", s.interval)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopped due to context cancellation")
			return
		case <-s.stopChan:
			s.logger.Info("scheduler stopped by stop signal")
			return
		case <-ticker.C:
			s.logger.Debug("scheduler tick: triggering sync engine")
			if err := s.engine.RunOnce(ctx); err != nil {
				s.logger.Error("sync engine run failed", "error", err)
			}
		}
	}
}