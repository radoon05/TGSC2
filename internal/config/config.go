package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Database   DatabaseConfig
	Scraper    ScraperConfig
	Woo        WooConfig
	Sync       SyncConfig
	HTTPPort   string
	LogLevel   string
}

type DatabaseConfig struct {
	URL string
}

type ScraperConfig struct {
	BaseURL      string
	Timeout      time.Duration
	RateLimit    int           // requests per second
	RetryMax     int
	RetryBackoff time.Duration
	PageSize     int
}

type WooConfig struct {
	BaseURL            string
	ConsumerKey        string
	ConsumerSecret     string
	Timeout            time.Duration
	RateLimit          int
	BatchCreateSize    int
	BatchUpdateSize    int
}

type SyncConfig struct {
	WorkerCount       int
	FetchLimit        int
	RetryBackoffBase  time.Duration
	MaxRetries        int
}

// Load reads configuration from environment variables
func Load() *Config {
	return &Config{
		Database: DatabaseConfig{
			URL: getEnv("DATABASE_URL", "postgres://user:pass@localhost:5432/tgsc?sslmode=disable"),
		},
		Scraper: ScraperConfig{
			BaseURL:      getEnv("SCRAPER_BASE_URL", "https://example.com/api/products"),
			Timeout:      getEnvDuration("SCRAPER_TIMEOUT", 30*time.Second),
			RateLimit:    getEnvInt("SCRAPER_RATE_LIMIT", 5),
			RetryMax:     getEnvInt("SCRAPER_RETRY_MAX", 3),
			RetryBackoff: getEnvDuration("SCRAPER_RETRY_BACKOFF", 2*time.Second),
			PageSize:     getEnvInt("SCRAPER_PAGE_SIZE", 50),
		},
		Woo: WooConfig{
			BaseURL:         getEnv("WOO_BASE_URL", "https://yourstore.com/wp-json/wc/v3"),
			ConsumerKey:     getEnv("WOO_CONSUMER_KEY", ""),
			ConsumerSecret:  getEnv("WOO_CONSUMER_SECRET", ""),
			Timeout:         getEnvDuration("WOO_TIMEOUT", 30*time.Second),
			RateLimit:       getEnvInt("WOO_RATE_LIMIT", 10),
			BatchCreateSize: getEnvInt("WOO_BATCH_CREATE_SIZE", 10),
			BatchUpdateSize: getEnvInt("WOO_BATCH_UPDATE_SIZE", 25),
		},
		Sync: SyncConfig{
			WorkerCount:      getEnvInt("SYNC_WORKER_COUNT", 5),
			FetchLimit:       getEnvInt("SYNC_FETCH_LIMIT", 100),
			RetryBackoffBase: getEnvDuration("SYNC_RETRY_BACKOFF_BASE", 30*time.Second),
			MaxRetries:       getEnvInt("SYNC_MAX_RETRIES", 3),
		},
		HTTPPort: getEnv("HTTP_PORT", "8080"),
		LogLevel: getEnv("LOG_LEVEL", "info"),
	}
}

// Helper functions
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if dur, err := time.ParseDuration(value); err == nil {
			return dur
		}
	}
	return fallback
}