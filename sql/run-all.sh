#!/usr/bin/env bash
# Runs every SQL script against the database that the PG* environment
# variables (PGHOST, PGUSER, PGPASSWORD, PGDATABASE) point at.
set -euo pipefail
cd "$(dirname "$0")"
for f in [0-9][0-9]-*.sql; do
  echo "== $f"
  psql -X -q -v ON_ERROR_STOP=1 -f "$f"
done
