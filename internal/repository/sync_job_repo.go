package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tgsc/internal/domain"
)

// ============================================================
//  Interface
// ============================================================

type SyncJobRepository interface {
	CreateJob(ctx context.Context, job *domain.SyncJob) error
	ClaimPendingJobs(ctx context.Context, jobType string, limit int) ([]*domain.SyncJob, error)
	FetchPendingJobs(ctx context.Context, limit int) ([]*domain.SyncJob, error)
	FetchPendingJobsByType(ctx context.Context, jobType string, limit int) ([]*domain.SyncJob, error)
	UpdateJobStatus(ctx context.Context, jobID string, state domain.SyncState, errMsg string, retryCount int) error
	ScheduleRetry(ctx context.Context, jobID string, nextScheduledAt time.Time, errMsg string) error
	MarkAsDeadLetter(ctx context.Context, jobID string, reason string) error
	RecoverStaleJobs(ctx context.Context) (int64, error)
}

// ============================================================
//  syncJobRepo (پیاده‌سازی معمولی با pgxpool.Pool)
// ============================================================

type syncJobRepo struct {
	db *pgxpool.Pool
}

func NewSyncJobRepository(db *pgxpool.Pool) SyncJobRepository {
	return &syncJobRepo{db: db}
}

func (r *syncJobRepo) CreateJob(ctx context.Context, job *domain.SyncJob) error {
	query := `
		INSERT INTO sync_jobs (
			id, product_id, job_type, state, retry_count, last_error,
			scheduled_at, started_at, finished_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := r.db.Exec(ctx, query,
		job.ID, job.ProductID, job.JobType, job.State, job.RetryCount, job.LastError,
		job.ScheduledAt, job.StartedAt, job.FinishedAt, job.CreatedAt, job.UpdatedAt,
	)
	return err
}

func (r *syncJobRepo) ClaimPendingJobs(ctx context.Context, jobType string, limit int) ([]*domain.SyncJob, error) {
	query := `
		UPDATE sync_jobs
		SET state = 'RUNNING',
		    started_at = NOW(),
		    updated_at = NOW()
		WHERE id IN (
			SELECT id FROM sync_jobs
			WHERE state = 'PENDING'
			  AND scheduled_at <= NOW()
			  AND job_type = $1
			ORDER BY scheduled_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		RETURNING
			id,
			product_id,
			job_type,
			state,
			retry_count,
			last_error,
			scheduled_at,
			started_at,
			finished_at,
			created_at,
			updated_at
	`
	rows, err := r.db.Query(ctx, query, jobType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*domain.SyncJob
	for rows.Next() {
		var j domain.SyncJob
		err := rows.Scan(
			&j.ID,
			&j.ProductID,
			&j.JobType,
			&j.State,
			&j.RetryCount,
			&j.LastError,
			&j.ScheduledAt,
			&j.StartedAt,
			&j.FinishedAt,
			&j.CreatedAt,
			&j.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, &j)
	}
	return jobs, rows.Err()
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
	return jobs, rows.Err()
}

func (r *syncJobRepo) FetchPendingJobsByType(ctx context.Context, jobType string, limit int) ([]*domain.SyncJob, error) {
	query := `
		SELECT id, product_id, job_type, state, retry_count, last_error, scheduled_at, started_at, finished_at, created_at, updated_at
		FROM sync_jobs
		WHERE state = $1 AND scheduled_at <= NOW() AND job_type = $2
		ORDER BY scheduled_at ASC
		LIMIT $3
		FOR UPDATE SKIP LOCKED
	`
	rows, err := r.db.Query(ctx, query, domain.StatePending, jobType, limit)
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
	return jobs, rows.Err()
}

func (r *syncJobRepo) UpdateJobStatus(ctx context.Context, jobID string, state domain.SyncState, errMsg string, retryCount int) error {
	stateStr := state.String()
	query := `
		UPDATE sync_jobs
		SET state = $1::text,
		    retry_count = $2,
		    last_error = $3,
		    updated_at = NOW(),
		    started_at = CASE WHEN $1::text = 'RUNNING' AND started_at IS NULL THEN NOW() ELSE started_at END,
		    finished_at = CASE WHEN $1::text IN ('SUCCESS', 'FAILED', 'DEAD_LETTER') THEN NOW() ELSE finished_at END
		WHERE id = $4
	`
	_, err := r.db.Exec(ctx, query, stateStr, retryCount, errMsg, jobID)
	return err
}

func (r *syncJobRepo) ScheduleRetry(ctx context.Context, jobID string, nextScheduledAt time.Time, errMsg string) error {
	query := `
		UPDATE sync_jobs
		SET state = $1::text,
		    scheduled_at = $2,
		    last_error = $3,
		    retry_count = retry_count + 1,
		    updated_at = NOW()
		WHERE id = $4
	`
	_, err := r.db.Exec(ctx, query, domain.StatePending.String(), nextScheduledAt, errMsg, jobID)
	return err
}

func (r *syncJobRepo) MarkAsDeadLetter(ctx context.Context, jobID string, reason string) error {
	query := `
		UPDATE sync_jobs
		SET state = $1::text,
		    last_error = $2,
		    finished_at = NOW(),
		    updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.db.Exec(ctx, query, domain.StateDeadLetter.String(), reason, jobID)
	return err
}

// ============================================================
//  syncJobTxRepo (نسخه تراکنشی با pgx.Tx)
// ============================================================

type SyncJobTxRepo struct {
	tx pgx.Tx
}

func NewSyncJobTxRepo(tx pgx.Tx) *SyncJobTxRepo {
	return &SyncJobTxRepo{tx: tx}
}

func (r *SyncJobTxRepo) CreateJob(ctx context.Context, job *domain.SyncJob) error {
	query := `
		INSERT INTO sync_jobs (
			id, product_id, job_type, state, retry_count, last_error,
			scheduled_at, started_at, finished_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := r.tx.Exec(ctx, query,
		job.ID, job.ProductID, job.JobType, job.State, job.RetryCount, job.LastError,
		job.ScheduledAt, job.StartedAt, job.FinishedAt, job.CreatedAt, job.UpdatedAt,
	)
	return err
}

func (r *SyncJobTxRepo) ClaimPendingJobs(ctx context.Context, jobType string, limit int) ([]*domain.SyncJob, error) {
	query := `
		UPDATE sync_jobs
		SET state = 'RUNNING',
		    started_at = NOW(),
		    updated_at = NOW()
		WHERE id IN (
			SELECT id FROM sync_jobs
			WHERE state = 'PENDING'
			  AND scheduled_at <= NOW()
			  AND job_type = $1
			ORDER BY scheduled_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		RETURNING
			id,
			product_id,
			job_type,
			state,
			retry_count,
			last_error,
			scheduled_at,
			started_at,
			finished_at,
			created_at,
			updated_at
	`
	rows, err := r.tx.Query(ctx, query, jobType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*domain.SyncJob
	for rows.Next() {
		var j domain.SyncJob
		err := rows.Scan(
			&j.ID,
			&j.ProductID,
			&j.JobType,
			&j.State,
			&j.RetryCount,
			&j.LastError,
			&j.ScheduledAt,
			&j.StartedAt,
			&j.FinishedAt,
			&j.CreatedAt,
			&j.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, &j)
	}
	return jobs, rows.Err()
}

func (r *SyncJobTxRepo) FetchPendingJobs(ctx context.Context, limit int) ([]*domain.SyncJob, error) {
	query := `
		SELECT id, product_id, job_type, state, retry_count, last_error, scheduled_at, started_at, finished_at, created_at, updated_at
		FROM sync_jobs
		WHERE state = $1 AND scheduled_at <= NOW()
		ORDER BY scheduled_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	`
	rows, err := r.tx.Query(ctx, query, domain.StatePending, limit)
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
	return jobs, rows.Err()
}

func (r *SyncJobTxRepo) FetchPendingJobsByType(ctx context.Context, jobType string, limit int) ([]*domain.SyncJob, error) {
	query := `
		SELECT id, product_id, job_type, state, retry_count, last_error, scheduled_at, started_at, finished_at, created_at, updated_at
		FROM sync_jobs
		WHERE state = $1 AND scheduled_at <= NOW() AND job_type = $2
		ORDER BY scheduled_at ASC
		LIMIT $3
		FOR UPDATE SKIP LOCKED
	`
	rows, err := r.tx.Query(ctx, query, domain.StatePending, jobType, limit)
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
	return jobs, rows.Err()
}

func (r *SyncJobTxRepo) UpdateJobStatus(ctx context.Context, jobID string, state domain.SyncState, errMsg string, retryCount int) error {
	stateStr := state.String()
	query := `
		UPDATE sync_jobs
		SET state = $1::text,
		    retry_count = $2,
		    last_error = $3,
		    updated_at = NOW(),
		    started_at = CASE WHEN $1::text = 'RUNNING' AND started_at IS NULL THEN NOW() ELSE started_at END,
		    finished_at = CASE WHEN $1::text IN ('SUCCESS', 'FAILED', 'DEAD_LETTER') THEN NOW() ELSE finished_at END
		WHERE id = $4
	`
	_, err := r.tx.Exec(ctx, query, stateStr, retryCount, errMsg, jobID)
	return err
}

func (r *SyncJobTxRepo) ScheduleRetry(ctx context.Context, jobID string, nextScheduledAt time.Time, errMsg string) error {
	query := `
		UPDATE sync_jobs
		SET state = $1::text,
		    scheduled_at = $2,
		    last_error = $3,
		    retry_count = retry_count + 1,
		    updated_at = NOW()
		WHERE id = $4
	`
	_, err := r.tx.Exec(ctx, query, domain.StatePending.String(), nextScheduledAt, errMsg, jobID)
	return err
}

func (r *SyncJobTxRepo) MarkAsDeadLetter(ctx context.Context, jobID string, reason string) error {
	query := `
		UPDATE sync_jobs
		SET state = $1::text,
		    last_error = $2,
		    finished_at = NOW(),
		    updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.tx.Exec(ctx, query, domain.StateDeadLetter.String(), reason, jobID)
	return err
}

func (r *syncJobRepo) RecoverStaleJobs(ctx context.Context) (int64, error) {
    query := `
        UPDATE sync_jobs
        SET state = 'PENDING',
            last_error = 'job recovered from stale running state',
            scheduled_at = NOW() + INTERVAL '1 minute',
            updated_at = NOW(),
            finished_at = NULL
        WHERE state = 'RUNNING'
          AND started_at < NOW() - INTERVAL '15 minutes'
    `
    result, err := r.db.Exec(ctx, query)
    if err != nil {
        return 0, err
    }
    return result.RowsAffected(), nil
}