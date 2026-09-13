# Coolify deployment

The Compose stack has two services sharing a named data volume:

- `worker` contains Bun and the Lucrum TypeScript source. It idles until a Coolify scheduled task runs it.
- `caddy` serves the generated files under `/warframe/v1/` with CORS, zstd/gzip compression, and five-minute JSON caching.

## Deploy

1. Create a Docker Compose resource in Coolify.
2. Select `deploy/docker-compose.coolify.yml` as the Compose file.
3. Attach a domain to the `caddy` service on container port 80.
4. Deploy the stack.
5. Run `/usr/local/bin/run-lucrum.sh` once in the `worker` service to generate initial data.

Coolify evaluates build paths from the repository root, so the Compose build contexts and Dockerfile paths are intentionally repository-root relative. TLS and public routing are handled by Coolify.

## Schedule

Create a Coolify scheduled task:

- Service: `worker`
- Cron: `0 */6 * * *`
- Command: `/usr/local/bin/run-lucrum.sh`

The wrapper uses `flock` to skip overlapping runs and retries the entire CLI invocation. It defaults to three attempts with 300 seconds between attempts.

## Endpoints

- `/warframe/v1/dictionary.json`
- `/warframe/v1/tradeable_items.json`
- `/healthz`
- `/warframe/v1/healthz`

## Environment variables

| Variable                     |                  Default | Meaning                                       |
| ---------------------------- | -----------------------: | --------------------------------------------- |
| `LUCRUM_REQUESTS_PER_SECOND` |                    `2.5` | Request-start rate                            |
| `LUCRUM_FETCH_OFFSET`        |                      `0` | No skip; positive values skip that many slugs |
| `LUCRUM_FETCH_LIMIT`         |                     `20` | Selected slug limit; `0` is unlimited         |
| `MAX_ATTEMPTS`               |                      `3` | Whole-run attempts in the wrapper             |
| `RETRY_SECONDS`              |                    `300` | Delay between whole-run attempts              |
| `LOCK_FILE`                  | `/app/data/.lucrum.lock` | Wrapper lock path                             |

Generated data lives at `/app/data` in the worker and `/srv/data` in Caddy. The worker must run with `/app` as its working directory because CLI paths are relative.

## Updating Warframe item data

`@wfcd/items` publishes new datasets as Warframe changes. Run `bun run update:items`, commit the updated `package.json` and `bun.lock`, and redeploy the worker. The next scheduled run reapplies component-to-set mappings even if `dictionary.json` is still within its 24-hour API cache window.
