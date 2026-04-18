mod api;
mod cache;
mod logging;
mod models;
mod scraper;
mod statistics;

use anyhow::Result;
use chrono::Utc;
use std::path::Path;

use crate::logging::{init_run_logging, log_error, log_info};

fn main() -> Result<()> {
    let run_start = Utc::now();
    let run_id = init_run_logging(run_start);
    log_info("main", &format!("Starting lucrum run {run_id}."));

    let result = run(run_start);
    match result {
        Ok(()) => {
            let elapsed_seconds = Utc::now().signed_duration_since(run_start).num_seconds();
            log_info(
                "main",
                &format!("Run completed successfully in {elapsed_seconds}s. run_id={run_id}."),
            );
            Ok(())
        }
        Err(error) => {
            log_error("main", &format!("Run failed: {error:#}"));
            Err(error)
        }
    }
}

fn run(run_start: chrono::DateTime<Utc>) -> Result<()> {
    let cache_path = Path::new("data/warframe_items.json");
    let statistics_path = Path::new("data/warframe_market_statistics.json");

    log_info(
        "main",
        &format!("Ensuring tradeable item cache at {}.", cache_path.display()),
    );
    let cache = cache::ensure_tradeable_items_cache(cache_path)?;

    log_info(
        "main",
        &format!(
            "Cache ready: {} tradeable items, last_fetched_at={}",
            cache.tradeable_items.len(),
            cache.last_fetched_at
        ),
    );

    log_info(
        "main",
        &format!(
            "Starting statistics scrape to output path {}.",
            statistics_path.display()
        ),
    );
    let scraped_items = statistics::scrape_and_write_statistics(
        &cache.tradeable_items,
        statistics_path,
        run_start,
    )?;

    log_info(
        "main",
        &format!(
            "Statistics write complete. fetched_items={} output_path={}",
            scraped_items,
            statistics_path.display()
        ),
    );

    Ok(())
}
