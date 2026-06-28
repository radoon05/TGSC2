package repository

import (
	"context"
	// "errors"
	"time"

	// "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tgsc/internal/domain"
)

// SyncJobRepository defines the interface for sync job persistence
type SyncJobRepository interface {
	CreateJob(ctx context.Context, job *domain.SyncJob) error
	FetchPendingJobs(ctx context.Context, limit int) ([]*domain.SyncJob, error)
	UpdateJobStatus(ctx context.Context, jobID string, state domain.SyncState, errMsg string, retryCount int) error
	ScheduleRetry(ctx context.Context, jobID string, nextScheduledAt time.Time, errMsg string) error
	MarkAsDeadLetter(ctx context.Context, jobID string, reason string) error
}

type syncJobRepo struct {
	db *pgxpool.Pool
}

// NewSyncJobRepository creates a new sync job repository
func NewSyncJobRepository(db *pgxpool.Pool) SyncJobRepository {
	return &syncJobRepo{db: db}
}

func (r *syncJobRepo) CreateJob(ctx context.Context, job *domain.SyncJob) error {
	query := `
		INSERT INTO sync_jobs (id, product_id, job_type, state, retry_count, last_error, scheduled_at, started_at, finished_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := r.db.Exec(ctx, query,
		job.ID, job.ProductID, job.JobType, job.State, job.RetryCount, job.LastError,
		job.ScheduledAt, job.StartedAt, job.FinishedAt, job.CreatedAt, job.UpdatedAt,
	)
	return err
}

func (r *syncJobRepo) FetchPendingJobs(ctx context.Context, limit int) ([]*domain.SyncJob, error) {
	query := `
		SELECT id, product_id, job_type, state, retry_count, last_error, scheduled_at, started_at, finished_at, created_at, updated_at
		FROM sync_jobs
		WHERE state = $1 AND scheduled_at <= NOW()
		ORDER BY scheduled_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	`
	rows, err := r.db.Query(ctx, query, domain.StatePending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*domain.SyncJob
	for rows.Next() {
		var j domain.SyncJob
		err := rows.Scan(
			&j.ID, &j.ProductID, &j.JobType, &j.State, &j.RetryCount, &j.LastError,
			&j.ScheduledAt, &j.StartedAt, &j.FinishedAt, &j.CreatedAt, &j.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, &j)
	}
	return jobs, nil
}

func (r *syncJobRepo) UpdateJobStatus(ctx context.Context, jobID string, state domain.SyncState, errMsg string, retryCount int) error {
	query := `
		UPDATE sync_jobs
		SET state = $1, retry_count = $2, last_error = $3, updated_at = NOW(),
			started_at = CASE WHEN $1 = 'RUNNING' AND started_at IS NULL THEN NOW() ELSE started_at END,
			finished_at = CASE WHEN $1 IN ('SUCCESS', 'FAILED', 'DEAD_LETTER') THEN NOW() ELSE finished_at END
		WHERE id = $4
	`
	_, err := r.db.Exec(ctx, query, state, retryCount, errMsg, jobID)
	return err
}

func (r *syncJobRepo) ScheduleRetry(ctx context.Context, jobID string, nextScheduledAt time.Time, errMsg string) error {
	query := `
		UPDATE sync_jobs
		SET state = $1, scheduled_at = $2, last_error = $3, retry_count = retry_count + 1, updated_at = NOW()
		WHERE id = $4
	`
	_, err := r.db.Exec(ctx, query, domain.StatePending, nextScheduledAt, errMsg, jobID)
	return err
}

func (r *syncJobRepo) MarkAsDeadLetter(ctx context.Context, jobID string, reason string) error {
	query := `
		UPDATE sync_jobs
		SET state = $1, last_error = $2, finished_at = NOW(), updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.db.Exec(ctx, query, domain.StateDeadLetter, reason, jobID)
	return err
}