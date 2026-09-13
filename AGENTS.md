# Project guide

Lucrum is a TypeScript CLI running on Bun that collects Warframe Market data into local JSON for static hosting. It uses Bun's `fetch`, Node-compatible filesystem and zlib APIs; there is no application server or frontend.

## Code map and behavior

- `src/main.ts`: small application entry point; paths and environment parsing live in `src/config.ts`.
- `src/dictionary.ts`: refreshes `data/dictionary.json` when missing, unreadable, malformed, or at least 24 hours old. It stores slugs, English names (fallback: slug), tags, and optional `maxRank`, vaulted, and ducat metadata.
- `src/item-sets.ts`: derives component-to-set relationships from the installed `@wfcd/items` dataset and adds optional `set_slug` values. Mappings are reapplied even when the API dictionary cache is fresh so dependency updates take effect immediately.
- `src/warframe-market.ts`: owns the `/v2/items` and `/v1/items/{slug}/statistics` HTTP boundary and validates API payloads.
- `src/snapshots.ts`: selects dictionary slugs, reuses fresh snapshots, filters statistics, and applies sequential request-start pacing with 60-second HTTP timeouts.
- `src/storage.ts`: reads and pretty-writes JSON and stores the first fetched raw response per slug/month at `data/archive/YYYY-MM/{slug}.json.gz`; archive failures only log.
- `src/models.ts`: persisted data shapes and the shared record type guard.
- `data/tradeable_items.json` contains `run_start`, `run_end`, and `tradeable_items`; only selected slugs are included, replacing the previous run on success. Fetch/parse errors abort without saving partial snapshots; writes are not atomic.
- `sample.json`: example statistics payload, useful for offline fixtures.

## Preserve these semantics

- All date handling is UTC. Closed `90days` rows shift **+1 day** before selecting today/yesterday; live `48hours` rows shift **+1 hour** before selecting current-hour sells. `current_offers` contains statistics rows, not individual orders.
- Keep all matching rows regardless of rank or subtype; strip `id`, `datetime`, and `order_type` from output rows. Liquidity sums today's and yesterday's volumes.
- Snapshot freshness uses rounded elapsed hours: liquidity `<=20` lasts 24 hours, `21–100` lasts 6, and `>100` lasts 1. Missing/malformed previous-run JSON gives an empty cache; other read errors propagate.

## Development

- Run from the repository root: `bun run start`. This contacts the live API and modifies `data/`.
- Environment: `LUCRUM_REQUESTS_PER_SECOND=2.5` by default (finite, positive); `LUCRUM_FETCH_OFFSET` defaults to no skip; `LUCRUM_FETCH_LIMIT` defaults to **20**, with `0` meaning unlimited. Offset/limit apply before cache checks.
- For TypeScript changes, use `bun run check`. Keep generated `data/`, `node_modules/`, and legacy `target/` out of Git.

## Deployment and documentation

- `deploy/` provides a Coolify Compose worker and Caddy sharing a data volume. The worker idles; a separately configured Coolify task runs `run-lucrum.sh` every six hours. The wrapper uses `flock` and retries whole runs (defaults: 3 attempts, 300 seconds apart).
- Caddy serves data under `/warframe/v1/` with CORS, compression, and five-minute JSON caching. Compose build paths are repository-root relative and expose request rate, fetch offset/limit, and wrapper retry settings.
- Generated outputs are `dictionary.json` and `tradeable_items.json`. The CLI has no per-item retries or `last_error` field. Keep documentation and deployment configuration aligned with source behavior.
