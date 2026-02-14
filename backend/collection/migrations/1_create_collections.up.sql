-- Create collections table
CREATE TABLE collections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id BIGINT NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    is_public BOOLEAN DEFAULT FALSE,
    share_token UUID DEFAULT gen_random_uuid(),
    created_at TIMESTAMP DEFAULT NOW()
);

-- Create collection_items junction table
CREATE TABLE collection_items (
    collection_id UUID REFERENCES collections(id) ON DELETE CASCADE,
    media_id UUID NOT NULL,
    added_at TIMESTAMP DEFAULT NOW(),
    PRIMARY KEY (collection_id, media_id)
);

-- Indexes
CREATE INDEX idx_collections_owner ON collections(owner_id);
CREATE INDEX idx_collections_share_token ON collections(share_token);
CREATE INDEX idx_collection_items_media ON collection_items(media_id);

