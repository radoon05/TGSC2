CREATE TABLE sync_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    job_type VARCHAR(10) NOT NULL CHECK (job_type IN ('create', 'update')),
    state VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    retry_count INT NOT NULL DEFAULT 0,
    last_error TEXT,
    scheduled_at TIMESTAMP WITH TIME ZONE NOT NULL,
    started_at TIMESTAMP WITH TIME ZONE,
    finished_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sync_jobs_state_scheduled ON sync_jobs(state, scheduled_at) WHERE state = 'PENDING';
CREATE INDEX idx_sync_jobs_product_id ON sync_jobs(product_id);
CREATE INDEX idx_sync_jobs_state_retry ON sync_jobs(state, retry_count);