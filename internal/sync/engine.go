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

// ============================================================
//  EngineConfig
// ============================================================

type EngineConfig struct {
	UpdateWorkerCount int
	UpdateFetchLimit  int
	UpdateBatchSize   int
	CreateWorkerCount int
	CreateFetchLimit  int
	CreateBatchSize   int
	RetryBackoffBase  time.Duration
	MaxRetries        int
	DryRun            bool
}

// ============================================================
//  Engine
// ============================================================

type Engine struct {
	syncJobRepo repository.SyncJobRepository
	productRepo repository.ProductRepository
	wooClient   *woo.Client
	logger      *logger.Logger
	cfg         *EngineConfig

	updatePool *WorkerPool
	createPool *WorkerPool

	runMutex  sync.Mutex
	stopChan  chan struct{}
	running   bool
	ctx       context.Context
	cancel    context.CancelFunc
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
		stopChan:    make(chan struct{}),
	}
}

// ============================================================
//  Start / Stop
// ============================================================

func (e *Engine) Start(ctx context.Context) {
	e.runMutex.Lock()
	defer e.runMutex.Unlock()

	if e.running {
		return
	}
	e.running = true
	e.ctx, e.cancel = context.WithCancel(ctx)

	if e.cfg.DryRun {
		e.logger.Info("engine running in DRY-RUN mode - no jobs will be processed")
		return
	}

	e.updatePool = NewWorkerPool(e.cfg.UpdateWorkerCount, e.logger.Named("update-pool"))
	e.createPool = NewWorkerPool(e.cfg.CreateWorkerCount, e.logger.Named("create-pool"))

	e.updatePool.Start()
	e.createPool.Start()

	e.logger.Info("engine started", "update_workers", e.cfg.UpdateWorkerCount, "create_workers", e.cfg.CreateWorkerCount)

	go func() {
		time.Sleep(5 * time.Second)
		e.processJobs(e.ctx)
	}()

	go e.runLoop(e.ctx)
	go e.recoverStaleLoop(e.ctx)
}

func (e *Engine) Stop() {
	e.runMutex.Lock()
	defer e.runMutex.Unlock()

	if !e.running {
		return
	}
	e.running = false

	if e.cancel != nil {
		e.cancel()
	}

	select {
	case <-e.stopChan:
	default:
		close(e.stopChan)
	}

	if e.updatePool != nil {
		e.updatePool.Stop()
	}
	if e.createPool != nil {
		e.createPool.Stop()
	}
	e.logger.Info("engine stopped")
}

// ============================================================
//  runLoop
// ============================================================

func (e *Engine) runLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.logger.Debug("engine run loop stopped")
			return
		case <-e.stopChan:
			return
		case <-ticker.C:
			e.processJobs(ctx)
		}
	}
}

// ============================================================
//  processJobs
// ============================================================

func (e *Engine) processJobs(ctx context.Context) {
	e.runMutex.Lock()
	defer e.runMutex.Unlock()

	if e.cfg.DryRun {
		return
	}

	createJobs, err := e.syncJobRepo.ClaimPendingJobs(ctx, "create", e.cfg.CreateFetchLimit)
	if err != nil {
		e.logger.Error("failed to claim create jobs", "error", err)
	} else if len(createJobs) > 0 {
		e.logger.Info("claimed create jobs", "count", len(createJobs))
		e.submitBatches(ctx, createJobs, e.cfg.CreateBatchSize, e.createPool)
	}

	updateJobs, err := e.syncJobRepo.ClaimPendingJobs(ctx, "update", e.cfg.UpdateFetchLimit)
	if err != nil {
		e.logger.Error("failed to claim update jobs", "error", err)
	} else if len(updateJobs) > 0 {
		e.logger.Info("claimed update jobs", "count", len(updateJobs))
		e.submitBatches(ctx, updateJobs, e.cfg.UpdateBatchSize, e.updatePool)
	}
}

// ============================================================
//  submitBatches
// ============================================================

func (e *Engine) submitBatches(ctx context.Context, jobs []*domain.SyncJob, batchSize int, pool *WorkerPool) {
	batches := e.buildBatches(jobs, batchSize)
	for _, b := range batches {
		task := e.createBatchTask(ctx, b)
		if !pool.Submit(task) {
			e.logger.Warn("failed to submit batch to pool (pool stopped?)")
		}
	}
}

// ============================================================
//  buildBatches
// ============================================================

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

// ============================================================
//  createBatchTask
// ============================================================

func (e *Engine) createBatchTask(ctx context.Context, b *batch) Task {
	return func(taskCtx context.Context) error {
		return e.processBatch(taskCtx, b)
	}
}

// ============================================================
//  processBatch
// ============================================================

func (e *Engine) processBatch(ctx context.Context, b *batch) error {
	if len(b.jobs) == 0 {
		return nil
	}
	jobType := b.jobs[0].JobType

	sourceIDToJob := make(map[string]*domain.SyncJob)
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

	var result *woo.BatchResult
	var apiErr error

	if jobType == "create" {
		result, apiErr = e.wooClient.BatchCreateProducts(ctx, products)
	} else {
		result, apiErr = e.wooClient.BatchUpdateProducts(ctx, products)
	}

	if apiErr != nil {
		e.logger.Error("Woo batch API call failed", "job_type", jobType, "error", apiErr)
		for _, job := range sourceIDToJob {
			e.handleJobFailure(ctx, job, apiErr)
		}
		return apiErr
	}

	// 🔥 اصلاح: استفاده از _ به‌جای idx
	for _, prod := range products {
		job := sourceIDToJob[prod.SourceID]
		if job == nil {
			continue
		}

		var success bool
		var errMsg string

		if jobType == "create" {
			if wooID, ok := result.SKUToWooID[prod.SourceID]; ok && wooID > 0 {
				success = true
				if updateErr := e.productRepo.UpdateWooID(ctx, prod.ID, wooID); updateErr != nil {
					e.logger.Error("failed to update woo_id", "product_id", prod.ID, "woo_id", wooID, "error", updateErr)
				}
			} else {
				success = false
				errMsg = result.FailedIDs[prod.SourceID]
				if errMsg == "" {
					errMsg = "unknown error in create batch"
				}
			}
		} else {
			if msg, ok := result.FailedIDs[prod.SourceID]; ok {
				success = false
				errMsg = msg
			} else {
				success = true
			}
		}

		if success {
			if err := e.syncJobRepo.UpdateJobStatus(ctx, job.ID, domain.StateSuccess, "", job.RetryCount); err != nil {
				e.logger.Error("failed to update job status to SUCCESS", "job_id", job.ID, "error", err)
			} else {
				e.logger.Debug("job succeeded", "job_id", job.ID, "type", job.JobType)
			}
		} else {
			if errMsg == "" {
				errMsg = "unknown error"
			}
			e.handleJobFailure(ctx, job, fmt.Errorf(errMsg))
		}
	}

	return nil
}

// ============================================================
//  handleJobFailure
// ============================================================

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

// ============================================================
//  recoverStaleLoop
// ============================================================

func (e *Engine) recoverStaleLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopChan:
			return
		case <-ticker.C:
			e.recoverStale(ctx)
		}
	}
}

func (e *Engine) recoverStale(ctx context.Context) {
	count, err := e.syncJobRepo.RecoverStaleJobs(ctx)
	if err != nil {
		e.logger.Error("failed to recover stale jobs", "error", err)
		return
	}
	if count > 0 {
		e.logger.Warn("recovered stale jobs", "count", count)
	}
}

// ============================================================
//  batch
// ============================================================

type batch struct {
	jobs []*domain.SyncJob
}