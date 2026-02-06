-- Rename pictures_count to segments_count on jobs table
ALTER TABLE jobs RENAME COLUMN pictures_count TO segments_count;

-- Update check constraint (column name is part of the constraint expression)
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_pictures_count_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_segments_count_check CHECK (segments_count >= 1 AND segments_count <= 20);
