-- Stage 3: inspect a B+ tree index and compare access paths.
-- Run: psql -v ON_ERROR_STOP=1 -f sql/03-indexes.sql
CREATE EXTENSION IF NOT EXISTS pageinspect;

DROP TABLE IF EXISTS s3_orders;
CREATE TABLE s3_orders (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  customer_id int     NOT NULL,
  status      text    NOT NULL,
  total       numeric NOT NULL
);
INSERT INTO s3_orders (customer_id, status, total)
SELECT (random() * 5000)::int,
       (ARRAY['new', 'paid', 'shipped'])[1 + (g % 3)],
       round((random() * 500)::numeric, 2)
FROM generate_series(1, 200000) AS g;
CREATE INDEX s3_orders_customer ON s3_orders (customer_id) INCLUDE (total);
VACUUM ANALYZE s3_orders;

-- Metapage: where the root is and how many levels the tree has.
-- level = 0 means the root is a leaf; 200k rows usually give level 1 or 2.
SELECT root, level, fastroot, fastlevel FROM bt_metap('s3_orders_pkey');

-- Leaf page statistics: live items, free space, sibling links.
SELECT blkno, type, live_items, avg_item_size, free_size, btpo_prev, btpo_next
FROM bt_page_stats('s3_orders_pkey', 1);

-- Every index page at once (PostgreSQL 17+ accepts a block range).
SELECT type, count(*) AS pages, sum(live_items) AS items
FROM bt_multi_page_stats('s3_orders_pkey', 1, -1)
GROUP BY type;

-- Access paths: the planner picks a different plan per predicate.
EXPLAIN (COSTS OFF) SELECT * FROM s3_orders WHERE id = 42;
EXPLAIN (COSTS OFF) SELECT customer_id, total FROM s3_orders WHERE customer_id = 7;
EXPLAIN (COSTS OFF) SELECT * FROM s3_orders WHERE status = 'paid';

-- Index-only scans depend on the visibility map. Heap Fetches: 0 means every
-- page was all-visible and the heap was never touched.
EXPLAIN (ANALYZE, COSTS OFF, TIMING OFF, SUMMARY OFF)
SELECT customer_id, total FROM s3_orders WHERE customer_id BETWEEN 100 AND 200;

-- Size of the table versus its indexes.
SELECT relname, pg_size_pretty(pg_relation_size(oid)) AS size
FROM pg_class
WHERE relname IN ('s3_orders', 's3_orders_pkey', 's3_orders_customer')
ORDER BY relname;

DO $$
DECLARE
  lvl int;
BEGIN
  SELECT level INTO lvl FROM bt_metap('s3_orders_pkey');
  ASSERT lvl BETWEEN 1 AND 3, format('unexpected B-tree height %s', lvl);
END $$;

DROP TABLE s3_orders;
