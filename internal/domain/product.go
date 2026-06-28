package domain

import (
	"time"
)

// Product represents a product from the source (scraped) and the canonical model for sync.
type Product struct {
	ID            string    `json:"id" db:"id"`
	SourceID      string    `json:"source_id" db:"source_id"`
	Title         string    `json:"title" db:"title"`
	Price         float64   `json:"price" db:"price"`
	Stock         int       `json:"stock" db:"stock"`
	Fingerprint   string    `json:"fingerprint" db:"fingerprint"` // hash of significant fields
	LastScrapedAt time.Time `json:"last_scraped_at" db:"last_scraped_at"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}