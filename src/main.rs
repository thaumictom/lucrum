mod api;
mod cache;
mod models;

use anyhow::Result;
use std::path::Path;

fn main() -> Result<()> {
    let cache_path = Path::new("data/warframe_items.json");
    let cache = cache::ensure_tradeable_items_cache(cache_path)?;

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

    Ok(())
}
