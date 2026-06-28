package sync

import (
	"context"
	"time"

	"github.com/google/uuid"

	"tgsc/internal/domain"
	"tgsc/internal/repository"
)

// ChangeDetector compares scraped products with existing ones and creates sync jobs for changes.
type ChangeDetector struct {
	productRepo repository.ProductRepository
	syncJobRepo repository.SyncJobRepository
	normalizer  *Normalizer
}

// NewChangeDetector creates a new change detector.
func NewChangeDetector(
	productRepo repository.ProductRepository,
	syncJobRepo repository.SyncJobRepository,
	normalizer *Normalizer,
) *ChangeDetector {
	return &ChangeDetector{
		productRepo: productRepo,
		syncJobRepo: syncJobRepo,
		normalizer:  normalizer,
	}
}

// ProcessScrapedProducts compares each scraped product and creates/updates sync jobs.
func (cd *ChangeDetector) ProcessScrapedProducts(ctx context.Context, scraped []*domain.Product) error {
	for _, scrapedProd := range scraped {
		// 1. Normalize and generate fingerprint
		normalized := cd.normalizer.Normalize(scrapedProd)
		fingerprint := cd.normalizer.GenerateFingerprint(normalized)
		normalized.Fingerprint = fingerprint
		normalized.LastScrapedAt = time.Now()

		// 2. Find existing product by source_id
		existing, err := cd.productRepo.FindBySourceID(ctx, normalized.SourceID)
		if err != nil {
			return err
		}

		if existing == nil {
			// NEW product
			normalized.ID = uuid.New().String()
			now := time.Now()
			normalized.CreatedAt = now
			normalized.UpdatedAt = now

			created, err := cd.productRepo.UpsertProduct(ctx, normalized)
			if err != nil {
				return err
			}
			if !created {
				continue
			}
			// Create CREATE job
			job := &domain.SyncJob{
				ID:          uuid.New().String(),
				ProductID:   normalized.ID,
				JobType:     "create",
				State:       domain.StatePending,
				RetryCount:  0,
				ScheduledAt: time.Now(),
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := cd.syncJobRepo.CreateJob(ctx, job); err != nil {
				return err
			}
			continue
		}

		// EXISTING product: compare fingerprint
		normalized.ID = existing.ID
		normalized.CreatedAt = existing.CreatedAt
		normalized.UpdatedAt = time.Now()

		if normalized.Fingerprint != existing.Fingerprint {
			// DIRTY: update product and create UPDATE job
			if _, err := cd.productRepo.UpsertProduct(ctx, normalized); err != nil {
				return err
			}
			job := &domain.SyncJob{
				ID:          uuid.New().String(),
				ProductID:   normalized.ID,
				JobType:     "update",
				State:       domain.StatePending,
				RetryCount:  0,
				ScheduledAt: time.Now(),
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			if err := cd.syncJobRepo.CreateJob(ctx, job); err != nil {
				return err
			}
		} else {
			// UNCHANGED: just update last_scraped_at
			if err := cd.productRepo.UpdateLastScraped(ctx, existing.ID, time.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}