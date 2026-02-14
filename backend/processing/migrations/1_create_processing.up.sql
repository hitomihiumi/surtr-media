-- Processing service uses the media database tables
-- This migration is a placeholder to satisfy Encore's service requirements
-- The actual media tables are in the media service

CREATE TABLE processing_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    error_message TEXT,
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_processing_jobs_media ON processing_jobs(media_id);
CREATE INDEX idx_processing_jobs_status ON processing_jobs(status);

