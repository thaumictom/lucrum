# Lucrum

Rust CLI that collects item statistics from [warframe.market](https://warframe.market) and writes them to local JSON files.

## What it does

1. Loads the local tradeable-item cache from `data/items.json`.
2. Refreshes that cache only when missing or older than 24 hours.
3. Scrapes per-item market statistics.
4. Writes results to `data/market_statistics.json`.

## Requirements

- Rust toolchain (stable)
- Internet access (calls the public Warframe Market API)

## Run

From the project root:

```powershell
cargo run --release
```

Optional: control request rate (default is `2.5` requests/sec):

```powershell
$env:LUCRUM_REQUESTS_PER_SECOND = "3"
cargo run --release
```

## Output files

- `data/items.json`: cached list of tradeable item slugs + fetch timestamp
- `data/market_statistics.json`: full scrape run with
  - `run_start`
  - `run_end`
  - `last_error`
  - `item_statistics`

## Notes

- The app logs progress and retries to the console.
- If a request fails 3 times for an item, the process stops and writes the last error to `last_error`.
- Existing statistics are reused for recently fetched items (freshness depends on item liquidity).
