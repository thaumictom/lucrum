//! Orchestrates the market-statistics scrape: configuration, caching policy,
//! JSON persistence, and the top-level scrape loop.

use std::collections::HashMap;
use std::env;
use std::fs;
use std::path::Path;

use anyhow::{Context, Result, anyhow, bail};
use chrono::{DateTime, Duration, Utc};

use crate::api::fetch_tradeable_item_names;
use crate::logging::{log_error, log_info};
use crate::models::{ItemStatistics, StatisticsRun};
use crate::scraper::{StatisticsScraper, sanitize_item_statistics};

const DEFAULT_REQUESTS_PER_SECOND: f64 = 2.5;
const MAX_ATTEMPTS: usize = 3;
const PROGRESS_LOG_INTERVAL: usize = 100;

// ── Public entry point ────────────────────────────────────────────────────

/// Scrape every item slug and write the aggregated market statistics JSON to disk.
pub fn scrape_and_write_statistics(
    slugs: &[String],
    output_path: &Path,
    run_start: DateTime<Utc>,
) -> Result<usize> {
    let rps = configured_requests_per_second()?;
    let mut scraper = StatisticsScraper::new(rps)?;
    let cached = load_cached_statistics(output_path)?;
    let cached_count = cached.len();
    let item_names = fetch_tradeable_item_names()?;
    let mut scraped = 0usize;
    let mut skipped = 0usize;

    initialize_run(output_path, run_start)?;

    log_info(
        "statistics",
        &format!(
            "Starting market statistics scrape: items={} cached_items={} requests_per_second={rps:.2} output_path={}",
            slugs.len(),
            cached_count,
            output_path.display()
        ),
    );

    for (i, slug) in slugs.iter().enumerate() {
        let item_name = item_names
            .get(slug)
            .map(String::as_str)
            .unwrap_or(slug.as_str());

        if i % PROGRESS_LOG_INTERVAL == 0 || i + 1 == slugs.len() {
            let percent = ((i + 1) as f64 / slugs.len() as f64) * 100.0;
            log_info(
                "statistics",
                &format!(
                    "Processing item {}/{} ({percent:.1}%): {slug}",
                    i + 1,
                    slugs.len()
                ),
            );
        }

        // Re-use cached data when the item was fetched recently enough.
        if let Some(cached_item) = cached.get(slug)
            && should_skip_fetch(cached_item, run_start)
        {
            let mut cached_item = cached_item.clone();
            if cached_item.name.is_empty() {
                cached_item.name = item_name.to_owned();
            }

            let age_minutes = run_start
                .signed_duration_since(cached_item.last_fetched_at)
                .num_minutes()
                .max(0);
            log_info(
                "statistics",
                &format!(
                    "Skipping {slug}; cache hit (liquidity={}, age={}m).",
                    cached_item.liquidity, age_minutes
                ),
            );
            append_item(output_path, run_start, cached_item)?;
            skipped += 1;
            continue;
        }

        match scraper.fetch_with_retry(slug, item_name) {
            Ok(stats) => {
                append_item(output_path, run_start, stats)?;
                scraped += 1;
            }
            Err(error) => {
                let msg = format!("{slug}: {error}");
                if let Err(e) = record_error(output_path, run_start, &msg) {
                    log_error(
                        "statistics",
                        &format!("Failed to write partial statistics output after error: {e}"),
                    );
                }
                log_error(
                    "statistics",
                    &format!("Aborting scrape after {MAX_ATTEMPTS} failed attempts for {msg}."),
                );
                return Err(anyhow!(
                    "Shutting down after {MAX_ATTEMPTS} failed attempts for {msg}"
                ));
            }
        }
    }

    finalize_run(output_path, run_start)?;
    let elapsed_seconds = Utc::now().signed_duration_since(run_start).num_seconds();
    log_info(
        "statistics",
        &format!(
            "Finished market statistics scrape: fetched_items={scraped} reused_cached_items={skipped} elapsed_seconds={elapsed_seconds} output_path={}",
            output_path.display()
        ),
    );

    Ok(scraped)
}

// ── Caching policy ────────────────────────────────────────────────────────

/// Decide whether a cached item is recent enough to skip re-fetching.
/// Higher-liquidity items are refreshed more aggressively.
fn should_skip_fetch(item: &ItemStatistics, now: DateTime<Utc>) -> bool {
    let max_age = match item.liquidity {
        l if l > 200 => Duration::hours(1),
        l if l > 100 => Duration::hours(2),
        l if l > 20 => Duration::hours(6),
        _ => Duration::hours(18),
    };
    now.signed_duration_since(item.last_fetched_at) < max_age
}

// ── Configuration ─────────────────────────────────────────────────────────

/// Read the request rate from `LUCRUM_REQUESTS_PER_SECOND`, falling back to 2.0.
fn configured_requests_per_second() -> Result<f64> {
    match env::var("LUCRUM_REQUESTS_PER_SECOND") {
        Ok(val) => {
            let parsed = val
                .parse::<f64>()
                .context("LUCRUM_REQUESTS_PER_SECOND must be a valid number")?;
            if !(parsed.is_finite() && parsed > 0.0) {
                bail!("LUCRUM_REQUESTS_PER_SECOND must be greater than zero");
            }
            Ok(parsed)
        }
        Err(env::VarError::NotPresent) => Ok(DEFAULT_REQUESTS_PER_SECOND),
        Err(e) => Err(anyhow!("failed to read LUCRUM_REQUESTS_PER_SECOND: {e}")),
    }
}

// ── JSON persistence ──────────────────────────────────────────────────────
//
// NOTE: `append_item` re-reads and rewrites the entire JSON file on every
// item so that partial progress is always persisted to disk.  This is an
// intentional durability trade-off — no data is lost if the process crashes,
// at the cost of O(n²) file I/O over a full scrape run.

/// Write an empty run to disk so that partial progress is always recoverable.
fn initialize_run(path: &Path, start: DateTime<Utc>) -> Result<()> {
    write_run(
        path,
        &StatisticsRun {
            run_start: start,
            run_end: start,
            last_error: String::new(),
            item_statistics: Vec::new(),
        },
    )
}

/// Append one item's statistics to the on-disk run file.
fn append_item(path: &Path, run_start: DateTime<Utc>, mut item: ItemStatistics) -> Result<()> {
    sanitize_item_statistics(&mut item);
    let mut run = read_run(path, run_start)?;
    run.run_end = Utc::now();
    run.last_error.clear();
    run.item_statistics.push(item);
    write_run(path, &run)
}

/// Record the last error encountered during a scrape run.
fn record_error(path: &Path, run_start: DateTime<Utc>, error: &str) -> Result<()> {
    let mut run = read_run(path, run_start)?;
    run.run_end = Utc::now();
    run.last_error = error.to_owned();
    write_run(path, &run)
}

/// Mark the run as complete by updating its end timestamp.
fn finalize_run(path: &Path, run_start: DateTime<Utc>) -> Result<()> {
    let mut run = read_run(path, run_start)?;
    run.run_end = Utc::now();
    run.last_error.clear();
    write_run(path, &run)
}

/// Load previous item statistics keyed by slug for cache-hit lookups.
fn load_cached_statistics(path: &Path) -> Result<HashMap<String, ItemStatistics>> {
    let Some(run) = read_existing_run(path)? else {
        return Ok(HashMap::new());
    };
    Ok(run
        .item_statistics
        .into_iter()
        .map(|s| (s.item.clone(), s))
        .collect())
}

fn read_run(path: &Path, run_start: DateTime<Utc>) -> Result<StatisticsRun> {
    Ok(read_existing_run(path)?.unwrap_or(StatisticsRun {
        run_start,
        run_end: run_start,
        last_error: String::new(),
        item_statistics: Vec::new(),
    }))
}

fn read_existing_run(path: &Path) -> Result<Option<StatisticsRun>> {
    if !path.exists() {
        return Ok(None);
    }
    let raw = fs::read_to_string(path)
        .with_context(|| format!("failed to read statistics file at {}", path.display()))?;
    let run = serde_json::from_str(&raw)
        .with_context(|| format!("failed to parse statistics file at {}", path.display()))?;
    Ok(Some(run))
}

fn write_run(path: &Path, run: &StatisticsRun) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).with_context(|| {
            format!("failed to create statistics directory {}", parent.display())
        })?;
    }
    let json =
        serde_json::to_string_pretty(run).context("failed to serialize market statistics")?;
    fs::write(path, format!("{json}\n"))
        .with_context(|| format!("failed to write statistics file at {}", path.display()))?;
    Ok(())
}

// ── Tests ─────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn skips_high_liquidity_items_only_for_one_hour() {
        let now = Utc::now();
        let cached = ItemStatistics {
            item: "secura_dual_cestra".to_owned(),
            name: "Secura Dual Cestra".to_owned(),
            last_fetched_at: now - Duration::minutes(59),
            liquidity: 201,
            statistics_yesterday: json!({}),
            statistics_today: json!({}),
            current_offers: json!({}),
        };

        assert!(should_skip_fetch(&cached, now));
        assert!(!should_skip_fetch(
            &ItemStatistics {
                last_fetched_at: now - Duration::minutes(61),
                ..cached
            },
            now
        ));
    }

    #[test]
    fn skips_low_liquidity_items_for_eighteen_hours() {
        let now = Utc::now();
        let cached = ItemStatistics {
            item: "irradiating_disarm".to_owned(),
            name: "Irradiating Disarm".to_owned(),
            last_fetched_at: now - Duration::hours(17),
            liquidity: 5,
            statistics_yesterday: json!({}),
            statistics_today: json!({}),
            current_offers: json!({}),
        };

        assert!(should_skip_fetch(&cached, now));
        assert!(!should_skip_fetch(
            &ItemStatistics {
                last_fetched_at: now - Duration::hours(19),
                ..cached
            },
            now
        ));
    }
}
