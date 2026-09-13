# Lucrum

Lucrum is a Bun/TypeScript CLI that collects public Warframe Market data as static JSON. It has no application server or frontend.

## Behavior

Each run:

1. Refreshes `data/dictionary.json` from `https://api.warframe.market/v2/items` when the file is missing, unreadable, malformed, or at least 24 hours old.
2. Selects dictionary slugs after applying the configured offset and limit.
3. Reuses snapshots that are still fresh according to their liquidity.
4. Sequentially fetches `/v1/items/{slug}/statistics` for stale or missing snapshots.
5. Replaces `data/tradeable_items.json` only after the complete selected run succeeds.

Dictionary entries contain the slug, English name (falling back to the slug), tags, and optional `maxRank`, `vaulted`, and `ducats` metadata. Tradeable components also receive a `set_slug` pointing to their matching market set, such as `wisp_prime_systems_blueprint -> wisp_prime_set`.

The raw first response fetched for each slug in a UTC month is stored at `data/archive/YYYY-MM/{slug}.json.gz`. Archive errors are logged but do not fail the scrape.

## Requirements

- [Bun](https://bun.sh/)
- Internet access to `api.warframe.market`

## Run

From the repository root:

```sh
bun install
bun run start
```

This contacts the live API and modifies `data/`.

Useful commands:

```sh
bun run check
```

Set relationships come from `@wfcd/items`. Its data is published frequently alongside Warframe updates, so update and redeploy it with:

```sh
bun run update:items
```

Mappings are derived from the installed dataset rather than a hard-coded item list. They are reapplied on every run, including when the API dictionary cache is still fresh.

## Configuration

| Variable                     | Default | Meaning                                     |
| ---------------------------- | ------: | ------------------------------------------- |
| `LUCRUM_REQUESTS_PER_SECOND` |   `2.5` | Finite, positive request-start rate         |
| `LUCRUM_FETCH_OFFSET`        |     `0` | Slugs to skip; `0` means no skip            |
| `LUCRUM_FETCH_LIMIT`         |    `20` | Maximum selected slugs; `0` means unlimited |

Offset and limit are applied before cache checks. Requests are sequential and have 60-second timeouts.

## Output semantics

- All dates and comparisons use UTC.
- Closed `90days` timestamps shift forward one day before selecting today and yesterday.
- Live `48hours` timestamps shift forward one hour before selecting current-hour sell rows.
- Every matching statistics row is retained regardless of rank or subtype. `id`, `datetime`, and `order_type` are removed.
- Liquidity is the sum of today and yesterday volumes.
- Snapshot freshness uses rounded elapsed hours: liquidity up to 20 uses 24 hours, 21–100 uses 6 hours, and above 100 uses 1 hour.
- A fetch or parse failure aborts the run without writing a partial `tradeable_items.json`. There are no per-item retries; deployment retries the whole run.

See [`deploy/README.md`](deploy/README.md) for Coolify and Caddy setup.
