-- Stage 1: look inside PostgreSQL heap pages.
-- Run: psql -v ON_ERROR_STOP=1 -f sql/01-heap-pages.sql
CREATE EXTENSION IF NOT EXISTS pageinspect;

DROP TABLE IF EXISTS s1_accounts;
CREATE TABLE s1_accounts (id int PRIMARY KEY, owner text, balance numeric);
-- 20 rows leave page 0 mostly empty, so nothing below is pruned.
INSERT INTO s1_accounts
SELECT g, 'owner-' || g, g * 10 FROM generate_series(1, 20) AS g;

-- Every row has a physical address: (block number, line pointer).
SELECT ctid, id, owner FROM s1_accounts ORDER BY id LIMIT 3;

-- The page header: lower = end of the line-pointer array,
-- upper = start of tuple data. Free space is upper - lower.
SELECT lsn, lower, upper, upper - lower AS free_bytes, special, pagesize
FROM page_header(get_raw_page('s1_accounts', 0));

-- Line pointers (the slot array). lp_off is where the tuple starts,
-- lp_len its length. Offsets decrease: tuples grow down from the end.
SELECT lp, lp_off, lp_len, t_xmin, t_xmax, t_ctid
FROM heap_page_items(get_raw_page('s1_accounts', 0))
ORDER BY lp LIMIT 5;

-- An UPDATE writes a new tuple version. The old one gets t_xmax set and its
-- t_ctid points at the new version, so the row's ctid changes.
UPDATE s1_accounts SET balance = balance + 1 WHERE id = 1;
SELECT ctid, id FROM s1_accounts WHERE id = 1;
SELECT lp, t_xmin, t_xmax, t_ctid
FROM heap_page_items(get_raw_page('s1_accounts', 0))
WHERE lp IN (1, 21);

DO $$
DECLARE
  n int;
BEGIN
  SELECT count(*) INTO n
  FROM heap_page_items(get_raw_page('s1_accounts', 0))
  WHERE t_xmax <> 0;
  ASSERT n = 1, 'expected one old tuple version after the UPDATE';
END $$;

-- Fill more pages, then count them.
INSERT INTO s1_accounts
SELECT g, 'owner-' || g, g * 10 FROM generate_series(21, 2000) AS g;
SELECT current_setting('block_size') AS block_size,
       pg_relation_size('s1_accounts') / current_setting('block_size')::int AS pages;

DROP TABLE s1_accounts;
