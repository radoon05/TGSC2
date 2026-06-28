package scraper

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"tgsc/internal/domain"
)

// Parser converts raw HTTP response body into domain products
type Parser struct{}

// NewParser creates a new parser instance
func NewParser() *Parser {
	return &Parser{}
}

// ParseProducts reads JSON from response body and returns a slice of domain products
// It uses streaming decoder to avoid loading entire body into memory
func (p *Parser) ParseProducts(body io.Reader) ([]*domain.Product, error) {
	dec := json.NewDecoder(body)

	// Expect JSON array at root level
	token, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("reading JSON token: %w", err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '[' {
		return nil, fmt.Errorf("expected JSON array, got %v", token)
	}

	var products []*domain.Product
	for dec.More() {
		var raw scrapedItem
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("decoding item: %w", err)
		}
		products = append(products, raw.toDomain())
	}

	// consume closing ']'
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("reading closing bracket: %w", err)
	}

	return products, nil
}

// scrapedItem represents the raw JSON structure from external API (adjust to actual schema)
type scrapedItem struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Price float64 `json:"price"`
	Stock int     `json:"stock"`
}

// toDomain converts a scraped item to domain.Product (without fingerprint and id)
func (s *scrapedItem) toDomain() *domain.Product {
	return &domain.Product{
		SourceID:      s.ID,
		Title:         s.Title,
		Price:         s.Price,
		Stock:         s.Stock,
		LastScrapedAt: time.Now(),
	}
}