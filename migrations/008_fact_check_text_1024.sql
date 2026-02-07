-- Expand fact_check_text to allow up to 1024 characters
ALTER TABLE segment_fact_checks ALTER COLUMN fact_check_text TYPE VARCHAR(1024);
