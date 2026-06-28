package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"tgsc/internal/domain"
)

// Normalizer handles pure business logic: price, stock, title normalization and fingerprint.
type Normalizer struct {
	stockBuffer int
}

// NewNormalizer creates a normalizer with default settings.
func NewNormalizer() *Normalizer {
	return &Normalizer{
		stockBuffer: 0,
	}
}

// NormalizePrice applies business rules to price.
func (n *Normalizer) NormalizePrice(price float64) float64 {
	// round to 2 decimals
	return float64(int(price*100+0.5)) / 100
}

// NormalizeStock applies stock buffer logic.
func (n *Normalizer) NormalizeStock(stock int) int {
	if stock < 0 {
		return 0
	}
	if n.stockBuffer > 0 && stock > n.stockBuffer {
		return stock - n.stockBuffer
	}
	return stock
}

// NormalizeTitle trims and cleans title.
func (n *Normalizer) NormalizeTitle(title string) string {
	return strings.TrimSpace(title)
}

// Normalize applies all normalizations to a product and returns a new product.
func (n *Normalizer) Normalize(p *domain.Product) *domain.Product {
	normalized := *p
	normalized.Price = n.NormalizePrice(p.Price)
	normalized.Stock = n.NormalizeStock(p.Stock)
	normalized.Title = n.NormalizeTitle(p.Title)
	return &normalized
}

// GenerateFingerprint creates a hash from normalized significant fields.
func (n *Normalizer) GenerateFingerprint(p *domain.Product) string {
	data := fmt.Sprintf("%s|%.2f|%d", p.Title, p.Price, p.Stock)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// SetStockBuffer allows dynamic configuration.
func (n *Normalizer) SetStockBuffer(buffer int) {
	n.stockBuffer = buffer
}