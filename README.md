# Lucrum

A small Go service that publishes Warframe knowledge and market statistics as
JSON. It uses the Go standard library and stores its state in files.

## Run

With Docker:

```sh
cp .env.example .env
docker compose up --build -d
curl http://localhost:8080/healthz
```

The named volume keeps data across restarts. Run one instance per volume. Compose
uses port 8080 and /data inside the container.

Locally, with the Go version in go.mod:

```sh
cp .env.example .env
set -a
. ./.env
set +a
go run .
```

The program reads process environment variables; it does not parse .env itself.

| Variable | Default |
| --- | --- |
| CATALOG_REFRESH_MINUTES | 180 |
| WFM_REQUESTS_PER_SECOND | 2.5 (greater than 0, at most 3) |
| HTTP_ADDR | :8080 |
| DATA_DIR | ./data |

## Datasets and API

| Endpoint | Result |
| --- | --- |
| GET /api/items | Rich items.json object keyed by gameRef |
| GET /api/warframe-market-items | Lightweight market catalogue array |
| GET /api/tradeable-items | Lightweight statistics array |
| GET /healthz | Process health, refresh errors/times, pending statistics count |
| GET /readyz | 200 when all three datasets exist; otherwise 503 |

Missing datasets return 503. Valid saved data remains available during upstream
outages. On first startup, listings appear with empty statistics, zero liquidity,
and null last_fetched_at. Statistics populate progressively; this does not block
readiness. Public snapshots update at most 30 seconds after a successful fetch.

### Knowledge

WFCD's published @wfcd/items archive supplies the knowledge snapshot. Masterable
or tradable items are roots. Components, abilities, and required items are followed
transitively. Drops, reward sources, and other associations get resolvable display
records without expanding their graphs. An association target becomes a full
record if it is independently a root or dependency.

Each identity has one canonical record with gameRef. Component entries contain
references and quantities, such as {"gameRef": "/example/part", "quantity": 2}.
Full records preserve recipes, acquisition information, mastery, combat stats,
relic data, and unknown fields. uniqueName, patchlogs, and wikiAvailable are removed.

Join knowledge and market data using exact, nonempty gameRef. Some market
listings have missing or shared references; market_id identifies each listing.

### Market statistics

Each tradeable entry contains market_id, gameRef, slug, name, variants,
statistics_today, statistics_yesterday, statistics_live, liquidity, and
last_fetched_at. Statistics arrays stay flat with their upstream variant fields.
Only datetime and id are removed from retained public rows.

- Closed 90-day timestamps shift **forward 24 hours**. Retain all rows belonging
  to the current UTC day and yesterday.
- Live 48-hour timestamps shift **forward one hour**. Retain the latest
  nonfuture sell row independently for each observed variant.
- Liquidity sums both retained closed buckets across variants.
- Refresh every 24 hours for liquidity <=20, every 6 hours for 21–150, and every
  hour above 150. These intervals apply to successful fetches.
- At UTC midnight, cached closed rows move between buckets without extra
  requests. last_fetched_at remains the successful-fetch time.

All market calls request Platform: pc, Crossplay: true, and Language: en.
Crossplay behavior depends on endpoint support; sending the header does not
establish that the legacy v1 statistics endpoint combines platforms.
Market requests and retries share the rate limit. HTTP 429 pauses that shared
client and honors Retry-After. Exhausted failures retry after five minutes.

### Cheap knowledge update checks

The knowledge endpoint supports GET/HEAD, ETag, Last-Modified, and conditional
requests. Store both the downloaded file and its ETag:

```sh
curl -D headers.txt http://localhost:8080/api/items -o items.json
curl -i -H 'If-None-Match: "ETAG_FROM_HEADERS"' http://localhost:8080/api/items
curl -I http://localhost:8080/api/items
```

An unchanged dataset returns **304 with no body**; changed content returns the
new JSON and ETag. Cache-Control: public, no-cache permits storage and requires
revalidation. ETags are computed during publication, not during update checks.
Files are built separately and atomically replaced, so downloads see complete
snapshots with matching validators.

## Development

Go code lives under internal/ apart from main.go. See AGENTS.md for invariants.

```sh
go test -race ./...
go vet ./...
```

An optional test downloads real upstream data into temporary storage and verifies
normalization, publication, and conditional requests:

```sh
LUCRUM_LIVE_TEST=1 go test -v ./internal/integration -run TestLiveUpstream
```
