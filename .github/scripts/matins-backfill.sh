#!/usr/bin/env bash
# Dispatch a whole backfill in one command: one matins-watch run per chunk.
#
#   .github/scripts/matins-backfill.sh 2026-05-21            # the last ~90 days
#   .github/scripts/matins-backfill.sh 2026-04-14 2026-06-30 # an explicit window
#
# Chunked rather than a single run because ~7KB of brief text per day adds up — the whole
# archive is ~500KB, and one agent pass asked to weigh all of it at once skims instead of
# reading. A workflow cannot dispatch itself under gh-aw (deliberately, to bar runaway
# loops), so the loop lives here. The runs queue on the workflow's concurrency group, so
# each chunk sees the issues the chunk before it filed and can dedupe against them.
set -euo pipefail
cd "$(dirname "$0")/../.."
SINCE="${1:-}"
UNTIL="${2:-}"
MAX_BRIEFS="${MAX_BRIEFS:-14}"

if [ -z "$SINCE" ]; then
  echo "usage: $0 <since YYYY-MM-DD> [until YYYY-MM-DD]   (env: MAX_BRIEFS=$MAX_BRIEFS)" >&2
  exit 2
fi

python3 .github/scripts/matins-fetch.py --plan \
  --since "$SINCE" --until "$UNTIL" --max-briefs "$MAX_BRIEFS" |
while read -r chunk_since chunk_until; do
  echo "dispatching $chunk_since .. $chunk_until"
  gh workflow run matins-watch.lock.yml \
    -f since="$chunk_since" \
    -f until="$chunk_until" \
    -f max-briefs="$MAX_BRIEFS"
done
