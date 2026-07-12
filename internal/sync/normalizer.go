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
	fixedCost   float64 // هزینه ثابت برای محاسبه قیمت نهایی
	roundTo     float64 // گرد کردن قیمت به مضرب
}

// NewNormalizer creates a normalizer with default settings.
func NewNormalizer(fixedCost, roundTo float64) *Normalizer {
	return &Normalizer{
		stockBuffer: 0,
		fixedCost:   fixedCost,
		roundTo:     roundTo,
	}
}

// ============================================================
//  محاسبه قیمت نهایی (متمرکز در یک نقطه)
// ============================================================

// CalculateFinalPrice قیمت نهایی را از ریال به تومان تبدیل، ضریب و سود ثابت اعمال کرده و گرد می‌کند
// این تابع تنها نقطه‌ای است که قیمت نهایی محاسبه می‌شود
func (n *Normalizer) CalculateFinalPrice(priceRial float64, coeff float64) float64 {
	// 1. اگر قیمت صفر یا منفی است، صفر برگردان (از انتشار محصول با قیمت اشتباه جلوگیری کن)
	if priceRial <= 0 {
		return 0
	}

	// 2. تبدیل ریال به تومان (هر ۱۰ ریال = ۱ تومان)
	priceToman := priceRial / 10.0

	// 3. اعمال ضریب دسته‌بندی
	priceWithCoeff := priceToman * coeff

	// 4. اضافه کردن هزینه ثابت
	priceWithFixed := priceWithCoeff + n.fixedCost

	// 5. گرد کردن به مضرب roundTo (مثلاً ۱۰۰۰ تومان)
	if n.roundTo > 0 {
		return float64(int(priceWithFixed/n.roundTo+0.5)) * n.roundTo
	}
	return priceWithFixed
}

// ============================================================
//  توابع نرمال‌سازی
// ============================================================

// NormalizePrice (قدیمی) – برای سازگاری نگه‌داشته شده ولی دیگر استفاده نمی‌شود
// Deprecated: از CalculateFinalPrice استفاده کنید
func (n *Normalizer) NormalizePrice(price float64) float64 {
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
// قیمت نهایی با استفاده از CalculateFinalPrice محاسبه می‌شود
func (n *Normalizer) Normalize(p *domain.Product) *domain.Product {
	normalized := *p

	// 🔥 محاسبه قیمت نهایی با استفاده از تابع متمرکز
	normalized.Price = n.CalculateFinalPrice(p.SourcePrice, p.PriceCoeff)

	normalized.Stock = n.NormalizeStock(p.Stock)
	normalized.Title = n.NormalizeTitle(p.Title)

	return &normalized
}

// GenerateFingerprint creates a hash from normalized significant fields.
// از Price نهایی (که قبلاً محاسبه شده) استفاده می‌کند
func (n *Normalizer) GenerateFingerprint(p *domain.Product) string {
	data := fmt.Sprintf("%s|%.2f|%d", p.Title, p.Price, p.Stock)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// SetStockBuffer allows dynamic configuration.
func (n *Normalizer) SetStockBuffer(buffer int) {
	n.stockBuffer = buffer
}

// SetFixedCost allows dynamic update of fixed cost (برای reload بدون ری‌استارت)
func (n *Normalizer) SetFixedCost(fixedCost float64) {
	n.fixedCost = fixedCost
}

// SetRoundTo allows dynamic update of roundTo (برای reload بدون ری‌استارت)
func (n *Normalizer) SetRoundTo(roundTo float64) {
	n.roundTo = roundTo
}