package woo

import (
	"fmt"

	"tgsc/internal/domain"
)

// WooProduct represents the WooCommerce product schema (simplified).
type WooProduct struct {
	ID            int64  `json:"id,omitempty"`
	SKU           string `json:"sku,omitempty"`
	Name          string `json:"name"`
	RegularPrice  string `json:"regular_price,omitempty"`
	Price         string `json:"price,omitempty"`
	StockQuantity int    `json:"stock_quantity,omitempty"`
	ManageStock   bool   `json:"manage_stock,omitempty"`
}

// MapDomainToWoo converts a domain product to WooCommerce product format.
func MapDomainToWoo(p *domain.Product) *WooProduct {
	return &WooProduct{
		SKU:           p.SourceID,
		Name:          p.Title,
		RegularPrice:  formatPrice(p.Price),
		Price:         formatPrice(p.Price),
		StockQuantity: p.Stock,
		ManageStock:   true,
	}
}

// formatPrice converts float64 to string with two decimals.
func formatPrice(price float64) string {
	return fmt.Sprintf("%.2f", price)
}