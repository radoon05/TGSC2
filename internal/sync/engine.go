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

// EngineConfig holds configuration for the sync engine.
type EngineConfig struct {
	WorkerCount      int           // number of concurrent workers
	FetchLimit       int           // max jobs to fetch per RunOnce
	RetryBackoffBase time.Duration // base delay for retry (exponential)
	MaxRetries       int           // max retries before dead letter
	BatchCreateSize  int           // max products per create batch
	BatchUpdateSize  int           // max products per update batch
}

// Engine is the core sync engine that processes pending jobs.
type Engine struct {
	syncJobRepo repository.SyncJobRepository
	productRepo repository.ProductRepository
	wooClient   *woo.Client
	logger      *logger.Logger
	cfg         *EngineConfig

	runMutex sync.Mutex // prevents concurrent RunOnce
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

// RunOnce fetches pending jobs, builds batches, and processes them with a worker pool.
// It is safe to call concurrently (only one execution at a time).
func (e *Engine) RunOnce(ctx context.Context) error {
	// Ensure only one RunOnce runs at a time
	e.runMutex.Lock()
	defer e.runMutex.Unlock()

	// 1. Fetch pending jobs
	jobs, err := e.syncJobRepo.FetchPendingJobs(ctx, e.cfg.FetchLimit)
	if err != nil {
		return fmt.Errorf("fetch pending jobs: %w", err)
	}
	if len(jobs) == 0 {
		e.logger.Debug("RunOnce: no pending jobs")
		return nil
	}
	e.logger.Info("RunOnce: fetched pending jobs", "count", len(jobs))

	// 2. Split jobs by type
	createJobs := make([]*domain.SyncJob, 0, len(jobs))
	updateJobs := make([]*domain.SyncJob, 0, len(jobs))
	for _, job := range jobs {
		if job.JobType == "create" {
			createJobs = append(createJobs, job)
		} else {
			updateJobs = append(updateJobs, job)
		}
	}

	// 3. Build batches
	createBatches := e.buildBatches(createJobs, e.cfg.BatchCreateSize)
	updateBatches := e.buildBatches(updateJobs, e.cfg.BatchUpdateSize)
	totalBatches := len(createBatches) + len(updateBatches)
	if totalBatches == 0 {
		return nil
	}

	// 4. Create job channel and start workers
	batchChan := make(chan *batch, totalBatches)
	var wg sync.WaitGroup
	for i := 0; i < e.cfg.WorkerCount; i++ {
		wg.Add(1)
		go e.worker(ctx, &wg, batchChan)
	}

	// 5. Send batches to workers
	for _, b := range createBatches {
		select {
		case <-ctx.Done():
			close(batchChan)
			wg.Wait()
			return ctx.Err()
		case batchChan <- b:
		}
	}
	for _, b := range updateBatches {
		select {
		case <-ctx.Done():
			close(batchChan)
			wg.Wait()
			return ctx.Err()
		case batchChan <- b:
		}
	}
	close(batchChan)

	// 6. Wait for all workers to finish
	wg.Wait()
	return nil
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

// worker processes batches from the channel.
func (e *Engine) worker(ctx context.Context, wg *sync.WaitGroup, batchChan <-chan *batch) {
	defer wg.Done()
	for b := range batchChan {
		select {
		case <-ctx.Done():
			return
		default:
		}
		e.processBatch(ctx, b)
	}
}

// processBatch handles one batch of jobs (all same type: create or update).
func (e *Engine) processBatch(ctx context.Context, b *batch) {
	if len(b.jobs) == 0 {
		return
	}
	jobType := b.jobs[0].JobType // "create" or "update"

	// Step 1: load products and build sourceID -> job mapping
	sourceIDToJob := make(map[string]*domain.SyncJob, len(b.jobs))
	products := make([]*domain.Product, 0, len(b.jobs))
	var loadErr error
	for _, job := range b.jobs {
		prod, err := e.productRepo.FindByID(ctx, job.ProductID)
		if err != nil {
			e.logger.Error("failed to load product for job", "job_id", job.ID, "error", err)
			e.handleJobFailure(ctx, job, err)
			loadErr = err
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
		if loadErr != nil {
			e.logger.Error("batch failed: no valid products loaded")
		}
		return
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
		// Complete batch failure (network, auth, etc.)
		e.logger.Error("Woo batch API call failed", "job_type", jobType, "error", apiErr)
		for _, job := range b.jobs {
			e.handleJobFailure(ctx, job, apiErr)
		}
		return
	}

	// Step 3: process partial success/failure
	for sourceID, job := range sourceIDToJob {
		if result.SuccessSet[sourceID] {
			// Success
			if err := e.syncJobRepo.UpdateJobStatus(ctx, job.ID, domain.StateSuccess, "", job.RetryCount); err != nil {
				e.logger.Error("failed to update job status to SUCCESS", "job_id", job.ID, "error", err)
			} else {
				e.logger.Debug("job succeeded", "job_id", job.ID, "type", job.JobType)
			}
		} else {
			// Failure
			errMsg := result.FailedIDs[sourceID]
			if errMsg == "" {
				errMsg = "unknown error in batch"
			}
			e.handleJobFailure(ctx, job, fmt.Errorf(errMsg))
		}
	}
}

// handleJobFailure decides whether to retry or move to dead letter.
func (e *Engine) handleJobFailure(ctx context.Context, job *domain.SyncJob, err error) {
	newRetryCount := job.RetryCount + 1
	errMsg := err.Error()

	if newRetryCount >= e.cfg.MaxRetries {
		// Move to dead letter
		if e := e.syncJobRepo.MarkAsDeadLetter(ctx, job.ID, errMsg); e != nil {
			e.logger.Error("failed to mark job as dead letter", "job_id", job.ID, "error", e)
		} else {
			e.logger.Warn("job moved to dead letter", "job_id", job.ID, "retries", job.RetryCount, "error", errMsg)
		}
		return
	}

	// Schedule retry with exponential backoff
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