package main

// ریختن محصولات ووکامرس در دیتابیس محلی

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/google/uuid"
)

type WooProduct struct {
	ID            int     `json:"id"`
	SKU           string  `json:"sku"`
	Name          string  `json:"name"`
	Price         string  `json:"price"`
	StockQuantity int     `json:"stock_quantity"`
	Images        []struct {
		Src string `json:"src"`
	} `json:"images"`
	Categories []struct {
		ID int `json:"id"`
	} `json:"categories"`
}

func main() {
	// ============================================================
	//  اتصال به PostgreSQL
	// ============================================================
	pgDSN := "postgres://scraper:scraper123@localhost:5432/scraper_sync?sslmode=disable"
	pgDB, err := sql.Open("pgx", pgDSN)
	if err != nil {
		log.Fatal("❌ اتصال به PostgreSQL:", err)
	}
	defer pgDB.Close()
	if err := pgDB.Ping(); err != nil {
		log.Fatal("❌ پینگ PostgreSQL:", err)
	}
	log.Println("✅ اتصال به PostgreSQL برقرار شد.")

	// ============================================================
	//  تنظیمات WooCommerce API
	// ============================================================
	baseURL := "https://topguard.ir/wp-json/wc/v3/products"
	consumerKey := "ck_26647f8101f401b65d57a55ca1ecaa2224a8cacc"
	consumerSecret := "cs_7c33790d4bceab3f65f1cdd340c8b5d569e9ef9e"

	// ============================================================
	//  بارگذاری دسته‌بندی‌ها
	// ============================================================
	categoryMap := make(map[int]float64)
	catRows, err := pgDB.Query(`
		SELECT wp_cat_id, price_coeff FROM categories
	`)
	if err != nil {
		log.Printf("⚠️ خطا در خواندن دسته‌بندی‌ها: %v (ادامه با پیش‌فرض 1.0)", err)
	} else {
		defer catRows.Close()
		for catRows.Next() {
			var wpCatID int
			var priceCoeff float64
			if err := catRows.Scan(&wpCatID, &priceCoeff); err == nil {
				categoryMap[wpCatID] = priceCoeff
			}
		}
	}
	log.Printf("📂 %d دسته‌بندی بارگذاری شد.", len(categoryMap))

	// ============================================================
	//  دریافت محصولات با retry و delay
	// ============================================================
	page := 219
	perPage := 100
	totalProducts := 0
	inserted := 0
	updated := 0

	// در صورت قطع اتصال، می‌توانید page را به شماره‌ی آخرین صفحه‌ی موفق تغییر دهید
	// (فعلاً از صفحه ۱ شروع می‌کنیم، اما می‌توانید صفحه ۲۲۰ را تنظیم کنید)

	for {
		var products []WooProduct
		var resp *http.Response
		var err error

		// تلاش مجدد (حداکثر ۳ بار)
		for attempt := 1; attempt <= 3; attempt++ {
			reqURL := fmt.Sprintf("%s?page=%d&per_page=%d", baseURL, page, perPage)
			req, reqErr := http.NewRequest("GET", reqURL, nil)
			if reqErr != nil {
				log.Fatal("❌ ساخت درخواست:", reqErr)
			}
			req.SetBasicAuth(consumerKey, consumerSecret)

			client := &http.Client{Timeout: 60 * time.Second}
			resp, err = client.Do(req)
			if err == nil && resp.StatusCode == http.StatusOK {
				break
			}
			if resp != nil {
				resp.Body.Close()
			}
			log.Printf("⚠️ تلاش %d برای صفحه %d ناموفق: %v", attempt, page, err)
			if attempt < 3 {
				time.Sleep(3 * time.Second) // تأخیر قبل از تلاش مجدد
			}
		}
		if err != nil {
			log.Printf("❌ خطا در دریافت صفحه %d: %v", page, err)
			log.Printf("💡 اسکریپت متوقف شد. برای ادامه از صفحه %d شروع کنید.", page)
			break
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			log.Printf("❌ خطا در ووکامرس: status=%d, body=%s", resp.StatusCode, string(body))
			log.Printf("💡 اسکریپت متوقف شد. برای ادامه از صفحه %d شروع کنید.", page)
			break
		}

		if err := json.NewDecoder(resp.Body).Decode(&products); err != nil {
			log.Printf("❌ خطا در decode JSON صفحه %d: %v", page, err)
			log.Printf("💡 اسکریپت متوقف شد. برای ادامه از صفحه %d شروع کنید.", page)
			break
		}

		if len(products) == 0 {
			log.Printf("✅ صفحه %d خالی است، پایان صفحه‌بندی.", page)
			break
		}

		log.Printf("📦 دریافت %d محصول از صفحه %d", len(products), page)

		for _, wp := range products {
			if wp.SKU == "" {
				log.Printf("⚠️ محصول %d (%s) بدون SKU، رد شد.", wp.ID, wp.Name)
				continue
			}

			price, _ := strconv.ParseFloat(wp.Price, 64)

			wpCatID := 0
			if len(wp.Categories) > 0 {
				wpCatID = wp.Categories[0].ID
			}
			priceCoeff := 1.0
			if coeff, ok := categoryMap[wpCatID]; ok {
				priceCoeff = coeff
			}

			imageURL := ""
			if len(wp.Images) > 0 {
				imageURL = wp.Images[0].Src
			}

			fingerprint := generateFingerprint(wp.Name, 0, wp.StockQuantity)

			galleryJSON := []byte("[]")
			attributesJSON := []byte("[]")

			query := `
				INSERT INTO products (
					id, source_id, title, price, source_price, stock, fingerprint,
					last_scraped_at, created_at, updated_at, woo_id, detail_fetched_at,
					wp_cat_id, price_coeff, image_url, eways_cat_id,
					full_description, attributes, gallery_images
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
				ON CONFLICT (source_id) DO UPDATE SET
					title = EXCLUDED.title,
					price = EXCLUDED.price,
					source_price = EXCLUDED.source_price,
					stock = EXCLUDED.stock,
					fingerprint = EXCLUDED.fingerprint,
					last_scraped_at = EXCLUDED.last_scraped_at,
					updated_at = NOW(),
					woo_id = COALESCE(products.woo_id, EXCLUDED.woo_id),
					wp_cat_id = EXCLUDED.wp_cat_id,
					price_coeff = EXCLUDED.price_coeff,
					image_url = EXCLUDED.image_url,
					detail_fetched_at = NULL
			`

			var existingID string
			checkQuery := `SELECT id FROM products WHERE source_id = $1`
			err = pgDB.QueryRow(checkQuery, wp.SKU).Scan(&existingID)
			isUpdate := err == nil

			_, err = pgDB.Exec(query,
				uuid.New().String(),
				wp.SKU,
				wp.Name,
				0,
				price,
				wp.StockQuantity,
				fingerprint,
				time.Now(),
				time.Now(),
				time.Now(),
				wp.ID,
				nil,
				wpCatID,
				priceCoeff,
				imageURL,
				"",
				"",
				attributesJSON,
				galleryJSON,
			)
			if err != nil {
				log.Printf("❌ خطا در درج محصول %s: %v", wp.SKU, err)
			} else {
				if isUpdate {
					updated++
				} else {
					inserted++
				}
			}
		}

		totalProducts += len(products)
		page++

		// ⏱️ تأخیر ۵۰۰ میلی‌ثانیه بین درخواست‌ها
		time.Sleep(500 * time.Millisecond)
	}

	log.Printf("✅ عملیات سینک به پایان رسید.")
	log.Printf("📊 جمعاً %d محصول از ووکامرس دریافت شد.", totalProducts)
	log.Printf("📥 %d محصول جدید درج شد.", inserted)
	log.Printf("🔄 %d محصول به‌روزرسانی شد.", updated)
	log.Printf("💡 آخرین صفحه پردازش‌شده: %d", page-1)
	log.Printf("   برای ادامه از صفحه %d شروع کنید (اگر متوقف شد).", page)
}

func generateFingerprint(title string, price float64, stock int) string {
	data := fmt.Sprintf("%s|%.2f|%d", title, price, stock)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}