# Working on Lucrum

- Keep Go readable for a beginner with a TypeScript background. Explain non-obvious Go concepts and why concurrency or storage decisions matter; use occasional accurate TypeScript comparisons.
- Keep startup/shutdown in `cmd/lucrum`, item behavior in `internal/items`, and deployment in `deploy`. Add packages when a concrete feature needs them; avoid speculative interfaces, frameworks, or extra root files.
- Use the standard library where practical. `godotenv` is the only current external dependency.
- The upstream URL is `https://api.warframe.market/v2/items`. The public path is `/warframe/v1/wfm-items` and port 3100 is fixed; do not add a port environment variable.
- Preserve unknown item fields. Remove only `id` and `i18n`, deriving `name` from `i18n.en.name`. Keep `last_fetched_at` unchanged for unchanged upstream responses.
- Use the upstream SHA-256 for change detection and the generated-file SHA-256 for the quoted HTTP ETag. Keep each served file and ETag consistent.
- Publish through a temporary file and same-directory atomic rename on Linux. Retain the last valid version after refresh failures. Assume one writer per data directory.
- Stream HTTP responses from disk. Avoid retaining the catalogue in memory between refreshes.
- Do not add tests or a test framework. Format changed Go code, run `go build ./...` and `go vet ./...`, and manually check affected behavior.
- Keep `readme.md` short and update it when configuration, endpoint behavior, or deployment changes.
