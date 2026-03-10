mod api;
mod cache;
mod models;
mod statistics;

use anyhow::Result;
use std::path::Path;

fn main() -> Result<()> {
    let run_start = chrono::Utc::now();
    let cache_path = Path::new("data/warframe_items.json");
    let cache = cache::ensure_tradeable_items_cache(cache_path)?;
    let statistics_path = Path::new("data/warframe_market_statistics.json");

    if cache.is_stale() {
        println!(
            "Cache is stale (last fetched at {}), but will be used until refreshed.",
            cache.last_fetched_at
        );
    } else {
        println!(
            "Cache is fresh (last fetched at {}), skipping API call.",
            cache.last_fetched_at
        );
    }

    println!(
        "Stored {} tradeable item slugs in {}",
        cache.tradeable_items.len(),
        cache_path.display()
    );

    let scraped_items = statistics::scrape_and_write_statistics(
        &cache.tradeable_items,
        statistics_path,
        run_start,
    )?;

    println!(
        "Stored market statistics for {} items in {}",
        scraped_items,
        statistics_path.display()
    );

    Ok(())
}
