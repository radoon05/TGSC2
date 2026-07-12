package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tgsc/internal/config"
	"tgsc/internal/domain"
	"tgsc/internal/repository"
	"tgsc/internal/scraper"
)

// ============================================================
//  ChangeDetector
// ============================================================

type ChangeDetector struct {
	productRepo repository.ProductRepository
	syncJobRepo repository.SyncJobRepository
	normalizer  *Normalizer
	categories  []config.Category
	dbPool      *pgxpool.Pool
}

func NewChangeDetector(
	productRepo repository.ProductRepository,
	syncJobRepo repository.SyncJobRepository,
	normalizer *Normalizer,
	categories []config.Category,
	dbPool *pgxpool.Pool,
) *ChangeDetector {
	return &ChangeDetector{
		productRepo: productRepo,
		syncJobRepo: syncJobRepo,
		normalizer:  normalizer,
		categories:  categories,
		dbPool:      dbPool,
	}
}

// ============================================================
//  ProcessScrapedProducts
// ============================================================

func (cd *ChangeDetector) ProcessScrapedProducts(
	ctx context.Context,
	scraped []*domain.Product,
	scraperClient *scraper.Client,
) error {
	var newCount, updateCount int

	for _, scrapedProd := range scraped {
		// ۱. نرمال‌سازی
		normalized := cd.normalizer.Normalize(scrapedProd)
		normalized.Fingerprint = cd.normalizer.GenerateFingerprint(normalized)
		normalized.LastScrapedAt = time.Now()

		// ۲. جستجوی محصول موجود
		existing, err := cd.productRepo.FindBySourceID(ctx, normalized.SourceID)
		if err != nil {
			log.Printf("⚠️ خطا در جستجوی محصول %s: %v", normalized.SourceID, err)
			continue
		}

		isNew := existing == nil || existing.WooID == 0

		// ۳. دریافت جزئیات کامل (فقط یک بار)
		if scraperClient != nil && (isNew || existing.DetailFetchedAt == nil) {
			detail, err := scraperClient.GetProductDetailFromPage(ctx, normalized.SourceID, normalized.EwaysCatID)
			if err != nil {
				log.Printf("⚠️ خطا در دریافت جزئیات محصول %s: %v", normalized.SourceID, err)
			} else {
				if detail.Description != "" {
					normalized.FullDescription = detail.Description
				}
				if len(detail.Images) > 0 {
					normalized.GalleryImages = detail.Images
				}
				if len(detail.Attributes) > 0 {
					attrBytes, _ := json.Marshal(detail.Attributes)
					normalized.Attributes = string(attrBytes)
				}
				now := time.Now()
				normalized.DetailFetchedAt = &now
			}
		}

		// ۴. محصول جدید → Create Job (با تراکنش)
		if isNew {
			normalized.ID = uuid.New().String()
			now := time.Now()
			normalized.CreatedAt = now
			normalized.UpdatedAt = now

			err := cd.withTransaction(ctx, func(tx pgx.Tx) error {
				// مخازن تراکنشی
				txProductRepo := repository.NewProductTxRepo(tx)
				txSyncJobRepo := repository.NewSyncJobTxRepo(tx)

				// Upsert محصول
				created, err := txProductRepo.UpsertProduct(ctx, normalized)
				if err != nil {
					return fmt.Errorf("upsert product: %w", err)
				}
				if !created {
					return nil // محصول از قبل وجود داشت
				}

				// ایجاد job
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
				if err := txSyncJobRepo.CreateJob(ctx, job); err != nil {
					return fmt.Errorf("create job: %w", err)
				}
				return nil
			})
			if err != nil {
				log.Printf("❌ خطا در تراکنش NEW محصول %s: %v", normalized.SourceID, err)
				continue
			}
			newCount++
			continue
		}

		// ۵. محصول موجود → Update Job یا بدون تغییر
		normalized.ID = existing.ID
		normalized.CreatedAt = existing.CreatedAt
		normalized.UpdatedAt = time.Now()
		normalized.WooID = existing.WooID
		if existing.DetailFetchedAt != nil {
			normalized.DetailFetchedAt = existing.DetailFetchedAt
		}

		if normalized.Fingerprint != existing.Fingerprint {
			// تغییر شناسایی شد → Update Job (بدون تراکنش، چون ریسک کمتری دارد)
			if _, err := cd.productRepo.UpsertProduct(ctx, normalized); err != nil {
				log.Printf("❌ خطا در آپدیت محصول %s: %v", normalized.SourceID, err)
				continue
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
				log.Printf("⚠️ خطا در ایجاد update job برای %s: %v", normalized.SourceID, err)
				continue
			}
			updateCount++
		} else {
			// بدون تغییر: فقط last_scraped_at به‌روز شود
			if err := cd.productRepo.UpdateLastScraped(ctx, existing.ID, time.Now()); err != nil {
				log.Printf("⚠️ خطا در به‌روزرسانی last_scraped_at برای %s: %v", normalized.SourceID, err)
			}
		}
	}

	log.Printf("📊 خلاصه تغییرات: %d محصول جدید (create), %d محصول تغییر یافته (update)", newCount, updateCount)
	return nil
}

// ============================================================
//  withTransaction (کمک‌کننده)
// ============================================================

func (cd *ChangeDetector) withTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := cd.dbPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}