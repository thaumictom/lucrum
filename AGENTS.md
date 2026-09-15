# Working on Lucrum

Lucrum is a Go service with no third-party Go dependencies.

## Layout

- Keep main.go limited to configuration, startup, and shutdown.
- Put all other Go code and tests under internal/.
- clients owns upstream HTTP, retry behavior, and the shared market limiter.
- normalize owns canonical WFCD records and statistics projection.
- storage owns cache state, atomic publication, and file validators.
- workers schedules refreshes; httpapi serves existing snapshots.
- config reads process environment variables. Compose loads .env.

## Invariants

- Apply contextual relationship handlers before generic uniqueName extraction,
  including while indexing source data.
- Follow components, abilities, and required items as dependencies.
- Other references are associations: publish scalar display metadata only.
  Promote a display record if it later becomes a root or dependency.
- Exactly one canonical record exists per gameRef. Nested item documents,
  uniqueName, patchlog fields, and wikiAvailable must not be published.
- Preserve rich knowledge and unknown fields in full records. Keep market
  contracts small and explicit.
- Use exact nonempty gameRef for knowledge joins and market IDs for cache identity.
- All upstream market calls, including retries, share pacing and use PC,
  crossplay=true, and English headers.
- Closed timestamps shift forward 24 hours; live timestamps shift forward one
  hour. Public statistics retain all fields except datetime and id.
- UTC rollover changes date buckets, never last_fetched_at.
- Never overwrite live JSON in place. Write, validate, sync, and rename a
  same-directory temporary file. Keep HTTP validators paired with the file.
- Compute ETags at publication/startup, never by scanning files per HTTP request.
- One running instance owns each data directory.

## Style and checks

Prefer small functions and concrete types. Explain non-obvious behavior in
comments, especially timestamp shifts, relationship context, and concurrency.
Use standard-library tools; avoid frameworks and unnecessary abstractions.

Run:

    gofmt -w main.go internal
    go test -race ./...
    go vet ./...

Tests should cover behavior with fixtures, httptest, temporary directories, and
controlled times. Do not make normal tests depend on live upstream services.
The optional integration smoke test is documented in README.md.
