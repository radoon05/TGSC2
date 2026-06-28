package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
    "path/filepath"
    "sort"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	// "github.com/pressly/goose/v3"

	"tgsc/internal/config"
	"tgsc/internal/logger"
)

func main() {
	// 1. Load .env file (optional)
	_ = godotenv.Load()

	// 2. Load configuration using the config package
	cfg := config.Load()

	// 3. Setup logger
	log := logger.New(cfg.LogLevel)
	slog.SetDefault(log.Logger)
	log.Info("starting scraper-sync service", "version", "0.1.0")

	// 4. Connect to database
	dbPool, err := pgxpool.New(context.Background(), cfg.Database.URL)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()
	log.Info("database connected")

	// 5. Run migrations (using goose with sql.DB)
	if err := runMigrations(cfg.Database.URL); err != nil {
		log.Error("migration failed", "error", err)
		os.Exit(1)
	}
	log.Info("migrations applied")

	// 6. Setup health check endpoint
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

	httpServer := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// 7. Start HTTP server in goroutine
	go func() {
		log.Info("starting health HTTP server", "port", cfg.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}()

	// 8. Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Error("http server shutdown error", "error", err)
	}
	log.Info("service stopped")
}

// runMigrations applies SQL migrations by executing .up.sql files in order.
func runMigrations(databaseURL string) error {
    // Open standard sql.DB
    db, err := sql.Open("pgx", databaseURL)
    if err != nil {
        return fmt.Errorf("open db for migrations: %w", err)
    }
    defer db.Close()

    // Read all .up.sql files from migrations directory
    files, err := filepath.Glob("migrations/*.up.sql")
    if err != nil {
        return fmt.Errorf("glob migrations: %w", err)
    }
    if len(files) == 0 {
        return nil
    }

    // Sort files by name (ensures correct order: 001, 002, ...)
    sort.Strings(files)

    for _, f := range files {
        content, err := os.ReadFile(f)
        if err != nil {
            return fmt.Errorf("read migration file %s: %w", f, err)
        }
        // Execute the SQL
        _, err = db.Exec(string(content))
        if err != nil {
            return fmt.Errorf("execute migration %s: %w", f, err)
        }
        fmt.Printf("Migration applied: %s\n", f)
    }
    return nil
}