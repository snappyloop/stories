-- Add fact_check_needed flag to jobs
ALTER TABLE jobs ADD COLUMN fact_check_needed BOOLEAN NOT NULL DEFAULT FALSE;

-- Fact-check output per segment (up to 512 chars)
CREATE TABLE segment_fact_checks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    segment_id UUID NOT NULL REFERENCES segments(id) ON DELETE CASCADE,
    job_id UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    fact_check_text VARCHAR(512) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_segment_fact_checks_segment_id ON segment_fact_checks(segment_id);
CREATE INDEX idx_segment_fact_checks_job_id ON segment_fact_checks(job_id);
