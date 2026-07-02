package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tgsc/internal/domain"
)

// ProductRepository defines the interface for product persistence
type ProductRepository interface {
	UpsertProduct(ctx context.Context, p *domain.Product) (created bool, err error)
	FindByID(ctx context.Context, id string) (*domain.Product, error)
	FindBySourceID(ctx context.Context, sourceID string) (*domain.Product, error)
	UpdateLastScraped(ctx context.Context, productID string, t time.Time) error
	WithTransaction(ctx context.Context, fn func(ProductRepository) error) error
}

type productRepo struct {
	db *pgxpool.Pool
}

// NewProductRepository creates a new product repository
func NewProductRepository(db *pgxpool.Pool) ProductRepository {
	return &productRepo{db: db}
}

// UpsertProduct inserts or updates a product with all fields including new ones
func (r *productRepo) UpsertProduct(ctx context.Context, p *domain.Product) (bool, error) {
	query := `
		INSERT INTO products (
			id, source_id, title, price, stock, fingerprint, last_scraped_at,
			created_at, updated_at, wp_cat_id, price_coeff, image_url, eways_cat_id,
			full_description, attributes, gallery_images
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (source_id) DO UPDATE SET
			title = EXCLUDED.title,
			price = EXCLUDED.price,
			stock = EXCLUDED.stock,
			fingerprint = EXCLUDED.fingerprint,
			last_scraped_at = EXCLUDED.last_scraped_at,
			updated_at = NOW(),
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

	// تبدیل GalleryImages به JSON برای PostgreSQL
	var galleryJSON []byte
	if len(p.GalleryImages) > 0 {
		var err error
		galleryJSON, err = json.Marshal(p.GalleryImages)
		if err != nil {
			return false, err
		}
	}

	err := r.db.QueryRow(ctx, query,
		p.ID, p.SourceID, p.Title, p.Price, p.Stock, p.Fingerprint, p.LastScrapedAt,
		p.CreatedAt, p.UpdatedAt,
		p.WPCatID, p.PriceCoeff, p.ImageURL, p.EwaysCatID,
		p.FullDescription, p.Attributes, galleryJSON,
	).Scan(&returnedID, &created)
	if err != nil {
		return false, err
	}
	p.ID = returnedID
	return created, nil
}

// FindByID retrieves a product by its UUID
func (r *productRepo) FindByID(ctx context.Context, id string) (*domain.Product, error) {
	query := `
		SELECT id, source_id, title, price, stock, fingerprint, last_scraped_at,
		       created_at, updated_at, wp_cat_id, price_coeff, image_url, eways_cat_id,
		       full_description, attributes, gallery_images
		FROM products WHERE id = $1
	`
	var p domain.Product
	var galleryJSON []byte

	err := r.db.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.SourceID, &p.Title, &p.Price, &p.Stock, &p.Fingerprint,
		&p.LastScrapedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.WPCatID, &p.PriceCoeff, &p.ImageURL, &p.EwaysCatID,
		&p.FullDescription, &p.Attributes, &galleryJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// تجزیه JSON گالری تصاویر
	if len(galleryJSON) > 0 {
		if err := json.Unmarshal(galleryJSON, &p.GalleryImages); err != nil {
			// فقط لاگ می‌کنیم و ادامه می‌دهیم (در صورت نیاز لاگ اضافه کنید)
		}
	}
	return &p, nil
}

// FindBySourceID retrieves a product by its source ID (Eways product ID)
func (r *productRepo) FindBySourceID(ctx context.Context, sourceID string) (*domain.Product, error) {
	query := `
		SELECT id, source_id, title, price, stock, fingerprint, last_scraped_at,
		       created_at, updated_at, wp_cat_id, price_coeff, image_url, eways_cat_id,
		       full_description, attributes, gallery_images
		FROM products WHERE source_id = $1
	`
	var p domain.Product
	var galleryJSON []byte

	err := r.db.QueryRow(ctx, query, sourceID).Scan(
		&p.ID, &p.SourceID, &p.Title, &p.Price, &p.Stock, &p.Fingerprint,
		&p.LastScrapedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.WPCatID, &p.PriceCoeff, &p.ImageURL, &p.EwaysCatID,
		&p.FullDescription, &p.Attributes, &galleryJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if len(galleryJSON) > 0 {
		if err := json.Unmarshal(galleryJSON, &p.GalleryImages); err != nil {
			// فقط لاگ می‌کنیم و ادامه می‌دهیم
		}
	}
	return &p, nil
}

// UpdateLastScraped updates only the last_scraped_at field
func (r *productRepo) UpdateLastScraped(ctx context.Context, productID string, t time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE products SET last_scraped_at = $1, updated_at = NOW() WHERE id = $2`, t, productID)
	return err
}

// WithTransaction executes a function within a database transaction
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
//  productTxRepo (Transaction-aware repository)
// ============================================================

type productTxRepo struct {
	tx pgx.Tx
}

func (r *productTxRepo) UpsertProduct(ctx context.Context, p *domain.Product) (bool, error) {
	query := `
		INSERT INTO products (
			id, source_id, title, price, stock, fingerprint, last_scraped_at,
			created_at, updated_at, wp_cat_id, price_coeff, image_url, eways_cat_id,
			full_description, attributes, gallery_images
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (source_id) DO UPDATE SET
			title = EXCLUDED.title,
			price = EXCLUDED.price,
			stock = EXCLUDED.stock,
			fingerprint = EXCLUDED.fingerprint,
			last_scraped_at = EXCLUDED.last_scraped_at,
			updated_at = NOW(),
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

	var galleryJSON []byte
	if len(p.GalleryImages) > 0 {
		var err error
		galleryJSON, err = json.Marshal(p.GalleryImages)
		if err != nil {
			return false, err
		}
	}

	err := r.tx.QueryRow(ctx, query,
		p.ID, p.SourceID, p.Title, p.Price, p.Stock, p.Fingerprint, p.LastScrapedAt,
		p.CreatedAt, p.UpdatedAt,
		p.WPCatID, p.PriceCoeff, p.ImageURL, p.EwaysCatID,
		p.FullDescription, p.Attributes, galleryJSON,
	).Scan(&returnedID, &created)
	if err != nil {
		return false, err
	}
	p.ID = returnedID
	return created, nil
}

func (r *productTxRepo) FindByID(ctx context.Context, id string) (*domain.Product, error) {
	query := `
		SELECT id, source_id, title, price, stock, fingerprint, last_scraped_at,
		       created_at, updated_at, wp_cat_id, price_coeff, image_url, eways_cat_id,
		       full_description, attributes, gallery_images
		FROM products WHERE id = $1
	`
	var p domain.Product
	var galleryJSON []byte

	err := r.tx.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.SourceID, &p.Title, &p.Price, &p.Stock, &p.Fingerprint,
		&p.LastScrapedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.WPCatID, &p.PriceCoeff, &p.ImageURL, &p.EwaysCatID,
		&p.FullDescription, &p.Attributes, &galleryJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if len(galleryJSON) > 0 {
		json.Unmarshal(galleryJSON, &p.GalleryImages)
	}
	return &p, nil
}

func (r *productTxRepo) FindBySourceID(ctx context.Context, sourceID string) (*domain.Product, error) {
	query := `
		SELECT id, source_id, title, price, stock, fingerprint, last_scraped_at,
		       created_at, updated_at, wp_cat_id, price_coeff, image_url, eways_cat_id,
		       full_description, attributes, gallery_images
		FROM products WHERE source_id = $1
	`
	var p domain.Product
	var galleryJSON []byte

	err := r.tx.QueryRow(ctx, query, sourceID).Scan(
		&p.ID, &p.SourceID, &p.Title, &p.Price, &p.Stock, &p.Fingerprint,
		&p.LastScrapedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.WPCatID, &p.PriceCoeff, &p.ImageURL, &p.EwaysCatID,
		&p.FullDescription, &p.Attributes, &galleryJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if len(galleryJSON) > 0 {
		json.Unmarshal(galleryJSON, &p.GalleryImages)
	}
	return &p, nil
}

func (r *productTxRepo) UpdateLastScraped(ctx context.Context, productID string, t time.Time) error {
	_, err := r.tx.Exec(ctx, `UPDATE products SET last_scraped_at = $1, updated_at = NOW() WHERE id = $2`, t, productID)
	return err
}

func (r *productTxRepo) WithTransaction(ctx context.Context, fn func(ProductRepository) error) error {
	panic("nested transaction not supported")
}