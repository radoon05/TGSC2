package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"tgsc/internal/config"
	"tgsc/internal/logger"
	"tgsc/internal/repository"
	"tgsc/internal/scraper"
	"tgsc/internal/sync"
	"tgsc/internal/woo"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	log := logger.New(cfg.LogLevel)
	slog.SetDefault(log.Logger)

	log.Info("starting scraper-sync service (dual-worker architecture)")

	// Connect to database
	dbPool, err := pgxpool.New(context.Background(), cfg.Database.URL)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()
	log.Info("database connected")

	// Run migrations
	if err := runMigrations(cfg.Database.URL); err != nil {
		log.Error("migration failed", "error", err)
		os.Exit(1)
	}
	log.Info("migrations applied")

	// Create repositories
	productRepo := repository.NewProductRepository(dbPool)
	syncJobRepo := repository.NewSyncJobRepository(dbPool)

	// Create scraper with login credentials
	categories := config.GetCategories()
	scraperClient := scraper.NewClient(
		&cfg.Scraper,
		cfg.Eways.LoginURL,
		cfg.Eways.Username,
		cfg.Eways.Password,
		categories,
	)

	// Create Woo client
	wooClient := woo.NewClient(
		cfg.Woo.BaseURL,
		cfg.Woo.ConsumerKey,
		cfg.Woo.ConsumerSecret,
		cfg.Woo.Timeout,
		cfg.Woo.RateLimit,
		cfg.Woo.BatchCreateSize,
		cfg.Woo.BatchUpdateSize,
		cfg.App.IsDryRun,
		cfg.App.FixedCost,
		cfg.App.RoundTo,
	)

	// Create normalizer and change detector
	normalizer := sync.NewNormalizer()
	changeDetector := sync.NewChangeDetector(productRepo, syncJobRepo, normalizer)

	// Create engine configuration
	engineCfg := &sync.EngineConfig{
		UpdateWorkerCount: cfg.Sync.UpdateWorkerCount,
		UpdateFetchLimit:  cfg.Sync.UpdateFetchLimit,
		UpdateBatchSize:   cfg.Sync.UpdateBatchSize,
		CreateWorkerCount: cfg.Sync.CreateWorkerCount,
		CreateFetchLimit:  cfg.Sync.CreateFetchLimit,
		CreateBatchSize:   cfg.Sync.CreateBatchSize,
		RetryBackoffBase:  cfg.Sync.RetryBackoffBase,
		MaxRetries:        cfg.Sync.MaxRetries,
	}

	// Create engine with separate worker pools
	engine := sync.NewEngine(syncJobRepo, productRepo, wooClient, log, engineCfg)
	ctx, cancel := context.WithCancel(context.Background())
	engine.Start(ctx)

	// ============================================================
	// 🔥 Schedulerها با ارسال scraperClient برای دریافت جزئیات
	// ============================================================

	// Scheduler 1: Update (every 2 hours)
	updateScraperScheduler := sync.NewScraperScheduler(
		scraperClient,
		changeDetector,
		log.Named("update-scraper"),
		2*time.Hour,
	)
	updateScraperScheduler.Start(ctx)

	// Scheduler 2: Create (every 12 hours)
	createScraperScheduler := sync.NewScraperScheduler(
		scraperClient,
		changeDetector,
		log.Named("create-scraper"),
		4*time.Hour,
	)
	createScraperScheduler.Start(ctx)

	// ============================================================
	//  HTTP Handlers
	// ============================================================

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := dbPool.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status": "down", "error": "%s"}`, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status": "ok"}`)
	})

	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "# Metrics endpoint\n")
	})

	mux.HandleFunc("POST /debug-raw", func(w http.ResponseWriter, r *http.Request) {
		log := logger.New(cfg.LogLevel).Named("debug")
		log.Info("debug raw response triggered")

		testCategories := []config.Category{
			{Name: "قاب و کاور اپل", EwaysCatID: "19136", WPCatID: 365, PriceCoeff: 1.10},
		}
		testScraper := scraper.NewClient(
			&cfg.Scraper,
			cfg.Eways.LoginURL,
			cfg.Eways.Username,
			cfg.Eways.Password,
			testCategories,
		)

		ctx := r.Context()
		if err := testScraper.Login(ctx); err != nil {
			log.Error("login failed", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error": "login failed: %s"}`, err.Error())
			return
		}

		payload := fmt.Sprintf(
			"ListViewType=0&CatId=%s&Order=2&Sort=2&LazyPageIndex=0&PageIndex=0&PageSize=24&Available=1&IsLazyLoading=true",
			"19136",
		)
		rawBody, err := testScraper.FetchRaw(ctx, payload)
		if err != nil {
			log.Error("fetch raw failed", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error": "%s"}`, err.Error())
			return
		}

		log.Info("raw response", "body", string(rawBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status": "ok", "raw": %s}`, string(rawBody))
	})

	mux.HandleFunc("POST /run-scrape", func(w http.ResponseWriter, r *http.Request) {
		log := logger.New(cfg.LogLevel).Named("manual")
		log.Info("manual scrape triggered")

		products, err := scraperClient.FetchProducts(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error": "%s"}`, err.Error())
			return
		}
		// 🔥 ارسال scraperClient برای دریافت جزئیات
		if err := changeDetector.ProcessScrapedProducts(r.Context(), products, scraperClient); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error": "%s"}`, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status": "ok", "products": %d}`, len(products))
	})

	mux.HandleFunc("GET /export-products", func(w http.ResponseWriter, r *http.Request) {
		log := logger.New(cfg.LogLevel).Named("export")
		log.Info("export products requested")

		rows, err := dbPool.Query(r.Context(), `
			SELECT source_id, title, price, stock, fingerprint, last_scraped_at 
			FROM products 
			ORDER BY created_at DESC
		`)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "خطا در خواندن دیتابیس: %v", err)
			return
		}
		defer rows.Close()

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=products_export.txt")

		fmt.Fprintf(w, "=== گزارش محصولات تاپ گارد ===\n")
		fmt.Fprintf(w, "تاریخ: %s\n", time.Now().Format("2006-01-02 15:04:05"))
		fmt.Fprintf(w, "================================\n\n")
		fmt.Fprintf(w, "%-10s | %-50s | %-12s | %-6s | %-20s\n", "SourceID", "عنوان", "قیمت (تومان)", "موجودی", "آخرین اسکرپ")
		fmt.Fprintf(w, "-----------|----------------------------------------------------|--------------|--------|----------------------\n")

		var count int
		for rows.Next() {
			var sourceID, title, fingerprint string
			var price float64
			var stock int
			var lastScrapedAt time.Time
			err := rows.Scan(&sourceID, &title, &price, &stock, &fingerprint, &lastScrapedAt)
			if err != nil {
				continue
			}
			count++
			displayTitle := title
			if len(displayTitle) > 50 {
				displayTitle = displayTitle[:50] + "..."
			}
			fmt.Fprintf(w, "%-10s | %-50s | %12.0f | %6d | %s\n",
				sourceID, displayTitle, price, stock, lastScrapedAt.Format("2006-01-02 15:04"))
		}

		fmt.Fprintf(w, "\n================================\n")
		fmt.Fprintf(w, "تعداد کل محصولات: %d\n", count)
		log.Info("export completed", "count", count)
	})

	mux.HandleFunc("POST /test-scrape", func(w http.ResponseWriter, r *http.Request) {
		log := logger.New(cfg.LogLevel).Named("test")
		log.Info("test scrape triggered - running in background")

		testCategories := []config.Category{
			{Name: "test cat", EwaysCatID: "18482", WPCatID: 38, PriceCoeff: 1.2},
		}
		testScraper := scraper.NewClient(
			&cfg.Scraper,
			cfg.Eways.LoginURL,
			cfg.Eways.Username,
			cfg.Eways.Password,
			testCategories,
		)

		go func() {
			ctx := context.Background()
			products, err := testScraper.FetchProducts(ctx)
			if err != nil {
				log.Error("test scrape failed", "error", err)
				return
			}
			log.Info("test scrape completed", "product_count", len(products))
			if len(products) == 0 {
				return
			}
			// 🔥 ارسال scraperClient برای دریافت جزئیات
			if err := changeDetector.ProcessScrapedProducts(ctx, products, testScraper); err != nil {
				log.Error("test change detection failed", "error", err)
			} else {
				log.Info("test change detection completed")
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "accepted",
			"message": "test scrape started in background, check logs for results",
		})
	})

	// ============================================================
	//  HTTP Server
	// ============================================================

	httpServer := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Info("starting HTTP server", "port", cfg.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}()

	// ============================================================
	//  Graceful Shutdown
	// ============================================================

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down gracefully...")

	cancel()
	engine.Stop()
	updateScraperScheduler.Stop()
	createScraperScheduler.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("http server shutdown error", "error", err)
	}
	log.Info("service stopped")
}

// runMigrations applies SQL migrations by executing .up.sql files in order.
func runMigrations(databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open db for migrations: %w", err)
	}
	defer db.Close()

	var exists bool
	err = db.QueryRow("SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'products')").Scan(&exists)
	if err != nil {
		return fmt.Errorf("check table existence: %w", err)
	}
	if exists {
		log.Println("✅ جداول قبلاً ایجاد شده‌اند، از migration صرف‌نظر می‌شود.")
		return nil
	}

	files, err := filepath.Glob("migrations/*.up.sql")
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(files)

	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read migration file %s: %w", f, err)
		}
		if _, err := db.Exec(string(content)); err != nil {
			return fmt.Errorf("execute migration %s: %w", f, err)
		}
		log.Printf("Migration applied: %s", f)
	}
	return nil
}