package woo

import (
	"fmt"
	"tgsc/internal/config"
	"tgsc/internal/domain"
)

// WooProduct represents the WooCommerce product schema (full version)
type WooProduct struct {
	ID            int64       `json:"id,omitempty"`
	SKU           string      `json:"sku,omitempty"`
	Name          string      `json:"name"`
	RegularPrice  string      `json:"regular_price"`
	StockQuantity int         `json:"stock_quantity"`
	ManageStock   bool        `json:"manage_stock"`
	Status        string      `json:"status,omitempty"`
	Description   string      `json:"description,omitempty"`
	Categories    []WooCat    `json:"categories,omitempty"`
	Images        []WooImage  `json:"images,omitempty"`
}

type WooCat struct {
	ID int `json:"id"`
}

type WooImage struct {
	Src string `json:"src"`
}

// MapDomainToWoo converts a domain product to WooCommerce product format with all fields
func MapDomainToWoo(p *domain.Product, fixedCost, roundTo float64) *WooProduct {
	// 🔥 اگر ضریب صفر است، از ۱ استفاده کن
	coeff := p.PriceCoeff
	if coeff == 0 {
		coeff = 1.0
	}

	// 1. تبدیل ریال به تومان
	priceToman := p.Price / 10.0

	// 2. اعمال ضریب
	priceWithCoeff := priceToman * coeff

	// 3. اضافه کردن هزینه ثابت
	priceWithFixed := priceWithCoeff + fixedCost

	// 4. گرد کردن به مضرب roundTo
	var finalPrice float64
	if roundTo > 0 {
		finalPrice = float64(int(priceWithFixed/roundTo+0.5)) * roundTo
	} else {
		finalPrice = priceWithFixed
	}

	// ساخت محصول ووکامرس
	wp := &WooProduct{
		SKU:           p.SourceID,
		Name:          p.Title,
		RegularPrice:  fmt.Sprintf("%.0f", finalPrice),
		StockQuantity: p.Stock,
		ManageStock:   true,
		Status:        "publish",
	}

	// افزودن توضیحات (از config)
	if config.ProductDescriptionHTML != "" {
		wp.Description = config.ProductDescriptionHTML
	}

	// افزودن دسته‌بندی (اگر WPCatID > 0 باشد)
	if p.WPCatID > 0 {
		wp.Categories = []WooCat{{ID: p.WPCatID}}
	}

	// افزودن تصویر (اگر ImageURL موجود باشد)
	if p.ImageURL != "" {
		wp.Images = []WooImage{{Src: p.ImageURL}}
	}

	return wp
}