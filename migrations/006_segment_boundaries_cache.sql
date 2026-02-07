-- Segment boundaries cache: text_hash (sha256 of normalized text) -> boundaries JSON
CREATE TABLE segment_boundaries_cache (
    text_hash TEXT PRIMARY KEY,
    input_type TEXT NOT NULL,
    boundaries JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_segment_boundaries_cache_created_at ON segment_boundaries_cache(created_at);
