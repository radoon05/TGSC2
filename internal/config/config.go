package config

import (
	"database/sql"
	"log"
	"os"
	"strconv"
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
	Categories []Category
	FixedCost  float64
	RoundTo    float64
	IsDryRun   bool
)

// ================================================================
//  توابع اصلی
// ================================================================

func Load() *Config {
	cfg := &Config{
		Database: DatabaseConfig{
			URL: getEnv("DATABASE_URL", "postgres://scraper:scraper123@localhost:5432/scraper_sync?sslmode=disable"),
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
			FixedCost:   getEnvFloat("FIXED_COST", 24000), // ← از دیتابیس شما
			RoundTo:     getEnvFloat("ROUND_TO", 1000),    // ← از دیتابیس شما
			IsDryRun:    getEnvBool("IS_DRY_RUN", false),
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

	// 🔥 فقط از دیتابیس بخوان
	if err := LoadFromDB(cfg.Database.URL); err != nil {
		log.Printf("⚠️ بارگذاری از دیتابیس ناموفق: %v (ادامه با پیش‌فرض)", err)
		// اگر دیتابیس خالی است، از fallback استفاده کن
		Categories = fallbackCategories()
		if err := SaveDefaultCategories(cfg.Database.URL); err != nil {
			log.Printf("⚠️ ذخیره پیش‌فرض در دیتابیس ناموفق: %v", err)
		}
	} else {
		log.Printf("✅ %d دسته‌بندی از دیتابیس بارگذاری شد.", len(Categories))
		log.Printf("✅ هزینه ثابت: %.0f تومان, گرد کردن: %.0f", FixedCost, RoundTo)
	}

	return cfg
}

// ================================================================
//  بارگذاری از دیتابیس
// ================================================================

func LoadFromDB(databaseURL string) error {
	conn, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := conn.Ping(); err != nil {
		return err
	}

	// ۱. خواندن fixed_cost و round_to
	var fixedCost, roundTo float64
	err = conn.QueryRow("SELECT fixed_cost, round_to FROM app_settings WHERE id = 1").Scan(&fixedCost, &roundTo)
	if err != nil {
		if err == sql.ErrNoRows {
			// اگر جدول خالی است، از پیش‌فرض استفاده کن
			fixedCost = 24000
			roundTo = 1000
			_, _ = conn.Exec(`
				INSERT INTO app_settings (id, fixed_cost, round_to)
				VALUES (1, $1, $2)
				ON CONFLICT (id) DO NOTHING
			`, fixedCost, roundTo)
		} else {
			return err
		}
	}
	FixedCost = fixedCost
	RoundTo = roundTo

	// ۲. خواندن دسته‌بندی‌ها (با EwaysCatIDهای اصلاح‌شده)
	rows, err := conn.Query(`
		SELECT name, eways_cat_id, wp_cat_id, price_coeff, title_prefix, coefficient_type, fixed_profit
		FROM categories ORDER BY id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var cats []Category
	for rows.Next() {
		var cat Category
		var fixedProfit sql.NullFloat64
		err := rows.Scan(&cat.Name, &cat.EwaysCatID, &cat.WPCatID, &cat.PriceCoeff,
			&cat.TitlePrefix, &cat.CoefficientType, &fixedProfit)
		if err != nil {
			return err
		}
		if fixedProfit.Valid {
			cat.FixedProfit = fixedProfit.Float64
		}
		cats = append(cats, cat)
	}
	Categories = cats
	return nil
}

// ================================================================
//  ذخیره دسته‌بندی‌های پیش‌فرض در دیتابیس (با CatIDهای اصلاح‌شده)
// ================================================================

func SaveDefaultCategories(databaseURL string) error {
	conn, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return err
	}
	defer conn.Close()

	// فقط اگر جدول خالی است، درج کن
	var count int
	if err := conn.QueryRow("SELECT COUNT(*) FROM categories").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	for _, cat := range fallbackCategories() {
		_, err := conn.Exec(`
			INSERT INTO categories (name, eways_cat_id, wp_cat_id, price_coeff, title_prefix, coefficient_type, fixed_profit)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, cat.Name, cat.EwaysCatID, cat.WPCatID, cat.PriceCoeff,
			cat.TitlePrefix, cat.CoefficientType, cat.FixedProfit)
		if err != nil {
			return err
		}
	}
	return nil
}

// ================================================================
//  لیست پیش‌فرض (با CatIDهای اصلاح‌شده) - فقط برای اولین اجرا
// ================================================================

func fallbackCategories() []Category {
	return []Category{
		// 1. قاب و کاور
		{Name: "قاب و کاور اپل", EwaysCatID: "19136", WPCatID: 365, PriceCoeff: 1.00, TitlePrefix: ""},
		{Name: "قاب و کاور سامسونگ", EwaysCatID: "19178", WPCatID: 367, PriceCoeff: 1.00, TitlePrefix: ""},
		{Name: "قاب و کاور شیائومی", EwaysCatID: "2470", WPCatID: 369, PriceCoeff: 1.00, TitlePrefix: ""},
		{Name: "قاب و کاور هوآوی", EwaysCatID: "1168", WPCatID: 371, PriceCoeff: 1.00, TitlePrefix: ""},

		// 2. محافظ صفحه نمایش (گلس) - 🔥 اصلاح CatIDهای تکراری
		{Name: "محافظ صفحه نمایش موبایل آیفون", EwaysCatID: "3353", WPCatID: 373, PriceCoeff: 1.30, TitlePrefix: "گلس و ", CoefficientType: "percent"},
		{Name: "محافظ صفحه نمایش موبایل سامسونگ", EwaysCatID: "3354", WPCatID: 375, PriceCoeff: 1.30, TitlePrefix: "گلس و ", CoefficientType: "percent"},
		{Name: "محافظ صفحه نمایش موبایل شیائومی", EwaysCatID: "3374", WPCatID: 377, PriceCoeff: 1.30, TitlePrefix: "گلس و ", CoefficientType: "percent"},
		{Name: "محافظ صفحه نمایش موبایل ریل می", EwaysCatID: "19667", WPCatID: 379, PriceCoeff: 1.30, TitlePrefix: "گلس و ", CoefficientType: "percent"},
		{Name: "محافظ صفحه نمایش موبایل هوآوی", EwaysCatID: "3355", WPCatID: 381, PriceCoeff: 1.30, TitlePrefix: "گلس و ", CoefficientType: "percent"},

		// 3. ساعت هوشمند
		{Name: "ساعت هوشمند", EwaysCatID: "14548", WPCatID: 357, PriceCoeff: 1.12, TitlePrefix: ""},
		{Name: "بند ساعت هوشمند", EwaysCatID: "9251", WPCatID: 385, PriceCoeff: 1.16, TitlePrefix: ""},

		// 4. لوازم صوتی
		{Name: "هدفون، هندزفری و هدست", EwaysCatID: "1593", WPCatID: 359, PriceCoeff: 1.10, TitlePrefix: ""},
		{Name: "هدفون", EwaysCatID: "2550", WPCatID: 395, PriceCoeff: 1.06, TitlePrefix: ""},
		{Name: "هندزفری باسیم", EwaysCatID: "2390", WPCatID: 397, PriceCoeff: 1.06, TitlePrefix: ""},
		{Name: "هندزفری گردنی", EwaysCatID: "12898", WPCatID: 399, PriceCoeff: 1.06, TitlePrefix: ""},
		{Name: "ایرفون و ایرپادز", EwaysCatID: "12896", WPCatID: 360, PriceCoeff: 1.08, TitlePrefix: ""},

		// 5. پاوربانک
		{Name: "پاوربانک ۱۰٬۰۰۰ میلی‌آمپر", EwaysCatID: "13091", WPCatID: 387, PriceCoeff: 1.30, TitlePrefix: ""},
		{Name: "پاوربانک ۲۰٬۰۰۰ میلی‌آمپر", EwaysCatID: "13092", WPCatID: 389, PriceCoeff: 1.30, TitlePrefix: ""},
		{Name: "پاوربانک ۳۰٬۰۰۰ میلی‌آمپر", EwaysCatID: "18058", WPCatID: 391, PriceCoeff: 1.30, TitlePrefix: ""},

		// 6. لوازم جانبی موبایل (⚠️ این دو هنوز CatID تکراری دارند)
		{Name: "آداپتور شارژر", EwaysCatID: "1585", WPCatID: 401, PriceCoeff: 1.16, TitlePrefix: ""},
		{Name: "شارژر گوشی", EwaysCatID: "1585", WPCatID: 407, PriceCoeff: 1.16, TitlePrefix: ""},
		{Name: "شارژر فندکی", EwaysCatID: "1609", WPCatID: 409, PriceCoeff: 1.20, TitlePrefix: ""},
		{Name: "کابل شارژر", EwaysCatID: "1587", WPCatID: 403, PriceCoeff: 1.12, TitlePrefix: ""},
		{Name: "محافظ کابل شارژر", EwaysCatID: "2675", WPCatID: 413, PriceCoeff: 1.15, TitlePrefix: ""},
		{Name: "تبدیل ها", EwaysCatID: "2685", WPCatID: 415, PriceCoeff: 1.08, TitlePrefix: ""},
		{Name: "هولدر و نگهدارنده موبایل", EwaysCatID: "2371", WPCatID: 411, PriceCoeff: 1.22, TitlePrefix: ""},

		// 7. ذخیره‌سازی
		{Name: "کارت حافظه و رم", EwaysCatID: "1396", WPCatID: 417, PriceCoeff: 1.20, TitlePrefix: "رم "},
		{Name: "فلش مموری", EwaysCatID: "1395", WPCatID: 418, PriceCoeff: 1.10, TitlePrefix: "فلش "},
		{Name: "هارد اکسترنال", EwaysCatID: "9426", WPCatID: 421, PriceCoeff: 1.20, TitlePrefix: ""},
		{Name: "هارد SSD", EwaysCatID: "9752", WPCatID: 423, PriceCoeff: 1.05, TitlePrefix: ""},

		// 8. تعمیرات موبایل
		{Name: "تاچ ال سی و تعمیرات موبایل", EwaysCatID: "17496", WPCatID: 363, PriceCoeff: 1.07, TitlePrefix: "تاچ ال سی دی "},

		// 9. کیف و کوله پشتی
		{Name: "کیف و کوله پشتی", EwaysCatID: "20761", WPCatID: 364, PriceCoeff: 1.04, TitlePrefix: ""},
	}
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

func GetCategories() []Category {
	return Categories
}

func GetFixedCost() float64 {
	return FixedCost
}

func GetRoundTo() float64 {
	return RoundTo
}

// ReloadSettings تنظیمات را از دیتابیس مجدداً بارگذاری می‌کند
func ReloadSettings(databaseURL string) error {
    return LoadFromDB(databaseURL)
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