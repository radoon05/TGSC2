package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for the application
type Metrics struct {
	// Scraper metrics
	ScrapeTotal          *prometheus.CounterVec
	ScrapeDuration       prometheus.Histogram
	ScrapeProductsTotal  prometheus.Counter
	ScrapeErrorsTotal    *prometheus.CounterVec

	// Change detection metrics
	ProductsChangedTotal *prometheus.CounterVec // labels: new, dirty, unchanged

	// Sync job metrics
	SyncJobsTotal        *prometheus.CounterVec   // labels: job_type, state
	SyncJobsRetries      *prometheus.CounterVec   // labels: job_type
	SyncJobDuration      *prometheus.HistogramVec // labels: job_type
	SyncQueueLength      prometheus.Gauge

	// WooCommerce client metrics
	WooRequestsTotal     *prometheus.CounterVec   // labels: method, status_code
	WooBatchSize         prometheus.Histogram
	WooPartialSuccesses  *prometheus.CounterVec   // labels: operation

	// Database metrics
	DBQueryDuration      *prometheus.HistogramVec // labels: query
	DBErrorsTotal        *prometheus.CounterVec   // labels: operation

	// System metrics
	WorkerActiveCount    prometheus.Gauge
	JobFetchCount        *prometheus.CounterVec   // labels: result
	DeadLetterTotal      *prometheus.CounterVec   // labels: job_type
}

// NewMetrics initializes all Prometheus metrics
func NewMetrics() *Metrics {
	m := &Metrics{
		ScrapeTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "scraper_runs_total",
			Help: "Total number of scraper runs",
		}, []string{"result"}),

		ScrapeDuration: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "scraper_duration_seconds",
			Help:    "Duration of scraper runs",
			Buckets: prometheus.DefBuckets,
		}),

		ScrapeProductsTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "scraper_products_total",
			Help: "Total number of products scraped",
		}),

		ScrapeErrorsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "scraper_errors_total",
			Help: "Total number of scraper errors by type",
		}, []string{"error_type"}),

		ProductsChangedTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "products_changed_total",
			Help: "Number of products marked as NEW, DIRTY, or UNCHANGED",
		}, []string{"change_type"}),

		SyncJobsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "sync_jobs_total",
			Help: "Total sync jobs created by type and final state",
		}, []string{"job_type", "state"}),

		SyncJobsRetries: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "sync_job_retries_total",
			Help: "Number of retries attempted per job type",
		}, []string{"job_type"}),

		SyncJobDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sync_job_duration_seconds",
			Help:    "Duration of sync job processing",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
		}, []string{"job_type"}),

		SyncQueueLength: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "sync_queue_length",
			Help: "Current number of pending sync jobs",
		}),

		WooRequestsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "woo_requests_total",
			Help: "Total WooCommerce API requests",
		}, []string{"method", "status_code"}),

		WooBatchSize: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "woo_batch_size",
			Help:    "Number of items in batch requests",
			Buckets: []float64{1, 2, 5, 10, 15, 20, 25, 50},
		}),

		WooPartialSuccesses: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "woo_partial_successes_total",
			Help: "Partial success occurrences in batch operations",
		}, []string{"operation"}),

		DBQueryDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Database query duration",
			Buckets: prometheus.DefBuckets,
		}, []string{"query"}),

		DBErrorsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "db_errors_total",
			Help: "Total database errors by operation",
		}, []string{"operation"}),

		WorkerActiveCount: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "worker_active_count",
			Help: "Number of currently active sync workers",
		}),

		JobFetchCount: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "job_fetch_total",
			Help: "Number of job fetch attempts and results",
		}, []string{"result"}),

		DeadLetterTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "dead_letter_total",
			Help: "Jobs moved to dead letter queue",
		}, []string{"job_type"}),
	}

	return m
}