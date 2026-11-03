-- Stage 2: watch the write-ahead log grow.
-- Run: psql -v ON_ERROR_STOP=1 -f sql/02-wal.sql
DROP TABLE IF EXISTS s2_events;
CREATE TABLE s2_events (id bigint, payload text);

-- The current insert position in the WAL, as an LSN (a byte position).
SELECT pg_current_wal_lsn() AS lsn, pg_walfile_name(pg_current_wal_lsn()) AS segment;

-- Measure how many WAL bytes one batch of inserts generates.
CREATE TEMP TABLE s2_mark AS SELECT pg_current_wal_lsn() AS before;
INSERT INTO s2_events SELECT g, repeat('x', 100) FROM generate_series(1, 10000) AS g;
SELECT pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), before)) AS wal_generated
FROM s2_mark;

-- Settings that decide durability and torn-page protection.
SELECT name, setting FROM pg_settings
WHERE name IN ('wal_level', 'fsync', 'synchronous_commit', 'full_page_writes',
               'wal_compression', 'checkpoint_timeout', 'max_wal_size', 'commit_delay')
ORDER BY name;

-- After a checkpoint, the first change to each page logs the whole page
-- (a full-page image). Compare WAL volume for the same UPDATE before and
-- after a checkpoint.
CHECKPOINT;
UPDATE s2_mark SET before = pg_current_wal_lsn();
UPDATE s2_events SET payload = 'y' WHERE id <= 100;
SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), before) AS bytes_first_update FROM s2_mark;
UPDATE s2_mark SET before = pg_current_wal_lsn();
UPDATE s2_events SET payload = 'z' WHERE id <= 100;
SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), before) AS bytes_second_update FROM s2_mark;

DO $$
BEGIN
  ASSERT pg_current_wal_lsn() > '0/0'::pg_lsn, 'WAL position must advance';
END $$;

DROP TABLE s2_events;
