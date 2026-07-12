package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

type Category struct {
	Name            string  `json:"name"`
	EwaysCatID      string  `json:"ewaysCatID"`
	WPCatID         int     `json:"wpCatID"`
	PriceCoeff      float64 `json:"priceCoeff"`
	TitlePrefix     string  `json:"titlePrefix"`
	CoefficientType string  `json:"coefficientType"`
	FixedProfit     float64 `json:"fixedProfit"`
}

type Settings struct {
	FixedCost  float64    `json:"fixedCost"`
	RoundTo    float64    `json:"roundTo"`
	Categories []Category `json:"categories"`
}

var db *sql.DB

func main() {
	port := "8081"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}
	if port[0] != ':' {
		port = ":" + port
	}

	// ============================================================
	//  اتصال به PostgreSQL
	// ============================================================
	dsn := "postgres://scraper:scraper123@localhost:5432/scraper_sync?sslmode=disable"
	var err error
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal("❌ اتصال به دیتابیس ناموفق: ", err)
	}
	if err = db.Ping(); err != nil {
		log.Fatal("❌ پینگ دیتابیس ناموفق: ", err)
	}
	log.Println("✅ اتصال به دیتابیس PostgreSQL برقرار شد.")

	r := gin.Default()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	htmlPath := findIndexHTML()
	if htmlPath == "" {
		log.Fatal("❌ فایل index.html پیدا نشد!")
	}
	log.Printf("📄 فایل HTML در مسیر: %s", htmlPath)

	r.GET("/", func(c *gin.Context) {
		c.File(htmlPath)
	})

	r.GET("/api/settings", getSettings)
	r.PUT("/api/settings", updateSettings)
	r.POST("/api/reload", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "تنظیمات بارگذاری مجدد شد. (ری‌استارت ربات برای اعمال تغییرات)"})
	})

	log.Printf("🚀 پنل مدیریت روی http://localhost%s در حال اجراست...", port)
	log.Fatal(r.Run(port))
}

func findIndexHTML() string {
	paths := []string{
		"./static/index.html",
		"./index.html",
		filepath.Join(filepath.Dir(os.Args[0]), "static", "index.html"),
		filepath.Join(filepath.Dir(os.Args[0]), "index.html"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ============================================================
//  دریافت تنظیمات با خطای دقیق
// ============================================================
func getSettings(c *gin.Context) {
	var fixed, round float64

	// ۱. خواندن app_settings
	err := db.QueryRow("SELECT fixed_cost, round_to FROM app_settings WHERE id = 1").Scan(&fixed, &round)
	if err != nil {
		if err == sql.ErrNoRows {
			// اگر رکوردی نبود، پیش‌فرض برگردان
			c.JSON(200, Settings{
				FixedCost:  73000,
				RoundTo:    1000,
				Categories: []Category{},
			})
			return
		}
		c.JSON(500, gin.H{"error": "خطا در خواندن app_settings", "detail": err.Error()})
		return
	}

	// ۲. خواندن دسته‌بندی‌ها
	rows, err := db.Query(`
		SELECT name, eways_cat_id, wp_cat_id, price_coeff, title_prefix, coefficient_type, fixed_profit
		FROM categories ORDER BY id
	`)
	if err != nil {
		c.JSON(500, gin.H{"error": "خطا در خواندن categories", "detail": err.Error()})
		return
	}
	defer rows.Close()

	var categories []Category
	for rows.Next() {
		var cat Category
		var coeffType string
		var fixedProfit sql.NullFloat64 // 🔥 برای مدیریت NULL

		err := rows.Scan(
			&cat.Name,
			&cat.EwaysCatID,
			&cat.WPCatID,
			&cat.PriceCoeff,
			&cat.TitlePrefix,
			&coeffType,
			&fixedProfit,
		)
		if err != nil {
			c.JSON(500, gin.H{"error": "خطا در اسکن داده", "detail": err.Error()})
			return
		}
		cat.CoefficientType = coeffType
		if fixedProfit.Valid {
			cat.FixedProfit = fixedProfit.Float64
		} else {
			cat.FixedProfit = 0
		}
		categories = append(categories, cat)
	}

	if err = rows.Err(); err != nil {
		c.JSON(500, gin.H{"error": "خطا در پردازش ردیف‌ها", "detail": err.Error()})
		return
	}

	c.JSON(200, Settings{
		FixedCost:  fixed,
		RoundTo:    round,
		Categories: categories,
	})
}

// ============================================================
//  به‌روزرسانی تنظیمات (با مدیریت NULL)
// ============================================================
func updateSettings(c *gin.Context) {
	var req Settings
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "داده نامعتبر", "detail": err.Error()})
		return
	}

	tx, err := db.Begin()
	if err != nil {
		c.JSON(500, gin.H{"error": "خطا در شروع تراکنش"})
		return
	}
	defer tx.Rollback()

	// ۱. به‌روزرسانی app_settings
	_, err = tx.Exec(`
		INSERT INTO app_settings (id, fixed_cost, round_to)
		VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET
			fixed_cost = EXCLUDED.fixed_cost,
			round_to = EXCLUDED.round_to,
			updated_at = NOW()
	`, req.FixedCost, req.RoundTo)
	if err != nil {
		c.JSON(500, gin.H{"error": "خطا در ذخیره هزینه ثابت", "detail": err.Error()})
		return
	}

	// ۲. پاک کردن دسته‌بندی‌های قبلی
	_, err = tx.Exec("DELETE FROM categories")
	if err != nil {
		c.JSON(500, gin.H{"error": "خطا در پاک کردن دسته‌بندی‌ها"})
		return
	}

	// ۳. درج دسته‌بندی‌های جدید
	for _, cat := range req.Categories {
		_, err = tx.Exec(`
			INSERT INTO categories (
				name, eways_cat_id, wp_cat_id, price_coeff, title_prefix,
				coefficient_type, fixed_profit
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, cat.Name, cat.EwaysCatID, cat.WPCatID, cat.PriceCoeff,
			cat.TitlePrefix, cat.CoefficientType, cat.FixedProfit)
		if err != nil {
			c.JSON(500, gin.H{"error": "خطا در درج دسته: " + cat.Name, "detail": err.Error()})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(500, gin.H{"error": "خطا در ذخیره نهایی"})
		return
	}

	c.JSON(200, gin.H{"status": "✅ تنظیمات با موفقیت ذخیره شد"})
}