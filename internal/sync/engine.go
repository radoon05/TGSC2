package sync

import (
	"context"
	"fmt"
	"sync"
	"time"

	"tgsc/internal/domain"
	"tgsc/internal/logger"
	"tgsc/internal/repository"
	"tgsc/internal/woo"
)

// EngineConfig holds configuration for the sync engine (separate for update and create)
type EngineConfig struct {
	// Update workers
	UpdateWorkerCount int
	UpdateFetchLimit  int
	UpdateBatchSize   int

	// Create workers
	CreateWorkerCount int
	CreateFetchLimit  int
	CreateBatchSize   int

	// Shared
	RetryBackoffBase time.Duration
	MaxRetries       int
}

// Engine is the core sync engine with separate pools for update and create jobs.
type Engine struct {
	syncJobRepo repository.SyncJobRepository
	productRepo repository.ProductRepository
	wooClient   *woo.Client
	logger      *logger.Logger
	cfg         *EngineConfig

	updatePool *WorkerPool
	createPool *WorkerPool

	runMutex sync.Mutex
}

// NewEngine creates a new sync engine.
func NewEngine(
	syncJobRepo repository.SyncJobRepository,
	productRepo repository.ProductRepository,
	wooClient *woo.Client,
	log *logger.Logger,
	cfg *EngineConfig,
) *Engine {
	return &Engine{
		syncJobRepo: syncJobRepo,
		productRepo: productRepo,
		wooClient:   wooClient,
		logger:      log,
		cfg:         cfg,
	}
}

// Start initializes and starts the worker pools.
func (e *Engine) Start(ctx context.Context) {
	e.updatePool = NewWorkerPool(e.cfg.UpdateWorkerCount, e.logger.Named("update-pool"))
	e.createPool = NewWorkerPool(e.cfg.CreateWorkerCount, e.logger.Named("create-pool"))

	e.updatePool.Start()
	e.createPool.Start()

	// Start the main loop that fetches jobs and submits to pools
	go e.runLoop(ctx)
}

// Stop gracefully stops both worker pools.
func (e *Engine) Stop() {
	if e.updatePool != nil {
		e.updatePool.Stop()
	}
	if e.createPool != nil {
		e.createPool.Stop()
	}
}

// runLoop continuously fetches pending jobs and submits them to appropriate pools.
func (e *Engine) runLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // check every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("engine run loop stopped")
			return
		case <-ticker.C:
			e.processJobs(ctx)
		}
	}
}

// processJobs fetches pending jobs and submits to pools.
func (e *Engine) processJobs(ctx context.Context) {
	e.runMutex.Lock()
	defer e.runMutex.Unlock()

	// 1. Fetch create jobs
	createJobs, err := e.syncJobRepo.FetchPendingJobsByType(ctx, "create", e.cfg.CreateFetchLimit)
	if err != nil {
		e.logger.Error("failed to fetch create jobs", "error", err)
	} else if len(createJobs) > 0 {
		e.logger.Info("fetched create jobs", "count", len(createJobs))
		e.submitBatches(ctx, createJobs, e.cfg.CreateBatchSize, e.createPool)
	}

	// 2. Fetch update jobs
	updateJobs, err := e.syncJobRepo.FetchPendingJobsByType(ctx, "update", e.cfg.UpdateFetchLimit)
	if err != nil {
		e.logger.Error("failed to fetch update jobs", "error", err)
	} else if len(updateJobs) > 0 {
		e.logger.Info("fetched update jobs", "count", len(updateJobs))
		e.submitBatches(ctx, updateJobs, e.cfg.UpdateBatchSize, e.updatePool)
	}
}

// submitBatches splits jobs into batches and submits to the given worker pool.
func (e *Engine) submitBatches(ctx context.Context, jobs []*domain.SyncJob, batchSize int, pool *WorkerPool) {
	batches := e.buildBatches(jobs, batchSize)
	for _, b := range batches {
		// Create a task from the batch
		task := e.createBatchTask(ctx, b)
		if !pool.Submit(task) {
			e.logger.Warn("failed to submit batch to pool (pool stopped?)")
		}
	}
}

// createBatchTask returns a Task function that processes a batch.
func (e *Engine) createBatchTask(ctx context.Context, b *batch) Task {
	return func(taskCtx context.Context) error {
		return e.processBatch(ctx, b)
	}
}

// buildBatches splits jobs into batches of max size.
func (e *Engine) buildBatches(jobs []*domain.SyncJob, batchSize int) []*batch {
	if len(jobs) == 0 {
		return nil
	}
	batches := make([]*batch, 0, (len(jobs)+batchSize-1)/batchSize)
	for i := 0; i < len(jobs); i += batchSize {
		end := i + batchSize
		if end > len(jobs) {
			end = len(jobs)
		}
		batches = append(batches, &batch{jobs: jobs[i:end]})
	}
	return batches
}

// processBatch handles one batch of jobs (all same type: create or update).
func (e *Engine) processBatch(ctx context.Context, b *batch) error {
	if len(b.jobs) == 0 {
		return nil
	}
	jobType := b.jobs[0].JobType

	// Step 1: load products and build sourceID -> job mapping
	sourceIDToJob := make(map[string]*domain.SyncJob, len(b.jobs))
	products := make([]*domain.Product, 0, len(b.jobs))
	for _, job := range b.jobs {
		prod, err := e.productRepo.FindByID(ctx, job.ProductID)
		if err != nil {
			e.logger.Error("failed to load product for job", "job_id", job.ID, "error", err)
			e.handleJobFailure(ctx, job, err)
			continue
		}
		if prod == nil {
			err := fmt.Errorf("product not found: %s", job.ProductID)
			e.logger.Error("product not found for job", "job_id", job.ID, "product_id", job.ProductID)
			e.handleJobFailure(ctx, job, err)
			continue
		}
		sourceIDToJob[prod.SourceID] = job
		products = append(products, prod)
	}
	if len(products) == 0 {
		return nil
	}

	// Step 2: call WooCommerce batch API
	var result *woo.BatchResult
	var apiErr error
	if jobType == "create" {
		result, apiErr = e.wooClient.BatchCreateProducts(ctx, products)
	} else {
		result, apiErr = e.wooClient.BatchUpdateProducts(ctx, products)
	}
	if apiErr != nil {
		e.logger.Error("Woo batch API call failed", "job_type", jobType, "error", apiErr)
		for _, job := range b.jobs {
			e.handleJobFailure(ctx, job, apiErr)
		}
		return apiErr
	}

	// Step 3: process partial success/failure
	for sourceID, job := range sourceIDToJob {
		if result.SuccessSet[sourceID] {
			if err := e.syncJobRepo.UpdateJobStatus(ctx, job.ID, domain.StateSuccess, "", job.RetryCount); err != nil {
				e.logger.Error("failed to update job status to SUCCESS", "job_id", job.ID, "error", err)
			} else {
				e.logger.Debug("job succeeded", "job_id", job.ID, "type", job.JobType)
			}
		} else {
			errMsg := result.FailedIDs[sourceID]
			if errMsg == "" {
				errMsg = "unknown error in batch"
			}
			e.handleJobFailure(ctx, job, fmt.Errorf(errMsg))
		}
	}
	return nil
}

func (e *Engine) handleJobFailure(ctx context.Context, job *domain.SyncJob, err error) {
	newRetryCount := job.RetryCount + 1
	errMsg := err.Error()

	if newRetryCount >= e.cfg.MaxRetries {
		if markErr := e.syncJobRepo.MarkAsDeadLetter(ctx, job.ID, errMsg); markErr != nil {
			e.logger.Error("failed to mark job as dead letter", "job_id", job.ID, "error", markErr)
		} else {
			e.logger.Warn("job moved to dead letter", "job_id", job.ID, "retries", job.RetryCount, "error", errMsg)
		}
		return
	}

	nextDelay := job.NextRetryDelay(e.cfg.RetryBackoffBase)
	nextScheduledAt := time.Now().Add(nextDelay)
	if err := e.syncJobRepo.ScheduleRetry(ctx, job.ID, nextScheduledAt, errMsg); err != nil {
		e.logger.Error("failed to schedule retry", "job_id", job.ID, "error", err)
	} else {
		e.logger.Info("job scheduled for retry", "job_id", job.ID, "retry_count", newRetryCount, "delay", nextDelay)
	}
}

// batch is a group of jobs to be processed together.
type batch struct {
	jobs []*domain.SyncJob
}