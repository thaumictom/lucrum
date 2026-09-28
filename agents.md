# Working on Lucrum

- Keep Go readable for a beginner with a TypeScript background. Explain non-obvious Go concepts and why concurrency or storage decisions matter; use occasional accurate TypeScript comparisons.
- Keep startup/shutdown in `cmd/lucrum`, WFM catalogue behavior in `internal/items`, statistics in `internal/tradeable`, WFCD release imports in `internal/warframedata`, shared disk/HTTP behavior in `internal/snapshot`, upstream limiting in `internal/upstream`, and deployment in `deploy`. Avoid speculative interfaces, frameworks, or extra root files.
- Use the standard library where practical. `godotenv` is the only current external dependency.
- The upstream URL is `https://api.warframe.market/v2/items`. The public path is `/warframe/v2/wfm-items` and port 3100 is fixed; do not add a port environment variable.
- Preserve unknown item fields. Remove only `id` and `i18n`, deriving `name` from `i18n.en.name`. Keep `last_fetched_at` unchanged for unchanged upstream responses.
- Use the upstream SHA-256 for change detection and the generated-file SHA-256 for the quoted HTTP ETag. Keep each served file and ETag consistent.
- Publish through a temporary file and same-directory atomic rename on Linux. Retain the last valid version after refresh failures. Assume one writer per data directory.
- Serve `/warframe/v2/tradeable-items` with slug, name, gameRef, liquidity, three statistics arrays, and per-item last_fetched_at. Select the previous two UTC days from closed 90days and the latest sell timestamp from live 48hours; strip only id and datetime. Preserve duplicate variants.
- Statistics passes run at :15 UTC, including the first pass after startup. Skip a tick if a pass is active; build an unsorted due queue from the current catalogue. Liquidity 0–20 / 21–150 / above 150 means 24h / 6h / 1h. Failed attempts preserve public data and timestamps and wait their cache interval; persist deadlines before dispatch.
- Share WFM_REQUESTS_PER_SECOND (default 2.5, maximum 3) across all upstream request starts, including redirects. Allow at most eight statistics requests in flight. On 429 pause starts for Retry-After or 30 seconds; do not retry the slug within the pass.
- Publish dirty statistics every max(1, ceil(WFM_REQUESTS_PER_SECOND * 60)) completed statistics requests, counting failures, and at pass completion/shutdown. There is no timed progress publication. Retain dirty state after publication errors.
- DEBUG defaults to false; when enabled, log every fetch and scheduling skip. Keep ordinary logs concise.
- Import the latest published WFCD/warframe-items release at startup and every FETCH_INTERVAL_MINUTES. Skip unchanged versions when the saved snapshot and metadata agree. Read data/json from that release tag, exclude i18n.json, and stream categories without keeping source files.
- Serve WFCD data at `/warframe/v2/items` as a root map keyed by uniqueName. Keep top-level records with tradable == true OR masterable == true and all their recursive components. Replace components with arrays of uniqueName strings and remove each flattened entry's own uniqueName; preserve unrelated nested data.
- For duplicate WFCD keys, retained top-level fields take precedence over component copies; otherwise prefer the first definition in alphabetical file order. Fill missing fields and merge additional component links so partial copies cannot hide sub-recipes. Persist version and file hash in items.meta.json only after successful publication. Failed imports keep the last valid snapshot. GitHub requests do not use the WFM limiter.
- Stream HTTP responses from disk. Retain only extracted tradeable statistics and scheduling metadata in memory; discard complete upstream histories after each fetch.
- Do not add tests or a test framework. Format changed Go code, run `go build ./...` and `go vet ./...`, and manually check affected behavior.
- Keep `readme.md` short and update it when configuration, endpoint behavior, or deployment changes.
