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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"tgsc/internal/config"
	"tgsc/internal/logger"
	"tgsc/internal/repository"
	"tgsc/internal/scraper"
	syncpkg "tgsc/internal/sync" // 🔥 alias برای جلوگیری از تداخل با پکیج sync
	"tgsc/internal/woo"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	log := logger.New(cfg.LogLevel)
	slog.SetDefault(log.Logger)

	log.Info("starting scraper-sync service (improved architecture)")

	// ============================================================
	//  دیتابیس
	// ============================================================
	dbPool, err := pgxpool.New(context.Background(), cfg.Database.URL)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()
	log.Info("database connected")

	if err := runMigrations(cfg.Database.URL); err != nil {
		log.Error("migration failed", "error", err)
		os.Exit(1)
	}
	log.Info("migrations applied")

	// ============================================================
	//  Repository‌ها
	// ============================================================
	productRepo := repository.NewProductRepository(dbPool)
	syncJobRepo := repository.NewSyncJobRepository(dbPool)

	// ============================================================
	//  Scraper (با مدیریت خطا)
	// ============================================================
	categories := config.GetCategories()
	scraperClient, err := scraper.NewClient(
		&cfg.Scraper,
		cfg.Eways.LoginURL,
		cfg.Eways.Username,
		cfg.Eways.Password,
		categories,
	)
	if err != nil {
		log.Error("failed to create scraper client", "error", err)
		os.Exit(1)
	}
	log.Info("scraper client created")

	// ============================================================
	//  Woo Client
	// ============================================================
	wooClient := woo.NewClient(
		cfg.Woo.BaseURL,
		cfg.Woo.ConsumerKey,
		cfg.Woo.ConsumerSecret,
		cfg.Woo.Timeout,
		cfg.Woo.RateLimit,
		cfg.App.IsDryRun,
	)
	log.Info("woo client created", "dry_run", cfg.App.IsDryRun)

	// ============================================================
	//  Normalizer
	// ============================================================
	normalizer := syncpkg.NewNormalizer(cfg.App.FixedCost, cfg.App.RoundTo)

	// ============================================================
	//  Change Detector
	// ============================================================
	// در ساخت ChangeDetector، dbPool را هم پاس دهید:
	changeDetector := syncpkg.NewChangeDetector(
		productRepo,
		syncJobRepo,
		normalizer,
		categories,
		dbPool, // 🔥 اضافه شد
	)

	// ============================================================
	//  Engine
	// ============================================================
	engineCfg := &syncpkg.EngineConfig{
		UpdateWorkerCount: cfg.Sync.UpdateWorkerCount,
		UpdateFetchLimit:  cfg.Sync.UpdateFetchLimit,
		UpdateBatchSize:   cfg.Sync.UpdateBatchSize,
		CreateWorkerCount: cfg.Sync.CreateWorkerCount,
		CreateFetchLimit:  cfg.Sync.CreateFetchLimit,
		CreateBatchSize:   cfg.Sync.CreateBatchSize,
		RetryBackoffBase:  cfg.Sync.RetryBackoffBase,
		MaxRetries:        cfg.Sync.MaxRetries,
		DryRun:            cfg.App.IsDryRun,
	}

	engine := syncpkg.NewEngine(syncJobRepo, productRepo, wooClient, log, engineCfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !cfg.App.IsDryRun {
		engine.Start(ctx)
		log.Info("engine started (real mode)")
	} else {
		log.Info("engine NOT started (dry-run mode)")
	}

	// ============================================================
	//  Scheduler واحد
	// ============================================================
	var isScraping bool
	var scrapeMutex sync.Mutex
	var isManualScraping bool
	var manualScrapeMutex sync.Mutex

	scrapeFunc := func() {
		scrapeMutex.Lock()
		if isScraping {
			scrapeMutex.Unlock()
			log.Info("scrape already running, skipping")
			return
		}
		isScraping = true
		scrapeMutex.Unlock()

		defer func() {
			scrapeMutex.Lock()
			isScraping = false
			scrapeMutex.Unlock()
		}()

		// ============================================================
		// 🔥 بارگذاری مجدد تنظیمات از دیتابیس قبل از هر اسکرپ
		// ============================================================
		if err := config.ReloadSettings(cfg.Database.URL); err != nil {
			log.Error("failed to reload settings from database", "error", err)
			// ادامه با تنظیمات قدیمی (خطا را نادیده می‌گیریم)
		} else {
			// به‌روزرسانی Categories در اسکرپر کلاینت
			newCategories := config.GetCategories()
			scraperClient.UpdateCategories(newCategories)

			// به‌روزرسانی FixedCost و RoundTo در Normalizer
			normalizer.SetFixedCost(config.GetFixedCost())
			normalizer.SetRoundTo(config.GetRoundTo())

			log.Info("settings reloaded from database")
		}

		log.Info("scrape started")
		products, err := scraperClient.FetchProducts(ctx)
		if err != nil {
			log.Error("scrape failed", "error", err)
			return
		}
		log.Info("scrape completed", "product_count", len(products))
		if len(products) == 0 {
			return
		}
		if err := changeDetector.ProcessScrapedProducts(ctx, products, scraperClient); err != nil {
			log.Error("change detection failed", "error", err)
		} else {
			log.Info("change detection completed")
		}
	}

	scraperScheduler := NewScraperScheduler(scrapeFunc, 2*time.Hour, log.Named("scheduler"))
	scraperScheduler.Start(ctx)

	go func() {
		time.Sleep(5 * time.Second)
		scrapeFunc()
	}()

	// ============================================================
	//  HTTP Handlers
	// ============================================================
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := dbPool.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "down", "error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Metrics
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "# Metrics endpoint\n")
	})

	// Debug raw
	mux.HandleFunc("POST /debug-raw", func(w http.ResponseWriter, r *http.Request) {
		log := logger.New(cfg.LogLevel).Named("debug")
		log.Info("debug raw response triggered")

		testCategories := []config.Category{
			{Name: "قاب و کاور اپل", EwaysCatID: "19136", WPCatID: 365, PriceCoeff: 1.10},
		}
		testScraper, err := scraper.NewClient(
			&cfg.Scraper,
			cfg.Eways.LoginURL,
			cfg.Eways.Username,
			cfg.Eways.Password,
			testCategories,
		)
		if err != nil {
			log.Error("failed to create test scraper", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to create scraper: " + err.Error()})
			return
		}

		ctx := r.Context()
		if err := testScraper.Login(ctx); err != nil {
			log.Error("login failed", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "login failed: " + err.Error()})
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
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		log.Info("raw response", "body", string(rawBody))
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"raw":    string(rawBody),
		})
	})

	// Run full scrape
	mux.HandleFunc("POST /run-scrape", func(w http.ResponseWriter, r *http.Request) {
		manualScrapeMutex.Lock()
		if isManualScraping {
			manualScrapeMutex.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "conflict",
				"message": "manual scrape already running",
			})
			return
		}
		isManualScraping = true
		manualScrapeMutex.Unlock()

		go func() {
			defer func() {
				manualScrapeMutex.Lock()
				isManualScraping = false
				manualScrapeMutex.Unlock()
			}()

			log := logger.New(cfg.LogLevel).Named("manual")
			log.Info("manual full scrape triggered")

			// Refresh session
			if err := scraperClient.RefreshSession(ctx); err != nil {
				log.Error("refresh session failed", "error", err)
				return
			}

			products, err := scraperClient.FetchProducts(ctx)
			if err != nil {
				log.Error("scrape failed", "error", err)
				return
			}
			log.Info("scrape completed", "product_count", len(products))
			if len(products) == 0 {
				return
			}
			if err := changeDetector.ProcessScrapedProducts(ctx, products, scraperClient); err != nil {
				log.Error("change detection failed", "error", err)
			} else {
				log.Info("change detection completed")
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "accepted",
			"message": "full scrape started in background, check logs for results",
		})
	})

	// Export products
	mux.HandleFunc("GET /backup-db", func(w http.ResponseWriter, r *http.Request) {
		log := logger.New(cfg.LogLevel).Named("backup")
		log.Info("database backup requested")

		// تنظیم هدر برای دانلود فایل SQL
		filename := fmt.Sprintf("backup_%s.sql", time.Now().Format("2006-01-02_15-04-05"))
		w.Header().Set("Content-Type", "application/sql; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))

		// شروع نوشتن فایل SQL
		fmt.Fprintf(w, "-- ============================================================\n")
		fmt.Fprintf(w, "--  بکاپ کامل از دیتابیس scraper_sync\n")
		fmt.Fprintf(w, "--  تاریخ: %s\n", time.Now().Format("2006-01-02 15:04:05"))
		fmt.Fprintf(w, "-- ============================================================\n\n")

		// ۱. گرفتن لیست همه جداول
		rows, err := dbPool.Query(r.Context(), `
        SELECT table_name 
        FROM information_schema.tables 
        WHERE table_schema = 'public' 
        AND table_type = 'BASE TABLE'
        ORDER BY table_name
    `)
		if err != nil {
			log.Error("failed to get tables", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "خطا در خواندن جداول: %v", err)
			return
		}
		defer rows.Close()

		var tables []string
		for rows.Next() {
			var tableName string
			if err := rows.Scan(&tableName); err != nil {
				continue
			}
			tables = append(tables, tableName)
		}

		log.Info("found tables", "count", len(tables))

		// ۲. برای هر جدول، ساختار و داده‌ها را استخراج کن
		for _, tableName := range tables {
			// ۲.۱. دریافت ستون‌های جدول
			colRows, err := dbPool.Query(r.Context(), `
            SELECT column_name, data_type, is_nullable
            FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = $1
            ORDER BY ordinal_position
        `, tableName)
			if err != nil {
				log.Error("failed to get columns", "table", tableName, "error", err)
				continue
			}

			var columns []string
			var columnTypes []string
			var nullable []string
			for colRows.Next() {
				var colName, dataType, isNullable string
				if err := colRows.Scan(&colName, &dataType, &isNullable); err != nil {
					continue
				}
				columns = append(columns, colName)
				columnTypes = append(columnTypes, dataType)
				nullable = append(nullable, isNullable)
			}
			colRows.Close()

			if len(columns) == 0 {
				continue
			}

			// ۲.۲. نوشتن دستور DELETE (برای پاک کردن داده‌های قبلی در صورت بازیابی)
			fmt.Fprintf(w, "-- ============================================================\n")
			fmt.Fprintf(w, "--  جدول: %s\n", tableName)
			fmt.Fprintf(w, "-- ============================================================\n")
			fmt.Fprintf(w, "DELETE FROM %s;\n\n", tableName)

			// ۲.۳. ساخت کوئری SELECT
			selectQuery := fmt.Sprintf("SELECT %s FROM %s", strings.Join(columns, ", "), tableName)
			dataRows, err := dbPool.Query(r.Context(), selectQuery)
			if err != nil {
				log.Error("failed to query table", "table", tableName, "error", err)
				continue
			}

			// ۲.۴. ساخت placeholder برای مقادیر
			placeholders := make([]string, len(columns))
			for i := range columns {
				placeholders[i] = fmt.Sprintf("$%d", i+1)
			}
			insertTemplate := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);\n",
				tableName,
				strings.Join(columns, ", "),
				strings.Join(placeholders, ", "),
			)

			// ۲.۵. خواندن و نوشتن داده‌ها (با استفاده از اسکن پویا)
			var count int
			for dataRows.Next() {
				// ساخت slice از interface{} برای اسکن
				values := make([]interface{}, len(columns))
				valuePtrs := make([]interface{}, len(columns))
				for i := range values {
					valuePtrs[i] = &values[i]
				}

				if err := dataRows.Scan(valuePtrs...); err != nil {
					log.Error("failed to scan row", "table", tableName, "error", err)
					continue
				}

				// تبدیل مقادیر به رشته برای SQL
				rowValues := make([]string, len(columns))
				for i, v := range values {
					if v == nil {
						rowValues[i] = "NULL"
					} else {
						switch val := v.(type) {
						case string:
							// escape کردن single quote
							rowValues[i] = fmt.Sprintf("'%s'", strings.ReplaceAll(val, "'", "''"))
						case int64, int32, int:
							rowValues[i] = fmt.Sprintf("%d", val)
						case float64, float32:
							rowValues[i] = fmt.Sprintf("%f", val)
						case bool:
							if val {
								rowValues[i] = "true"
							} else {
								rowValues[i] = "false"
							}
						case time.Time:
							rowValues[i] = fmt.Sprintf("'%s'", val.Format("2006-01-02 15:04:05.999999-07:00"))
						case []byte:
							// برای JSONB و bytea
							rowValues[i] = fmt.Sprintf("'%s'", string(val))
						default:
							rowValues[i] = fmt.Sprintf("'%v'", val)
						}
					}
				}

				// نوشتن INSERT
				fmt.Fprintf(w, insertTemplate, argsToInterface(rowValues)...)
				count++
			}
			dataRows.Close()

			fmt.Fprintf(w, "\n-- %d رکورد در جدول %s\n\n", count, tableName)
			log.Info("table backed up", "table", tableName, "count", count)
		}

		log.Info("database backup completed")
	})

	// Test scrape
	mux.HandleFunc("POST /test-scrape", func(w http.ResponseWriter, r *http.Request) {
		log := logger.New(cfg.LogLevel).Named("test")
		log.Info("test scrape triggered - running in background")

		testCategories := []config.Category{
			{Name: "test cat", EwaysCatID: "18482", WPCatID: 3047, PriceCoeff: 1.2},
		}
		testScraper, err := scraper.NewClient(
			&cfg.Scraper,
			cfg.Eways.LoginURL,
			cfg.Eways.Username,
			cfg.Eways.Password,
			testCategories,
		)
		if err != nil {
			log.Error("failed to create test scraper", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to create scraper: " + err.Error()})
			return
		}

		go func() {
			products, err := testScraper.FetchProducts(ctx)
			if err != nil {
				log.Error("test scrape failed", "error", err)
				return
			}
			log.Info("test scrape completed", "product_count", len(products))
			if len(products) == 0 {
				return
			}
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
		WriteTimeout: 60 * time.Second,
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

	if !cfg.App.IsDryRun {
		engine.Stop()
	}
	scraperScheduler.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("http server shutdown error", "error", err)
	}
	log.Info("service stopped")
}

// ============================================================
//  ScraperScheduler
// ============================================================

type ScraperScheduler struct {
	fn       func()
	interval time.Duration
	logger   *logger.Logger
	stopChan chan struct{}
	running  bool
	mu       sync.Mutex
}

func NewScraperScheduler(fn func(), interval time.Duration, log *logger.Logger) *ScraperScheduler {
	return &ScraperScheduler{
		fn:       fn,
		interval: interval,
		logger:   log,
		stopChan: make(chan struct{}),
	}
}

func (s *ScraperScheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	go s.run(ctx)
}

func (s *ScraperScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	select {
	case <-s.stopChan:
	default:
		close(s.stopChan)
	}
}

func (s *ScraperScheduler) run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.logger.Info("scheduler started", "interval", s.interval)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopped due to context cancellation")
			return
		case <-s.stopChan:
			s.logger.Info("scheduler stopped by stop signal")
			return
		case <-ticker.C:
			s.fn()
		}
	}
}

// ============================================================
//  runMigrations
// ============================================================

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

// helper function: تبدیل []string به []interface{} برای استفاده در fmt.Sprintf
func argsToInterface(args []string) []interface{} {
	result := make([]interface{}, len(args))
	for i, v := range args {
		result[i] = v
	}
	return result
}
