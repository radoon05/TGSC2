package woo

import (
	"encoding/json"
	"fmt"

	"tgsc/internal/config"
	"tgsc/internal/domain"
)

// ============================================================
//  ساختارهای ووکامرس
// ============================================================

type WooProduct struct {
	ID            int64      `json:"id,omitempty"`
	SKU           string     `json:"sku,omitempty"`
	Name          string     `json:"name,omitempty"`
	RegularPrice  string     `json:"regular_price,omitempty"`
	StockQuantity int        `json:"stock_quantity,omitempty"`
	ManageStock   bool       `json:"manage_stock,omitempty"`
	Status        string     `json:"status,omitempty"`
	Description   string     `json:"description,omitempty"`
	Categories    []WooCat   `json:"categories,omitempty"`
	Images        []WooImage `json:"images,omitempty"`
	Attributes    []WooAttr  `json:"attributes,omitempty"`
	MetaData      []WooMeta  `json:"meta_data,omitempty"`
}

type WooCat struct {
	ID int `json:"id"`
}

type WooImage struct {
	Src string `json:"src"`
}

type WooAttr struct {
	Name      string   `json:"name"`
	Options   []string `json:"options"`
	Visible   bool     `json:"visible,omitempty"`
	Variation bool     `json:"variation,omitempty"`
}

type WooMeta struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

// ============================================================
//  MapToCreate - برای ایجاد محصول جدید (همه فیلدها)
// ============================================================
func MapToCreate(p *domain.Product) *WooProduct {
	wp := &WooProduct{
		SKU:           p.SourceID,
		Name:          p.Title,
		RegularPrice:  fmt.Sprintf("%.0f", p.Price), // قیمت نهایی قبلاً توسط Normalizer محاسبه شده
		StockQuantity: p.Stock,
		ManageStock:   true,
		Status:        "publish",
	}

	// ۱. توضیحات
	if p.FullDescription != "" {
		wp.Description = p.FullDescription
	} else if config.ProductDescriptionHTML != "" {
		wp.Description = config.ProductDescriptionHTML
	}

	// ۲. تصاویر (گالری)
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

	// ۳. ویژگی‌ها
	if p.Attributes != "" {
		var rawAttrs []struct {
			Name  string `json:"Name"`
			Value string `json:"Value"`
		}
		if err := json.Unmarshal([]byte(p.Attributes), &rawAttrs); err == nil && len(rawAttrs) > 0 {
			wooAttrs := make([]WooAttr, 0, len(rawAttrs))
			for _, a := range rawAttrs {
				wooAttrs = append(wooAttrs, WooAttr{
					Name:      a.Name,
					Options:   []string{a.Value},
					Visible:   true,
					Variation: false,
				})
			}
			wp.Attributes = wooAttrs
		}
	}

	// ۴. دسته‌بندی
	if p.WPCatID > 0 {
		wp.Categories = []WooCat{{ID: p.WPCatID}}
	}

	// ۵. متادیتا: قیمت خام از ایویز
	if p.SourcePrice > 0 {
		wp.MetaData = append(wp.MetaData, WooMeta{
			Key:   "_source_price",
			Value: p.SourcePrice,
		})
	}

	return wp
}

// ============================================================
//  MapToUpdate - برای آپدیت محصول (حداقلی: فقط قیمت و موجودی)
// ============================================================
func MapToUpdate(p *domain.Product) *WooProduct {
    wp := &WooProduct{
        ID:            p.WooID,
        RegularPrice:  fmt.Sprintf("%.0f", p.Price),
        StockQuantity: p.Stock,
    }

    // 🔥 ارسال دسته‌بندی (اگر موجود باشد)
    if p.WPCatID > 0 {
        wp.Categories = []WooCat{{ID: p.WPCatID}}
    }

    // 🔥 ارسال تصاویر (اگر در دیتابیس وجود داشته باشد)
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

    // 🔥 ارسال ویژگی‌ها (اگر در دیتابیس وجود داشته باشد)
    if p.Attributes != "" && p.Attributes != "[]" {
        var rawAttrs []struct {
            Name  string `json:"Name"`
            Value string `json:"Value"`
        }
        if err := json.Unmarshal([]byte(p.Attributes), &rawAttrs); err == nil && len(rawAttrs) > 0 {
            wooAttrs := make([]WooAttr, 0, len(rawAttrs))
            for _, a := range rawAttrs {
                wooAttrs = append(wooAttrs, WooAttr{
                    Name:      a.Name,
                    Options:   []string{a.Value},
                    Visible:   true,
                    Variation: false,
                })
            }
            wp.Attributes = wooAttrs
        }
    }

    return wp
}



// func MapToUpdate(p *domain.Product) *WooProduct {
// 	// فقط فیلدهای لازم برای آپدیت
// 	return &WooProduct{
// 		ID:            p.WooID,                      // شناسه ووکامرس (ضروری)
// 		RegularPrice:  fmt.Sprintf("%.0f", p.Price), // قیمت نهایی
// 		StockQuantity: p.Stock,                     // موجودی
		
// 		// بقیه فیلدها ارسال نمی‌شوند تا از بازنویسی غیرضروری جلوگیری شود
// 	}
// }