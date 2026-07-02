package sync

import (
	"context"
	"tgsc/internal/logger"
	"tgsc/internal/scraper"
	"time"
)

// Scheduler runs periodic sync jobs based on a ticker.
type Scheduler struct {
	engine   *Engine
	interval time.Duration
	logger   *logger.Logger
	stopChan chan struct{}
	running  bool
}

// ScraperScheduler runs scraper + change detector periodically.
type ScraperScheduler struct {
	scraper        *scraper.Client
	changeDetector *ChangeDetector
	logger         *logger.Logger
	interval       time.Duration
	stopChan       chan struct{}
	running        bool
}

// NewScraperScheduler creates a new scraper scheduler.
func NewScraperScheduler(
	scraper *scraper.Client,
	changeDetector *ChangeDetector,
	log *logger.Logger,
	interval time.Duration,
) *ScraperScheduler {
	return &ScraperScheduler{
		scraper:        scraper,
		changeDetector: changeDetector,
		logger:         log,
		interval:       interval,
		stopChan:       make(chan struct{}),
	}
}

// Start begins the scraper scheduler loop.
func (s *ScraperScheduler) Start(ctx context.Context) {
	if s.running {
		return
	}
	s.running = true
	go s.run(ctx)
}

// Stop gracefully stops the scraper scheduler.
func (s *ScraperScheduler) Stop() {
	if !s.running {
		return
	}
	close(s.stopChan)
	s.running = false
}

func (s *ScraperScheduler) run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.logger.Info("scraper scheduler started", "interval", s.interval)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scraper scheduler stopped due to context cancellation")
			return
		case <-s.stopChan:
			s.logger.Info("scraper scheduler stopped by stop signal")
			return
		case <-ticker.C:
			s.logger.Info("scraper scheduler tick: starting scrape")
			products, err := s.scraper.FetchProducts(ctx)
			if err != nil {
				s.logger.Error("scrape failed", "error", err)
				continue
			}
			s.logger.Info("scrape completed", "product_count", len(products))
			if len(products) == 0 {
				continue
			}
			if err := s.changeDetector.ProcessScrapedProducts(ctx, products, s.scraper); err != nil {
				s.logger.Error("change detection failed", "error", err)
			} else {
				s.logger.Info("change detection completed")
			}
		}
	}
}
