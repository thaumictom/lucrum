# Project guide

Lucrum is a Rust 2024 CLI that collects Warframe Market data into local JSON for static hosting. It uses synchronous `reqwest` with rustls, Serde, Chrono, anyhow, and gzip archives; there is no application server or frontend.

## Code map and behavior
- `src/main.rs`: orchestrates dictionary refresh and snapshot generation; paths are relative to the working directory.
- `src/cache.rs` / `src/fetch.rs`: refresh `data/dictionary.json` from `/v2/items` when missing, unreadable, malformed, or at least 24 hours old. Store slugs, English names (fallback: slug), tags, and optional vaulted/ducat metadata.
- `src/tradeable_items.rs`: selects dictionary slugs, reuses fresh snapshots, and sequentially fetches `/v1/items/{slug}/statistics` with request-start pacing and 60-second HTTP timeouts. Writes the first fetched raw response per slug/month to `data/archive/YYYY-MM/{slug}.json.gz`; archive failures only log.
- `src/types.rs`: API and persisted JSON structures. `src/output.rs`: pretty JSON writes, creating parent directories. `data/tradeable_items.json` contains `run_start`, `run_end`, and `tradeable_items`; only selected slugs are included, replacing the previous run on success. Fetch/parse errors abort without saving partial snapshots; writes are not atomic.
- `sample.json`: example statistics payload, useful for offline fixtures.

## Preserve these semantics
- All date handling is UTC. Closed `90days` rows shift **+1 day** before selecting today/yesterday; live `48hours` rows shift **+1 hour** before selecting current-hour sells. `current_offers` contains statistics rows, not individual orders.
- Keep all matching rows regardless of rank or subtype; strip `id`, `datetime`, and `order_type` from output rows. Liquidity sums today's and yesterday's volumes.
- Snapshot freshness uses rounded elapsed hours: liquidity `<=20` lasts 24 hours, `21–100` lasts 6, and `>100` lasts 1. Missing/malformed previous-run JSON gives an empty cache; other read errors propagate.

## Development
- Run from the repository root: `cargo run --release --locked`. This contacts the live API and modifies `data/`.
- Environment: `LUCRUM_REQUESTS_PER_SECOND=2.5` by default (finite, positive); `LUCRUM_FETCH_OFFSET` defaults to no skip; `LUCRUM_FETCH_LIMIT` defaults to **20**, with `0` meaning unlimited. Offset/limit apply before cache checks.
- For Rust changes, use `cargo fmt --check`, `cargo check --locked`, and `cargo test --locked`. Filtering regression tests live in `src/tradeable_items.rs`; add focused offline tests when changing filtering/cache logic. Keep generated `data/` and `target/` out of Git.

## Deployment and documentation
- `deploy/` provides a Coolify Compose worker and Caddy sharing a data volume. The worker idles; a separately configured Coolify task runs `run-lucrum.sh` every six hours. The wrapper uses `flock` and retries whole runs (defaults: 3 attempts, 300 seconds apart).
- Caddy serves data under `/warframe/v1/` with CORS, compression, and five-minute JSON caching. Compose build paths are repository-root relative. Compose currently exposes request rate and wrapper retry settings, but not fetch offset/limit.
- Both READMEs contain stale filenames/retry claims: actual outputs are `dictionary.json` and `tradeable_items.json`; Rust has no per-item retries or `last_error` field. Follow source behavior and keep docs/configuration aligned when changing it.
