package domain

import (
	"time"
)

// SyncJob represents a job to sync a product to WooCommerce
type SyncJob struct {
	ID          string     `json:"id" db:"id"`
	ProductID   string     `json:"product_id" db:"product_id"`
	JobType     string     `json:"job_type" db:"job_type"` // "create" or "update"
	State       SyncState  `json:"state" db:"state"`
	RetryCount  int        `json:"retry_count" db:"retry_count"`
	LastError   *string    `json:"last_error" db:"last_error"`
	ScheduledAt time.Time  `json:"scheduled_at" db:"scheduled_at"`
	StartedAt   *time.Time `json:"started_at" db:"started_at"`
	FinishedAt  *time.Time `json:"finished_at" db:"finished_at"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at" db:"updated_at"`
}

// IsCreate returns true if job type is "create"
func (j *SyncJob) IsCreate() bool {
	return j.JobType == "create"
}

// IsUpdate returns true if job type is "update"
func (j *SyncJob) IsUpdate() bool {
	return j.JobType == "update"
}

// ShouldRetry returns true if the job can be retried (not terminal and retry count less than max)
func (j *SyncJob) ShouldRetry(maxRetries int) bool {
	return !j.State.IsTerminal() && j.RetryCount < maxRetries
}

// NextRetryDelay calculates exponential backoff delay for next retry
func (j *SyncJob) NextRetryDelay(baseDelay time.Duration) time.Duration {
	// exponential: baseDelay * 2^retryCount, cap at 1 hour
	delay := baseDelay * (1 << j.RetryCount)
	if delay > time.Hour {
		delay = time.Hour
	}
	return delay
}