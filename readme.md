# Lucrum

A small Go service that fetches `https://api.warframe.market/v2/items` at startup and every 180 minutes, then serves a simplified catalogue on port **3100**.

## Run locally

Install Go 1.27 or newer, then run from the project root:

```sh
cp .env.example .env
go run ./cmd/lucrum
```

Optional `.env` settings (existing environment variables take precedence):

| Variable | Default | Meaning |
| --- | --- | --- |
| `FETCH_INTERVAL_MINUTES` | `180` | Positive whole minutes between fetches |
| `DATA_DIR` | `./data` | Directory for the generated file and hash metadata |

Port 3100 is fixed. Restart the app after changing settings.

## Use the endpoint

```sh
curl -i http://localhost:3100/warframe/v1/wfm-items
# Substitute the quoted ETag returned above:
curl -i -H 'If-None-Match: "your-etag-hash"' http://localhost:3100/warframe/v1/wfm-items
```

The response contains `items` and `last_fetched_at`. Each item keeps its original fields except `id` and `i18n`; `name` comes from `i18n.en.name`. The UTC timestamp records the request start time for the published version. Unchanged upstream responses leave the file and timestamp untouched.

The upstream response SHA-256 detects changes. A separate SHA-256 of the generated file is its ETag. Matching conditional requests return `304` without a body; HEAD is also supported. `Cache-Control: public, no-cache` allows storage but requires revalidation.

Updates atomically replace `wfm-items.json`. Requests stream from disk, and failed refreshes retain the last valid file. Before any valid file exists, the endpoint returns `503`. Failures retry at the next interval. Fetches have a 30-second timeout and a 32 MiB response limit.

## Docker / Coolify

From the project root, after creating `.env`:

```sh
docker compose --project-directory . --env-file .env -f deploy/compose.yaml up -d --build
```

In Coolify, select the **Docker Compose** build pack, keep the base directory at the repository root, and set the Compose location to `/deploy/compose.yaml`. Assign your domain to the `lucrum` service on port **3100**. Set `FETCH_INTERVAL_MINUTES` in Coolify if needed; no `.env` file is required there.

The build context is the repository root. The local command explicitly sets `--project-directory .` to match Coolify's path resolution.

The named volume preserves `/data` across container replacements. Run one instance against that volume. The container runs as a non-root user; if replacing the named volume with a bind mount, make its directory writable by UID 10001. Logs go to standard output/error. HTTPS for upstream requests uses the container's CA certificates.

## Code map

`cmd/lucrum` starts and stops the app. `internal/items` contains configuration, fetching/transformation, disk storage, and HTTP handling. `deploy` contains the container setup. The app keeps only small hash metadata in memory between refreshes; Go manages temporary fetch allocations through garbage collection.

There are no tests. Basic development checks are `go build ./...` and `go vet ./...`.
