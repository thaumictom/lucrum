# Deployment Guide (Coolify + Caddy)

This folder contains everything needed to:

- host generated JSON with Caddy
- regenerate JSON on a 6-hour schedule
- avoid overlapping runs with a lock file

## Files

- `docker-compose.coolify.yml`: two services (`caddy`, `worker`) and shared volumes
- `Dockerfile.worker`: builds the Rust binary and includes scheduler script
- `run-lucrum.sh`: lock + retry wrapper around the Rust binary
- `Caddyfile`: static JSON hosting config with CORS and gzip/zstd
- `.env.example`: optional env overrides for scrape behavior

## Coolify Setup

1. Create a new Docker Compose resource in Coolify.
2. Set compose file path to `deploy/docker-compose.coolify.yml`.
3. Add a domain to the `caddy` service in Coolify (target container port `80`).
4. Deploy the stack.

Coolify will handle TLS and public routing. Caddy only serves internal HTTP on port 80.

## Coolify Path Resolution Note

Coolify runs Docker Compose with the repository root as the project directory.
Because of that, relative paths in `deploy/docker-compose.coolify.yml` should be
repo-root relative, for example:

- `build.context: .`
- `dockerfile: deploy/Dockerfile.worker`
- `./deploy/Caddyfile:/etc/caddy/Caddyfile:ro`

## Initial Data Generation

After first deploy, run this command once on the `worker` service:

~~~sh
/usr/local/bin/run-lucrum.sh
~~~

This will produce data in the shared volume mounted at `/app/data` on the worker and `/srv/data` on Caddy.

## Scheduled Regeneration (Every 6 Hours)

Create a Coolify Scheduled Task with:

- Service: `worker`
- Cron: `0 */6 * * *`
- Command:

~~~sh
/usr/local/bin/run-lucrum.sh
~~~

## Hosted Endpoints

Once deployed, your domain will serve:

- `/warframe_items.json`
- `/warframe_market_statistics.json`

Optional health endpoint:

- `/healthz`

## Optional Tuning

Adjust values via environment variables in Coolify:

- `LUCRUM_REQUESTS_PER_SECOND` (default `2.5`)
- `MAX_ATTEMPTS` (default `3`)
- `RETRY_SECONDS` (default `300`)

## Troubleshooting Build Failures

If Coolify fails on `cargo build --release` with exit code `101`:

- ensure the latest `deploy/Dockerfile.worker` is deployed
- check full build logs for the first Rust compiler error above the final `exit code: 101` line
- verify your deployment server has enough RAM (Rust builds can fail under memory pressure)

The worker Dockerfile already applies:

- lockfile build (`cargo build --release --locked`)
- single-job compile (`CARGO_BUILD_JOBS=1` and `-j 1`)
- crates.io sparse protocol and retries
