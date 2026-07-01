package config

import (
	"database/sql"
	// "log"
	"os"
	"strconv"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

// ================================================================
//  ساختارهای اصلی
// ================================================================

type Config struct {
	Database   DatabaseConfig
	Scraper    ScraperConfig
	Woo        WooConfig
	Sync       SyncConfig
	App        AppConfig
	Eways      EwaysConfig
	HTTPPort   string
	LogLevel   string
}

type DatabaseConfig struct {
	URL string
}

type ScraperConfig struct {
	BaseURL      string
	Timeout      time.Duration
	RateLimit    int
	RetryMax     int
	RetryBackoff time.Duration
	PageSize     int
}

type WooConfig struct {
	BaseURL         string
	ConsumerKey     string
	ConsumerSecret  string
	Timeout         time.Duration
	RateLimit       int
	BatchCreateSize int
	BatchUpdateSize int
}

type SyncConfig struct {
	UpdateWorkerCount int
	UpdateFetchLimit  int
	UpdateBatchSize   int
	CreateWorkerCount int
	CreateFetchLimit  int
	CreateBatchSize   int
	RetryBackoffBase  time.Duration
	MaxRetries        int
}

type AppConfig struct {
	FixedCost   float64
	RoundTo     float64
	IsDryRun    bool
	Description string
}

type EwaysConfig struct {
	LoginURL string
	Username string
	Password string
}

// ================================================================
//  Category و متغیرهای سراسری
// ================================================================

type Category struct {
	Name            string
	EwaysCatID      string
	WPCatID         int
	PriceCoeff      float64
	TitlePrefix     string
	CoefficientType string // "percent" یا "fixed"
	FixedProfit     float64
}

var (
	Categories   []Category
	FixedCost    float64
	RoundTo      float64
	IsDryRun     bool
	db           *sql.DB
	mu           sync.RWMutex
)

// ================================================================
//  لیست کامل دسته‌بندی‌ها (پیش‌فرض)
// ================================================================

var defaultCategories = []Category{
	// 1. قاب و کاور
	{Name: "قاب و کاور اپل", EwaysCatID: "19136", WPCatID: 365, PriceCoeff: 1.10, TitlePrefix: ""},
	{Name: "قاب و کاور سامسونگ", EwaysCatID: "19178", WPCatID: 367, PriceCoeff: 1.10, TitlePrefix: ""},
	{Name: "قاب و کاور شیائومی", EwaysCatID: "2470", WPCatID: 369, PriceCoeff: 1.10, TitlePrefix: ""},
	{Name: "قاب و کاور هوآوی", EwaysCatID: "1168", WPCatID: 371, PriceCoeff: 1.10, TitlePrefix: ""},

	// 2. محافظ صفحه نمایش (گلس)
	{Name: "محافظ صفحه نمایش موبایل آیفون", EwaysCatID: "3374", WPCatID: 373, PriceCoeff: 1.3, TitlePrefix: "گلس و "},
	{Name: "محافظ صفحه نمایش موبایل سامسونگ", EwaysCatID: "3354", WPCatID: 375, PriceCoeff: 1.3, TitlePrefix: "گلس و "},
	{Name: "محافظ صفحه نمایش موبایل شیائومی", EwaysCatID: "3374", WPCatID: 377, PriceCoeff: 1.3, TitlePrefix: "گلس و "},
	{Name: "محافظ صفحه نمایش موبایل ریل می", EwaysCatID: "19667", WPCatID: 379, PriceCoeff: 1.3, TitlePrefix: "گلس و "},
	{Name: "محافظ صفحه نمایش موبایل هوآوی", EwaysCatID: "3374", WPCatID: 381, PriceCoeff: 1.3, TitlePrefix: "گلس و "},

	// 3. ساعت هوشمند
	{Name: "ساعت هوشمند", EwaysCatID: "14548", WPCatID: 357, PriceCoeff: 1.12, TitlePrefix: ""},
	{Name: "بند ساعت هوشمند", EwaysCatID: "9251", WPCatID: 385, PriceCoeff: 1.16, TitlePrefix: ""},

	// 4. لوازم صوتی
	{Name: "هدفون، هندزفری و هدست", EwaysCatID: "1593", WPCatID: 359, PriceCoeff: 1.1, TitlePrefix: ""},
	{Name: "هدفون", EwaysCatID: "2550", WPCatID: 395, PriceCoeff: 1.06, TitlePrefix: ""},
	{Name: "هندزفری باسیم", EwaysCatID: "2390", WPCatID: 397, PriceCoeff: 1.06, TitlePrefix: ""},
	{Name: "هندزفری گردنی", EwaysCatID: "12898", WPCatID: 399, PriceCoeff: 1.06, TitlePrefix: ""},
	{Name: "ایرفون و ایرپادز", EwaysCatID: "12896", WPCatID: 360, PriceCoeff: 1.08, TitlePrefix: ""},

	// 5. پاوربانک
	{Name: "پاوربانک ۱۰٬۰۰۰ میلی‌آمپر", EwaysCatID: "13091", WPCatID: 387, PriceCoeff: 1.3, TitlePrefix: ""},
	{Name: "پاوربانک ۲۰٬۰۰۰ میلی‌آمپر", EwaysCatID: "13092", WPCatID: 389, PriceCoeff: 1.3, TitlePrefix: ""},
	{Name: "پاوربانک ۳۰٬۰۰۰ میلی‌آمپر", EwaysCatID: "18058", WPCatID: 391, PriceCoeff: 1.3, TitlePrefix: ""},

	// 6. لوازم جانبی موبایل
	{Name: "آداپتور شارژر", EwaysCatID: "1585", WPCatID: 401, PriceCoeff: 1.16, TitlePrefix: ""},
	{Name: "شارژر گوشی", EwaysCatID: "1585", WPCatID: 407, PriceCoeff: 1.16, TitlePrefix: ""},
	{Name: "شارژر فندکی", EwaysCatID: "1609", WPCatID: 409, PriceCoeff: 1.2, TitlePrefix: ""},
	{Name: "کابل شارژر", EwaysCatID: "1587", WPCatID: 403, PriceCoeff: 1.12, TitlePrefix: ""},
	{Name: "محافظ کابل شارژر", EwaysCatID: "2675", WPCatID: 413, PriceCoeff: 1.15, TitlePrefix: ""},
	{Name: "تبدیل ها", EwaysCatID: "2685", WPCatID: 415, PriceCoeff: 1.08, TitlePrefix: ""},
	{Name: "هولدر و نگهدارنده موبایل", EwaysCatID: "2371", WPCatID: 411, PriceCoeff: 1.22, TitlePrefix: ""},

	// 7. ذخیره‌سازی
	{Name: "کارت حافظه و رم", EwaysCatID: "1396", WPCatID: 417, PriceCoeff: 1.2, TitlePrefix: "رم "},
	{Name: "فلش مموری", EwaysCatID: "1395", WPCatID: 418, PriceCoeff: 1.1, TitlePrefix: "فلش "},
	{Name: "هارد اکسترنال", EwaysCatID: "9426", WPCatID: 421, PriceCoeff: 1.2, TitlePrefix: ""},
	{Name: "هارد SSD", EwaysCatID: "9752", WPCatID: 423, PriceCoeff: 1.05, TitlePrefix: ""},

	// 8. تعمیرات موبایل
	{Name: "تاچ ال سی و تعمیرات موبایل", EwaysCatID: "17496", WPCatID: 363, PriceCoeff: 1.07, TitlePrefix: "تاچ ال سی دی "},

	// 9. کیف و کوله پشتی
	{Name: "کیف و کوله پشتی", EwaysCatID: "20761", WPCatID: 364, PriceCoeff: 1.04, TitlePrefix: ""},
}

// ================================================================
//  توابع اصلی
// ================================================================

func Load() *Config {
	cfg := &Config{
		Database: DatabaseConfig{
			URL: getEnv("DATABASE_URL", "postgres://scraper:scraper123@postgres:5432/scraper_sync?sslmode=disable"),
		},
		Scraper: ScraperConfig{
			BaseURL:      getEnv("SCRAPER_BASE_URL", "https://panel.eways.co/Store/ListLazy"),
			Timeout:      getEnvDuration("SCRAPER_TIMEOUT", 30*time.Second),
			RateLimit:    getEnvInt("SCRAPER_RATE_LIMIT", 2),
			RetryMax:     getEnvInt("SCRAPER_RETRY_MAX", 3),
			RetryBackoff: getEnvDuration("SCRAPER_RETRY_BACKOFF", 3*time.Second),
			PageSize:     24,
		},
		Woo: WooConfig{
			BaseURL:         getEnv("WOO_BASE_URL", "https://topguard.ir/wp-json/wc/v3"),
			ConsumerKey:     getEnv("WOO_CONSUMER_KEY", ""),
			ConsumerSecret:  getEnv("WOO_CONSUMER_SECRET", ""),
			Timeout:         getEnvDuration("WOO_TIMEOUT", 30*time.Second),
			RateLimit:       getEnvInt("WOO_RATE_LIMIT", 10),
			BatchCreateSize: getEnvInt("WOO_BATCH_CREATE_SIZE", 10),
			BatchUpdateSize: getEnvInt("WOO_BATCH_UPDATE_SIZE", 25),
		},
		Sync: SyncConfig{
			UpdateWorkerCount: getEnvInt("SYNC_UPDATE_WORKER_COUNT", 10),
			UpdateFetchLimit:  getEnvInt("SYNC_UPDATE_FETCH_LIMIT", 100),
			UpdateBatchSize:   getEnvInt("SYNC_UPDATE_BATCH_SIZE", 25),
			CreateWorkerCount: getEnvInt("SYNC_CREATE_WORKER_COUNT", 2),
			CreateFetchLimit:  getEnvInt("SYNC_CREATE_FETCH_LIMIT", 20),
			CreateBatchSize:   getEnvInt("SYNC_CREATE_BATCH_SIZE", 5),
			RetryBackoffBase:  getEnvDuration("SYNC_RETRY_BACKOFF_BASE", 30*time.Second),
			MaxRetries:        getEnvInt("SYNC_MAX_RETRIES", 3),
		},
		App: AppConfig{
			FixedCost:   getEnvFloat("FIXED_COST", 50000),
			RoundTo:     getEnvFloat("ROUND_TO", 1000),
			IsDryRun:    getEnvBool("IS_DRY_RUN", true),
			Description: ProductDescriptionHTML,
		},
		Eways: EwaysConfig{
			LoginURL: getEnv("EWAYS_LOGIN_URL", "https://panel.eways.co/User/Login"),
			Username: getEnv("EWAYS_USERNAME", ""),
			Password: getEnv("EWAYS_PASSWORD", ""),
		},
		HTTPPort: getEnv("HTTP_PORT", "8080"),
		LogLevel: getEnv("LOG_LEVEL", "info"),
	}

	// مقداردهی Categories با لیست پیش‌فرض
	Categories = defaultCategories

	// اگر تمایل به بارگذاری از دیتابیس دارید، این بخش فعال شود
	// اما فعلاً با لیست پیش‌فرض کار می‌کنیم
	_ = LoadFromDB(cfg.Database.URL) // خطا را نادیده می‌گیریم

	return cfg
}

// LoadFromDB سعی در بارگذاری از دیتابیس دارد (اختیاری)
func LoadFromDB(databaseURL string) error {
	// این بخش فعلاً غیرفعال است و فقط برای سازگاری نگهداری می‌شود
	return nil
}

// GetCategories بازگرداندن لیست دسته‌بندی‌ها
func GetCategories() []Category {
	mu.RLock()
	defer mu.RUnlock()
	return Categories
}

// ================================================================
//  توابع کمکی
// ================================================================

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		return v == "true" || v == "1" || v == "yes"
	}
	return fallback
}

// ================================================================
//  توضیحات ثابت محصول
// ================================================================

const ProductDescriptionHTML = `
<div style="direction: rtl; text-align: right; font-family: tahoma;">
	<h2 style="color: #2c3e50; border-bottom: 2px solid #e74c3c; padding-bottom: 10px;">چرا تاپ گارد انتخاب اول شماست؟</h2>
	<p>در <strong>تاپ گارد</strong>، ما فقط کالا نمی‌فروشیم؛ ما تجربه‌ای از یک خرید مطمئن را برای شما می‌سازیم.</p>
	
	<h3 style="color: #e67e22;">ضمانت سلامت و اصالت کالا</h3>
	<p>تمام محصولاتی که از تاپ گارد می‌خرید، با تضمین ۱۰۰٪ اصالت و سلامت فیزیکی عرضه می‌شوند. پیش از ارسال، تمامی کالاها از نظر کیفی بررسی می‌شوند.</p>
	
	<h3 style="color: #e67e22;">ارسال سریع به سراسر ایران</h3>
	<p>سفارش شما در کوتاه‌ترین زمان ممکن پردازش شده و از طریق پست یا تیپاکس به دست شما خواهد رسید. کد رهگیری مرسوله بلافاصله پس از ارسال برای شما پیامک می‌شود.</p>
	
	<h3 style="color: #e67e22;">پشتیبانی ویژه</h3>
	<p>تیم پشتیبانی ما در تمامی مراحل خرید، از انتخاب محصول تا پس از تحویل، در کنار شماست تا خریدی بی‌دغدغه داشته باشید.</p>
</div>
`