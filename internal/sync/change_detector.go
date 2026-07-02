package sync

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"

	"tgsc/internal/domain"
	"tgsc/internal/repository"
	"tgsc/internal/scraper"
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
// اگر scraperClient != nil باشد، برای محصولات جدید جزئیات کامل دریافت می‌شود.
func (cd *ChangeDetector) ProcessScrapedProducts(
	ctx context.Context,
	scraped []*domain.Product,
	scraperClient *scraper.Client, // می‌تواند nil باشد
) error {
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
			// ============================================================
			// NEW product
			// ============================================================
			normalized.ID = uuid.New().String()
			now := time.Now()
			normalized.CreatedAt = now
			normalized.UpdatedAt = now

			// 🔥 دریافت جزئیات کامل محصول از ایویز (اگر scraperClient موجود باشد)
			if scraperClient != nil {
				detail, err := scraperClient.GetProductDetailFromPage(ctx, normalized.SourceID, normalized.EwaysCatID)
				if err != nil {
					log.Printf("⚠️ خطا در دریافت جزئیات محصول %s: %v", normalized.SourceID, err)
				} else {
					// توضیحات کامل
					if detail.Description != "" {
						normalized.FullDescription = detail.Description
					}
					// گالری تصاویر
					if len(detail.Images) > 0 {
						normalized.GalleryImages = detail.Images
					}
					// ویژگی‌ها (به JSON تبدیل می‌شوند)
					if len(detail.Attributes) > 0 {
						attrBytes, marshalErr := json.Marshal(detail.Attributes)
						if marshalErr == nil {
							normalized.Attributes = string(attrBytes)
						} else {
							log.Printf("⚠️ خطا در تبدیل ویژگی‌های محصول %s به JSON: %v", normalized.SourceID, marshalErr)
						}
					}
				}
			}

			// ذخیره در دیتابیس
			created, err := cd.productRepo.UpsertProduct(ctx, normalized)
			if err != nil {
				return err
			}
			if !created {
				continue
			}

			// ایجاد job از نوع "create"
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

		// ============================================================
		// EXISTING product: compare fingerprint
		// ============================================================
		normalized.ID = existing.ID
		normalized.CreatedAt = existing.CreatedAt
		normalized.UpdatedAt = time.Now()

		// برای محصولات موجود، اگر قبلاً جزئیات ذخیره نشده بود، می‌توانیم آن‌ها را دریافت کنیم
		// (اختیاری - فقط در صورت نیاز و اگر scraperClient موجود باشد)
		if scraperClient != nil && existing.FullDescription == "" && existing.Attributes == "" {
			detail, err := scraperClient.GetProductDetailFromPage(ctx, normalized.SourceID, normalized.EwaysCatID)
			if err != nil {
				log.Printf("⚠️ خطا در دریافت جزئیات محصول موجود %s: %v", normalized.SourceID, err)
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
			}
		} else {
			// اگر قبلاً جزئیات داشت، آنها را از existing کپی کن تا overwrite نشوند
			normalized.FullDescription = existing.FullDescription
			normalized.Attributes = existing.Attributes
			normalized.GalleryImages = existing.GalleryImages
		}

		if normalized.Fingerprint != existing.Fingerprint {
			// ============================================================
			// DIRTY: update product and create UPDATE job
			// ============================================================
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
			// ============================================================
			// UNCHANGED: just update last_scraped_at
			// ============================================================
			if err := cd.productRepo.UpdateLastScraped(ctx, existing.ID, time.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}