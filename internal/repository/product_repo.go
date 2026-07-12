package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tgsc/internal/domain"
)

// ============================================================
//  Interface
// ============================================================

type ProductRepository interface {
	UpsertProduct(ctx context.Context, p *domain.Product) (created bool, err error)
	FindByID(ctx context.Context, id string) (*domain.Product, error)
	FindBySourceID(ctx context.Context, sourceID string) (*domain.Product, error)
	UpdateLastScraped(ctx context.Context, productID string, t time.Time) error
	UpdateWooID(ctx context.Context, productID string, wooID int64) error
	WithTransaction(ctx context.Context, fn func(ProductRepository) error) error
}

// ============================================================
//  productRepo (پیاده‌سازی اصلی با pgxpool.Pool)
// ============================================================

type productRepo struct {
	db *pgxpool.Pool
}

func NewProductRepository(db *pgxpool.Pool) ProductRepository {
	return &productRepo{db: db}
}

// ============================================================
//  UpsertProduct
// ============================================================

func (r *productRepo) UpsertProduct(ctx context.Context, p *domain.Product) (bool, error) {
	query := `
		INSERT INTO products (
			id, source_id, title, price, source_price, stock, fingerprint, last_scraped_at,
			created_at, updated_at, woo_id, detail_fetched_at, wp_cat_id, price_coeff,
			image_url, eways_cat_id, full_description, attributes, gallery_images
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (source_id) DO UPDATE SET
			title = EXCLUDED.title,
			price = EXCLUDED.price,
			source_price = EXCLUDED.source_price,
			stock = EXCLUDED.stock,
			fingerprint = EXCLUDED.fingerprint,
			last_scraped_at = EXCLUDED.last_scraped_at,
			updated_at = NOW(),
			woo_id = COALESCE(products.woo_id, EXCLUDED.woo_id),
			detail_fetched_at = COALESCE(EXCLUDED.detail_fetched_at, products.detail_fetched_at),
			wp_cat_id = EXCLUDED.wp_cat_id,
			price_coeff = EXCLUDED.price_coeff,
			image_url = EXCLUDED.image_url,
			eways_cat_id = EXCLUDED.eways_cat_id,
			full_description = EXCLUDED.full_description,
			attributes = EXCLUDED.attributes,
			gallery_images = EXCLUDED.gallery_images
		RETURNING id, created_at = updated_at AS created
	`
	var created bool
	var returnedID string

	// ============================================================
	// مدیریت فیلدهای JSON
	// ============================================================

	// ۱. Attributes: اگر خالی یا "null" باشد، NULL بفرست
	var attributesJSON interface{}
	if p.Attributes != "" && p.Attributes != "null" {
		attributesJSON = p.Attributes
	} else {
		attributesJSON = nil
	}

	// ۲. GalleryImages: اگر خالی باشد، nil بفرست
	var galleryJSON []byte
	if len(p.GalleryImages) > 0 {
		var err error
		galleryJSON, err = json.Marshal(p.GalleryImages)
		if err != nil {
			return false, fmt.Errorf("marshal gallery images: %w", err)
		}
	}

	err := r.db.QueryRow(ctx, query,
		p.ID, p.SourceID, p.Title, p.Price, p.SourcePrice, p.Stock, p.Fingerprint, p.LastScrapedAt,
		p.CreatedAt, p.UpdatedAt,
		p.WooID, p.DetailFetchedAt,
		p.WPCatID, p.PriceCoeff, p.ImageURL, p.EwaysCatID,
		p.FullDescription,
		attributesJSON,
		galleryJSON,
	).Scan(&returnedID, &created)
	if err != nil {
		return false, err
	}
	p.ID = returnedID
	return created, nil
}

// ============================================================
//  FindByID (با اصلاح NULL برای attributes و gallery_images)
// ============================================================

func (r *productRepo) FindByID(ctx context.Context, id string) (*domain.Product, error) {
	query := `
		SELECT id, source_id, title, price, source_price, stock, fingerprint, last_scraped_at,
		       created_at, updated_at, woo_id, detail_fetched_at, wp_cat_id, price_coeff,
		       image_url, eways_cat_id, full_description, attributes, gallery_images
		FROM products WHERE id = $1
	`
	var p domain.Product
	var galleryJSON []byte
	var attributes sql.NullString // 🔥 برای مدیریت NULL

	err := r.db.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.SourceID, &p.Title, &p.Price, &p.SourcePrice, &p.Stock, &p.Fingerprint,
		&p.LastScrapedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.WooID, &p.DetailFetchedAt,
		&p.WPCatID, &p.PriceCoeff, &p.ImageURL, &p.EwaysCatID,
		&p.FullDescription,
		&attributes, // ← استفاده از sql.NullString
		&galleryJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// تبدیل Attributes
	p.Attributes = attributes.String

	// تجزیه JSON گالری تصاویر
	if len(galleryJSON) > 0 {
		_ = json.Unmarshal(galleryJSON, &p.GalleryImages)
	}
	return &p, nil
}

// ============================================================
//  FindBySourceID (با اصلاح NULL برای attributes و gallery_images)
// ============================================================

func (r *productRepo) FindBySourceID(ctx context.Context, sourceID string) (*domain.Product, error) {
	query := `
		SELECT id, source_id, title, price, source_price, stock, fingerprint, last_scraped_at,
		       created_at, updated_at, woo_id, detail_fetched_at, wp_cat_id, price_coeff,
		       image_url, eways_cat_id, full_description, attributes, gallery_images
		FROM products WHERE source_id = $1
	`
	var p domain.Product
	var galleryJSON []byte
	var attributes sql.NullString // 🔥 برای مدیریت NULL

	err := r.db.QueryRow(ctx, query, sourceID).Scan(
		&p.ID, &p.SourceID, &p.Title, &p.Price, &p.SourcePrice, &p.Stock, &p.Fingerprint,
		&p.LastScrapedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.WooID, &p.DetailFetchedAt,
		&p.WPCatID, &p.PriceCoeff, &p.ImageURL, &p.EwaysCatID,
		&p.FullDescription,
		&attributes, // ← استفاده از sql.NullString
		&galleryJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	p.Attributes = attributes.String

	if len(galleryJSON) > 0 {
		_ = json.Unmarshal(galleryJSON, &p.GalleryImages)
	}
	return &p, nil
}

// ============================================================
//  UpdateLastScraped
// ============================================================

func (r *productRepo) UpdateLastScraped(ctx context.Context, productID string, t time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE products SET last_scraped_at = $1, updated_at = NOW() WHERE id = $2`, t, productID)
	return err
}

// ============================================================
//  UpdateWooID
// ============================================================

func (r *productRepo) UpdateWooID(ctx context.Context, productID string, wooID int64) error {
	_, err := r.db.Exec(ctx, `UPDATE products SET woo_id = $1, updated_at = NOW() WHERE id = $2`, wooID, productID)
	return err
}

// ============================================================
//  WithTransaction (استفاده از productTxRepo)
// ============================================================

func (r *productRepo) WithTransaction(ctx context.Context, fn func(ProductRepository) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	txRepo := &productTxRepo{tx: tx}
	if err := fn(txRepo); err != nil {
		tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// ============================================================
//  productTxRepo (نسخه تراکنشی داخلی برای WithTransaction)
// ============================================================

type productTxRepo struct {
	tx pgx.Tx
}

func (r *productTxRepo) UpsertProduct(ctx context.Context, p *domain.Product) (bool, error) {
	query := `
		INSERT INTO products (
			id, source_id, title, price, source_price, stock, fingerprint, last_scraped_at,
			created_at, updated_at, woo_id, detail_fetched_at, wp_cat_id, price_coeff,
			image_url, eways_cat_id, full_description, attributes, gallery_images
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (source_id) DO UPDATE SET
			title = EXCLUDED.title,
			price = EXCLUDED.price,
			source_price = EXCLUDED.source_price,
			stock = EXCLUDED.stock,
			fingerprint = EXCLUDED.fingerprint,
			last_scraped_at = EXCLUDED.last_scraped_at,
			updated_at = NOW(),
			woo_id = COALESCE(products.woo_id, EXCLUDED.woo_id),
			detail_fetched_at = COALESCE(EXCLUDED.detail_fetched_at, products.detail_fetched_at),
			wp_cat_id = EXCLUDED.wp_cat_id,
			price_coeff = EXCLUDED.price_coeff,
			image_url = EXCLUDED.image_url,
			eways_cat_id = EXCLUDED.eways_cat_id,
			full_description = EXCLUDED.full_description,
			attributes = EXCLUDED.attributes,
			gallery_images = EXCLUDED.gallery_images
		RETURNING id, created_at = updated_at AS created
	`
	var created bool
	var returnedID string

	// مدیریت JSON
	var attributesJSON interface{}
	if p.Attributes != "" && p.Attributes != "null" {
		attributesJSON = p.Attributes
	} else {
		attributesJSON = nil
	}

	var galleryJSON []byte
	if len(p.GalleryImages) > 0 {
		var err error
		galleryJSON, err = json.Marshal(p.GalleryImages)
		if err != nil {
			return false, fmt.Errorf("marshal gallery images: %w", err)
		}
	}

	err := r.tx.QueryRow(ctx, query,
		p.ID, p.SourceID, p.Title, p.Price, p.SourcePrice, p.Stock, p.Fingerprint, p.LastScrapedAt,
		p.CreatedAt, p.UpdatedAt,
		p.WooID, p.DetailFetchedAt,
		p.WPCatID, p.PriceCoeff, p.ImageURL, p.EwaysCatID,
		p.FullDescription,
		attributesJSON,
		galleryJSON,
	).Scan(&returnedID, &created)
	if err != nil {
		return false, err
	}
	p.ID = returnedID
	return created, nil
}

func (r *productTxRepo) FindByID(ctx context.Context, id string) (*domain.Product, error) {
	query := `
		SELECT id, source_id, title, price, source_price, stock, fingerprint, last_scraped_at,
		       created_at, updated_at, woo_id, detail_fetched_at, wp_cat_id, price_coeff,
		       image_url, eways_cat_id, full_description, attributes, gallery_images
		FROM products WHERE id = $1
	`
	var p domain.Product
	var galleryJSON []byte
	var attributes sql.NullString

	err := r.tx.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.SourceID, &p.Title, &p.Price, &p.SourcePrice, &p.Stock, &p.Fingerprint,
		&p.LastScrapedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.WooID, &p.DetailFetchedAt,
		&p.WPCatID, &p.PriceCoeff, &p.ImageURL, &p.EwaysCatID,
		&p.FullDescription,
		&attributes,
		&galleryJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	p.Attributes = attributes.String
	if len(galleryJSON) > 0 {
		_ = json.Unmarshal(galleryJSON, &p.GalleryImages)
	}
	return &p, nil
}

func (r *productTxRepo) FindBySourceID(ctx context.Context, sourceID string) (*domain.Product, error) {
	query := `
		SELECT id, source_id, title, price, source_price, stock, fingerprint, last_scraped_at,
		       created_at, updated_at, woo_id, detail_fetched_at, wp_cat_id, price_coeff,
		       image_url, eways_cat_id, full_description, attributes, gallery_images
		FROM products WHERE source_id = $1
	`
	var p domain.Product
	var galleryJSON []byte
	var attributes sql.NullString

	err := r.tx.QueryRow(ctx, query, sourceID).Scan(
		&p.ID, &p.SourceID, &p.Title, &p.Price, &p.SourcePrice, &p.Stock, &p.Fingerprint,
		&p.LastScrapedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.WooID, &p.DetailFetchedAt,
		&p.WPCatID, &p.PriceCoeff, &p.ImageURL, &p.EwaysCatID,
		&p.FullDescription,
		&attributes,
		&galleryJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	p.Attributes = attributes.String
	if len(galleryJSON) > 0 {
		_ = json.Unmarshal(galleryJSON, &p.GalleryImages)
	}
	return &p, nil
}

func (r *productTxRepo) UpdateLastScraped(ctx context.Context, productID string, t time.Time) error {
	_, err := r.tx.Exec(ctx, `UPDATE products SET last_scraped_at = $1, updated_at = NOW() WHERE id = $2`, t, productID)
	return err
}

func (r *productTxRepo) UpdateWooID(ctx context.Context, productID string, wooID int64) error {
	_, err := r.tx.Exec(ctx, `UPDATE products SET woo_id = $1, updated_at = NOW() WHERE id = $2`, wooID, productID)
	return err
}

func (r *productTxRepo) WithTransaction(ctx context.Context, fn func(ProductRepository) error) error {
	panic("nested transaction not supported")
}

// ============================================================
//  ProductTxRepo (نسخه تراکنشی خارجی برای استفاده در ChangeDetector)
// ============================================================

type ProductTxRepo struct {
	tx pgx.Tx
}

func NewProductTxRepo(tx pgx.Tx) *ProductTxRepo {
	return &ProductTxRepo{tx: tx}
}

func (r *ProductTxRepo) UpsertProduct(ctx context.Context, p *domain.Product) (bool, error) {
	// همانند productTxRepo.UpsertProduct (برای جلوگیری از تکرار، می‌توان از productTxRepo استفاده کرد)
	query := `
		INSERT INTO products (
			id, source_id, title, price, source_price, stock, fingerprint, last_scraped_at,
			created_at, updated_at, woo_id, detail_fetched_at, wp_cat_id, price_coeff,
			image_url, eways_cat_id, full_description, attributes, gallery_images
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (source_id) DO UPDATE SET
			title = EXCLUDED.title,
			price = EXCLUDED.price,
			source_price = EXCLUDED.source_price,
			stock = EXCLUDED.stock,
			fingerprint = EXCLUDED.fingerprint,
			last_scraped_at = EXCLUDED.last_scraped_at,
			updated_at = NOW(),
			woo_id = COALESCE(products.woo_id, EXCLUDED.woo_id),
			detail_fetched_at = COALESCE(EXCLUDED.detail_fetched_at, products.detail_fetched_at),
			wp_cat_id = EXCLUDED.wp_cat_id,
			price_coeff = EXCLUDED.price_coeff,
			image_url = EXCLUDED.image_url,
			eways_cat_id = EXCLUDED.eways_cat_id,
			full_description = EXCLUDED.full_description,
			attributes = EXCLUDED.attributes,
			gallery_images = EXCLUDED.gallery_images
		RETURNING id, created_at = updated_at AS created
	`
	var created bool
	var returnedID string

	var attributesJSON interface{}
	if p.Attributes != "" && p.Attributes != "null" {
		attributesJSON = p.Attributes
	} else {
		attributesJSON = nil
	}

	var galleryJSON []byte
	if len(p.GalleryImages) > 0 {
		var err error
		galleryJSON, err = json.Marshal(p.GalleryImages)
		if err != nil {
			return false, fmt.Errorf("marshal gallery images: %w", err)
		}
	}

	err := r.tx.QueryRow(ctx, query,
		p.ID, p.SourceID, p.Title, p.Price, p.SourcePrice, p.Stock, p.Fingerprint, p.LastScrapedAt,
		p.CreatedAt, p.UpdatedAt,
		p.WooID, p.DetailFetchedAt,
		p.WPCatID, p.PriceCoeff, p.ImageURL, p.EwaysCatID,
		p.FullDescription,
		attributesJSON,
		galleryJSON,
	).Scan(&returnedID, &created)
	if err != nil {
		return false, err
	}
	p.ID = returnedID
	return created, nil
}

func (r *ProductTxRepo) FindByID(ctx context.Context, id string) (*domain.Product, error) {
	query := `
		SELECT id, source_id, title, price, source_price, stock, fingerprint, last_scraped_at,
		       created_at, updated_at, woo_id, detail_fetched_at, wp_cat_id, price_coeff,
		       image_url, eways_cat_id, full_description, attributes, gallery_images
		FROM products WHERE id = $1
	`
	var p domain.Product
	var galleryJSON []byte
	var attributes sql.NullString

	err := r.tx.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.SourceID, &p.Title, &p.Price, &p.SourcePrice, &p.Stock, &p.Fingerprint,
		&p.LastScrapedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.WooID, &p.DetailFetchedAt,
		&p.WPCatID, &p.PriceCoeff, &p.ImageURL, &p.EwaysCatID,
		&p.FullDescription,
		&attributes,
		&galleryJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	p.Attributes = attributes.String
	if len(galleryJSON) > 0 {
		_ = json.Unmarshal(galleryJSON, &p.GalleryImages)
	}
	return &p, nil
}

func (r *ProductTxRepo) FindBySourceID(ctx context.Context, sourceID string) (*domain.Product, error) {
	query := `
		SELECT id, source_id, title, price, source_price, stock, fingerprint, last_scraped_at,
		       created_at, updated_at, woo_id, detail_fetched_at, wp_cat_id, price_coeff,
		       image_url, eways_cat_id, full_description, attributes, gallery_images
		FROM products WHERE source_id = $1
	`
	var p domain.Product
	var galleryJSON []byte
	var attributes sql.NullString

	err := r.tx.QueryRow(ctx, query, sourceID).Scan(
		&p.ID, &p.SourceID, &p.Title, &p.Price, &p.SourcePrice, &p.Stock, &p.Fingerprint,
		&p.LastScrapedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.WooID, &p.DetailFetchedAt,
		&p.WPCatID, &p.PriceCoeff, &p.ImageURL, &p.EwaysCatID,
		&p.FullDescription,
		&attributes,
		&galleryJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	p.Attributes = attributes.String
	if len(galleryJSON) > 0 {
		_ = json.Unmarshal(galleryJSON, &p.GalleryImages)
	}
	return &p, nil
}

func (r *ProductTxRepo) UpdateLastScraped(ctx context.Context, productID string, t time.Time) error {
	_, err := r.tx.Exec(ctx, `UPDATE products SET last_scraped_at = $1, updated_at = NOW() WHERE id = $2`, t, productID)
	return err
}

func (r *ProductTxRepo) UpdateWooID(ctx context.Context, productID string, wooID int64) error {
	_, err := r.tx.Exec(ctx, `UPDATE products SET woo_id = $1, updated_at = NOW() WHERE id = $2`, wooID, productID)
	return err
}

func (r *ProductTxRepo) WithTransaction(ctx context.Context, fn func(ProductRepository) error) error {
	panic("nested transaction not supported")
}