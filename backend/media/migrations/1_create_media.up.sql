-- Create media table
CREATE TABLE media (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id BIGINT NOT NULL,
    title TEXT,
    original_filename TEXT,
    s3_key_original TEXT NOT NULL,
    s3_key_processed TEXT,
    mime_type TEXT,
    size_bytes BIGINT,
    duration_seconds INT,
    status TEXT NOT NULL DEFAULT 'uploading' CHECK (status IN ('uploading', 'queued', 'processing', 'ready', 'failed')),
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create tags table
CREATE TABLE tags (
    id BIGSERIAL PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    color TEXT DEFAULT '#FFFFFF'
);

-- Create media_tags junction table
CREATE TABLE media_tags (
    media_id UUID REFERENCES media(id) ON DELETE CASCADE,
    tag_id BIGINT REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (media_id, tag_id)
);

-- Indexes
CREATE INDEX idx_media_owner ON media(owner_id);
CREATE INDEX idx_media_status ON media(status);
CREATE INDEX idx_media_created ON media(created_at DESC);
CREATE INDEX idx_tags_name ON tags(name);

