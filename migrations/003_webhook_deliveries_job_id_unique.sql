-- Enforce at-most-once webhook delivery per job (idempotent Kafka handling).
-- Dedupe: keep one row per job_id (earliest created_at), then add unique constraint.
DELETE FROM webhook_deliveries a
USING webhook_deliveries b
WHERE a.job_id = b.job_id AND a.created_at > b.created_at;

DROP INDEX IF EXISTS idx_webhook_deliveries_job_id;
ALTER TABLE webhook_deliveries ADD CONSTRAINT webhook_deliveries_job_id_key UNIQUE (job_id);
