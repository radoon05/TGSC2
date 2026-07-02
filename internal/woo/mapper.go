package woo

import (
	"encoding/json"
	"fmt"

	"tgsc/internal/config"
	"tgsc/internal/domain"
)

// WooProduct represents the WooCommerce product schema (full version)
type WooProduct struct {
	ID            int64      `json:"id,omitempty"`
	SKU           string     `json:"sku,omitempty"`
	Name          string     `json:"name"`
	RegularPrice  string     `json:"regular_price"`
	StockQuantity int        `json:"stock_quantity"`
	ManageStock   bool       `json:"manage_stock"`
	Status        string     `json:"status,omitempty"`
	Description   string     `json:"description,omitempty"`
	Categories    []WooCat   `json:"categories,omitempty"`
	Images        []WooImage `json:"images,omitempty"`
	Attributes    []WooAttr  `json:"attributes,omitempty"`
}

// WooCat represents a product category in WooCommerce
type WooCat struct {
	ID int `json:"id"`
}

// WooImage represents a product image in WooCommerce
type WooImage struct {
	Src string `json:"src"`
}

// WooAttr represents a product attribute in WooCommerce
type WooAttr struct {
	Name      string   `json:"name"`
	Options   []string `json:"options"`
	Visible   bool     `json:"visible,omitempty"`
	Variation bool     `json:"variation,omitempty"`
}

// MapDomainToWoo converts a domain product to WooCommerce product format with all fields
func MapDomainToWoo(p *domain.Product, fixedCost, roundTo float64) *WooProduct {
	// ============================================================
	// ۱. محاسبه قیمت نهایی
	// ============================================================
	coeff := p.PriceCoeff
	if coeff == 0 {
		coeff = 1.0
	}

	// تبدیل ریال به تومان
	priceToman := p.Price / 10.0

	// اعمال ضریب
	priceWithCoeff := priceToman * coeff

	// اضافه کردن هزینه ثابت
	priceWithFixed := priceWithCoeff + fixedCost

	// گرد کردن به مضرب roundTo
	var finalPrice float64
	if roundTo > 0 {
		finalPrice = float64(int(priceWithFixed/roundTo+0.5)) * roundTo
	} else {
		finalPrice = priceWithFixed
	}

	// ============================================================
	// ۲. ساخت محصول پایه
	// ============================================================
	wp := &WooProduct{
		SKU:           p.SourceID,
		Name:          p.Title,
		RegularPrice:  fmt.Sprintf("%.0f", finalPrice),
		StockQuantity: p.Stock,
		ManageStock:   true,
		Status:        "publish",
	}

	// ============================================================
	// ۳. توضیحات محصول (اولویت با FullDescription، سپس config)
	// ============================================================
	if p.FullDescription != "" {
		wp.Description = p.FullDescription
	} else if config.ProductDescriptionHTML != "" {
		wp.Description = config.ProductDescriptionHTML
	}

	// ============================================================
	// ۴. تصاویر محصول (اولویت با گالری، سپس ImageURL تکی)
	// ============================================================
	if len(p.GalleryImages) > 0 {
		images := make([]WooImage, 0, len(p.GalleryImages))
		for _, url := range p.GalleryImages {
			if url != "" {
				images = append(images, WooImage{Src: url})
			}
		}
		if len(images) > 0 {
			wp.Images = images
		}
	} else if p.ImageURL != "" {
		wp.Images = []WooImage{{Src: p.ImageURL}}
	}

	// ============================================================
	// ۵. ویژگی‌ها (Attributes) – تبدیل JSON به ساختار ووکامرس
	// ============================================================
	if p.Attributes != "" {
		// ساختار موقت برای خواندن از دیتابیس (همان ساختار ایویز)
		var rawAttrs []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		}
		if err := json.Unmarshal([]byte(p.Attributes), &rawAttrs); err == nil && len(rawAttrs) > 0 {
			wooAttrs := make([]WooAttr, 0, len(rawAttrs))
			for _, a := range rawAttrs {
				wooAttrs = append(wooAttrs, WooAttr{
					Name:      a.Name,
					Options:   []string{a.Value}, // هر مقدار به عنوان یک گزینه
					Visible:   true,
					Variation: false,
				})
			}
			wp.Attributes = wooAttrs
		}
	}

	// ============================================================
	// ۶. دسته‌بندی
	// ============================================================
	if p.WPCatID > 0 {
		wp.Categories = []WooCat{{ID: p.WPCatID}}
	}

	return wp
}