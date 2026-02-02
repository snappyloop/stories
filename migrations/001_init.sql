-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Users table
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_users_email ON users(email);

-- API Keys table
CREATE TYPE api_key_status AS ENUM ('active', 'disabled');
CREATE TYPE quota_period AS ENUM ('daily', 'weekly', 'monthly', 'yearly');

CREATE TABLE api_keys (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key_hash TEXT NOT NULL UNIQUE,
    status api_key_status NOT NULL DEFAULT 'active',
    quota_period quota_period NOT NULL DEFAULT 'monthly',
    quota_chars BIGINT NOT NULL DEFAULT 100000,
    used_chars_in_period BIGINT NOT NULL DEFAULT 0,
    period_started_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_api_keys_user_id ON api_keys(user_id);
CREATE INDEX idx_api_keys_key_hash ON api_keys(key_hash);

-- Jobs table
CREATE TYPE job_status AS ENUM ('queued', 'running', 'succeeded', 'failed', 'canceled');
CREATE TYPE input_type AS ENUM ('educational', 'financial', 'fictional');
CREATE TYPE audio_type AS ENUM ('free_speech', 'podcast');

CREATE TABLE jobs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    status job_status NOT NULL DEFAULT 'queued',
    input_type input_type NOT NULL,
    pictures_count INTEGER NOT NULL CHECK (pictures_count >= 1 AND pictures_count <= 20),
    audio_type audio_type NOT NULL,
    input_text TEXT NOT NULL,
    output_markup TEXT,
    webhook_url TEXT,
    webhook_secret TEXT,
    error_code TEXT,
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    started_at TIMESTAMP WITH TIME ZONE,
    finished_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX idx_jobs_user_id ON jobs(user_id, created_at DESC);
CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_created_at ON jobs(created_at DESC);

-- Segments table
CREATE TYPE segment_status AS ENUM ('queued', 'running', 'succeeded', 'failed');

CREATE TABLE segments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    job_id UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    idx INTEGER NOT NULL,
    start_char INTEGER NOT NULL,
    end_char INTEGER NOT NULL,
    title TEXT,
    segment_text TEXT,
    status segment_status NOT NULL DEFAULT 'queued',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_segments_job_id ON segments(job_id, idx);

-- Assets table
CREATE TYPE asset_kind AS ENUM ('image', 'audio');

CREATE TABLE assets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    job_id UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    segment_id UUID REFERENCES segments(id) ON DELETE CASCADE,
    kind asset_kind NOT NULL,
    mime_type TEXT NOT NULL,
    s3_bucket TEXT NOT NULL,
    s3_key TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    checksum TEXT,
    meta JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_assets_job_id ON assets(job_id);
CREATE INDEX idx_assets_segment_id ON assets(segment_id);
CREATE INDEX idx_assets_kind ON assets(job_id, segment_id, kind);

-- Webhook deliveries table
CREATE TYPE webhook_delivery_status AS ENUM ('pending', 'sent', 'failed');

CREATE TABLE webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    job_id UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    status webhook_delivery_status NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMP WITH TIME ZONE,
    last_error TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_webhook_deliveries_job_id ON webhook_deliveries(job_id);
CREATE INDEX idx_webhook_deliveries_status ON webhook_deliveries(status, created_at);

-- Trigger to update updated_at on segments
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_segments_updated_at BEFORE UPDATE ON segments
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
