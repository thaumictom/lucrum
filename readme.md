# Lucrum

A small Go service serving a Warframe Market catalogue, cached trading statistics, and WFCD game data on port **3100**. The WFM catalogue refreshes and WFCD releases are checked at startup and every 180 minutes.

## Run locally

Install Go 1.27 or newer, then run from the project root:

```sh
cp .env.example .env
go run ./cmd/lucrum
```

Optional `.env` settings (existing environment variables take precedence):

| Variable                 | Default  | Meaning                                            |
| ------------------------ | -------- | -------------------------------------------------- |
| `FETCH_INTERVAL_MINUTES` | `180`    | Positive whole minutes between WFM catalogue refreshes and WFCD release checks |
| `DATA_DIR`               | `./data` | Directory for the generated file and hash metadata |
| `WFM_REQUESTS_PER_SECOND` | `2.5` | Shared upstream request starts per second; greater than 0, at most 3 |
| `DEBUG` | `false` | Log every fetch and scheduling skip |

Port 3100 is fixed. Restart the app after changing settings.

## Use the endpoint

```sh
curl -i http://localhost:3100/warframe/v2/wfm-items
# Substitute the quoted ETag returned above:
curl -i -H 'If-None-Match: "your-etag-hash"' http://localhost:3100/warframe/v2/wfm-items
```

The response contains `items` and `last_fetched_at`. Each item keeps its original fields except `id` and `i18n`; `name` comes from `i18n.en.name`. The UTC timestamp records the request start time for the published upstream version. Unchanged upstream responses leave the timestamp untouched; parent links can still update the file.

The upstream response SHA-256 detects changes. A separate SHA-256 of the generated file is its ETag. Matching conditional requests return `304` without a body; HEAD is also supported. `Cache-Control: public, no-cache` allows storage but requires revalidation.

Updates atomically replace `wfm-items.json`. Requests stream from disk, and failed refreshes retain the last valid file. Before any valid file exists, the endpoint returns `503`. Failures retry at the next interval. Fetches have a 30-second timeout and a 32 MiB response limit.

WFM entries tagged `component` or `blueprint` gain `set_slug` when `items.json` links them to one market parent. Its value is the parent's WFM slug, with the same `Component` → `Blueprint` matching fallback used for `marketSlug`. Entries without a market parent, or with multiple market parents, omit the field. Parent links refresh at the fetch interval even when the upstream WFM response is unchanged, preserving `last_fetched_at`. If `items.json` is not available yet, linking waits for a later refresh.

## Trading statistics

```sh
curl -i http://localhost:3100/warframe/v2/tradeable-items
```

`tradeable-items.json` contains an `items` array with `slug`, `name`, `gameRef`, `liquidity`, `statistics_today`, `statistics_yesterday`, `statistics_live`, and `last_fetched_at` on each item. Initially, arrays are empty, liquidity is zero, and the timestamp is null. This endpoint supports the same ETag, HEAD, and conditional-request behavior as the catalogue.

Each statistics fetch uses `https://api.warframe.market/v1/items/{slug}/statistics`. WFM's completed periods are delayed: a September 16 UTC request uses September 15 from `statistics_closed["90days"]` for `statistics_today`, and September 14 for `statistics_yesterday`. `statistics_live` contains all sell variants at the newest sell timestamp from `statistics_live["48hours"]`. Only `id` and `datetime` are stripped from these records. Liquidity sums volume across both daily arrays.

Passes start every hour at **:15 UTC**, including the first pass after startup. An active pass causes that scheduled tick to be skipped. Each pass reads the latest catalogue and processes due slugs once in catalogue order, without sorting. Catalogue changes join the next pass. Refresh intervals are **24 hours** for liquidity 0–20, **6 hours** for 21–150, and **1 hour** above 150. Deadlines are measured from request start and checked at the next scheduled pass, so actual refreshes may happen later than those intervals.

Only successful fetches update an item's statistics and `last_fetched_at`. Failures retain the previous snapshot and wait the existing liquidity interval; a never-successful item waits 24 hours. Dates describe the last successful fetch's snapshot, even across midnight. Private deadline metadata prevents immediate retries after restarting.

The shared limiter spaces catalogue and statistics request starts, including redirects, at least 400 ms apart by default. Statistics requests can overlap, with at most eight in flight. A 429 pauses all new starts for `Retry-After` (seconds or an HTTP date), or 30 seconds if missing/invalid; existing requests can finish. The failed slug is not retried within the pass, and pauses survive restarts. Rate-limit waiting does not consume the HTTP timeout. Other applications sharing the same upstream rate limit must leave sufficient capacity.

Progress is atomically published every **max(1, ceil(WFM_REQUESTS_PER_SECOND × 60)) completed statistics requests**: 150 by default, counting failures. There is no publication timer. Remaining changes publish at pass completion or graceful shutdown; failure-only checkpoints leave the public file untouched. An abrupt stop can lose unpublished statistics, but saved deadlines remain in effect. At the default rate, an initial pass over 3,840 items takes at least 26 minutes after its scheduled start.

Normal logs include pass summaries, publications, failures, and rate-limit pauses. `DEBUG=true` adds individual fetches and skips. Both snapshots, private deadlines, and pause state live in `DATA_DIR`; keep that directory persistent.

## WFCD game data

```sh
curl -i http://localhost:3100/warframe/v2/items
```

The app checks the latest published [WFCD/warframe-items release](https://github.com/WFCD/warframe-items/releases). If its version matches the saved snapshot's metadata, it reuses the saved records and refreshes market links. Otherwise, it reads every JSON file in `data/json` except `i18n.json`, pinned to that release tag.

`items.json` is a **root map keyed by `uniqueName`**, without an `items` wrapper. Top-level entries are kept when `tradable` or `masterable` is `true`. Their components are recursively retained regardless of those flags, so every component reference resolves:

```json
{
  "/Example/Parent": {"name": "Parent", "masterable": true, "components": ["/Example/Part"]},
  "/Example/Part": {"name": "Part", "tradable": true}
}
```

Each flattened entry loses its own `uniqueName`; other fields, including nested abilities and drops, remain intact. Shared components have one definition: top-level fields take precedence over nested copies, otherwise the first definition in alphabetical file order wins. Missing fields and additional component links are merged from other copies so partial definitions do not hide sub-recipes. The preferred component list retains its order and duplicates; extra links are appended once.

Entries gain `marketSlug` from `wfm-items.json` when their key matches a market item's `gameRef`. If no exact match exists, a trailing `Component` is replaced with `Blueprint` for matching (for example, `WispPrimeChassisComponent` → `WispPrimeChassisBlueprint`). Unmatched entries omit `marketSlug`. Links refresh at the configured fetch interval, even for unchanged WFCD releases. If WFM data is not available yet, the import proceeds and links are added on a later refresh.

Source categories are streamed and discarded; only `items.json` and `items.meta.json` (release version and output hash) are saved in `DATA_DIR`. Publication is atomic, with the same ETag/HEAD/304 behavior as the other endpoints. Failed imports leave the last valid snapshot available. GitHub downloads use a separate HTTP client and do not consume the WFM request budget.

## Docker / Coolify

From the project root, after creating `.env`:

```sh
docker compose --project-directory . --env-file .env -f deploy/compose.yaml up -d --build
```

In Coolify, select the **Docker Compose** build pack, keep the base directory at the repository root, and set the Compose location to `/deploy/compose.yaml`. Set `FETCH_INTERVAL_MINUTES` in Coolify if needed; no `.env` file is required there.

To make the endpoint public through **Domains for app** (the `lucrum` service):

1. Point your domain's DNS to the Coolify server.
2. Enter `https://api.example.com:3100` in the Domains field, replacing the example hostname with your own. Use just the domain and port, without the endpoint path.
3. Save and redeploy, then open `https://api.example.com/warframe/v2/wfm-items`.

The `:3100` in Coolify's domain setting selects the **container port**; public HTTPS requests use port 443, so omit `:3100` from the public URL. The Go server listens on all interfaces (`:3100`), allowing Coolify's proxy to reach it. See [Coolify's domain routing documentation](https://coolify.io/docs/core/networking/domains#route-to-a-port-or-path). The domain root `/` returns `404`; use the full endpoint path above.

The build context is the repository root. The local command explicitly sets `--project-directory .` to match Coolify's path resolution.

The named volume preserves `/data` across container replacements. Run one instance against that volume. The container runs as a non-root user; if replacing the named volume with a bind mount, make its directory writable by UID 10001. Logs go to standard output/error. HTTPS for upstream requests uses the container's CA certificates.

## Code map

`cmd/lucrum` starts and stops the app. `internal/items` handles WFM catalogue/configuration, `internal/tradeable` handles statistics passes, `internal/warframedata` imports WFCD releases, `internal/upstream` shares WFM rate limiting, and `internal/snapshot` publishes and serves files. `deploy` contains the container setup. Only extracted statistics and scheduling metadata remain in memory between passes; complete upstream histories and the WFCD import map are released after processing.

There are no tests. Basic development checks are `go build ./...` and `go vet ./...`.
