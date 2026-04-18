//! Manages the local cache of tradeable item slugs.
//!
//! The cache is a JSON file that stores the full list of tradeable slugs along
//! with a timestamp.  It is refreshed only when missing or older than 72 hours.

use std::fs;
use std::path::Path;

use anyhow::{Context, Result};
use chrono::Utc;
use serde_json::Value;

use crate::api::fetch_tradeable_item_slugs;
use crate::logging::{log_info, log_warn};
use crate::models::TradeableItemsCache;

/// Tracks whether the on-disk JSON already uses the current field name.
struct LoadedCache {
    cache: TradeableItemsCache,
    /// `true` when the file already has `"tradeable_items"` (not the legacy `"data"` key).
    is_normalized: bool,
}

/// Ensure the cache exists and refresh it only when it is missing or stale.
pub fn ensure_tradeable_items_cache(path: &Path) -> Result<TradeableItemsCache> {
    log_info(
        "cache",
        &format!("Checking tradeable item cache at {}.", path.display()),
    );

    if let Some(loaded) = load_cache(path)? {
        let age_hours = Utc::now()
            .signed_duration_since(loaded.cache.last_fetched_at)
            .num_hours()
            .max(0);

        if !loaded.cache.is_stale() {
            log_info(
                "cache",
                &format!(
                    "Using existing cache (items={}, age={}h, last_fetched_at={}).",
                    loaded.cache.tradeable_items.len(),
                    age_hours,
                    loaded.cache.last_fetched_at
                ),
            );

            // Rewrite old cache files so future reads use the new field name.
            if !loaded.is_normalized {
                log_info(
                    "cache",
                    "Normalizing legacy cache format to use tradeable_items field.",
                );
                write_cache(path, &loaded.cache)?;
            }
            return Ok(loaded.cache);
        }

        log_warn(
            "cache",
            &format!(
                "Cache is stale (age={}h, last_fetched_at={}); refreshing from API.",
                age_hours, loaded.cache.last_fetched_at
            ),
        );
    } else {
        log_warn("cache", "Cache file not found; refreshing from API.");
    }

    log_info("cache", "Fetching tradeable item list from API.");
    let cache = TradeableItemsCache {
        last_fetched_at: Utc::now(),
        tradeable_items: fetch_tradeable_item_slugs()?,
    };

    write_cache(path, &cache)?;
    log_info(
        "cache",
        &format!(
            "Cache refreshed with {} tradeable items.",
            cache.tradeable_items.len()
        ),
    );

    Ok(cache)
}

/// Attempt to load an existing cache file, detecting the legacy field name.
fn load_cache(path: &Path) -> Result<Option<LoadedCache>> {
    if !path.exists() {
        return Ok(None);
    }

    let raw = fs::read_to_string(path)
        .with_context(|| format!("failed to read cache file at {}", path.display()))?;
    let json: Value = serde_json::from_str(&raw)
        .with_context(|| format!("failed to parse cache file at {}", path.display()))?;

    // The file is already normalized if it uses `tradeable_items` instead of `data`.
    let is_normalized = json.get("tradeable_items").is_some();

    let cache: TradeableItemsCache = serde_json::from_value(json)
        .with_context(|| format!("failed to deserialize cache file at {}", path.display()))?;

    Ok(Some(LoadedCache {
        cache,
        is_normalized,
    }))
}

/// Serialize and write the cache, creating parent directories as needed.
fn write_cache(path: &Path, cache: &TradeableItemsCache) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .with_context(|| format!("failed to create cache directory {}", parent.display()))?;
    }
    let json = serde_json::to_string_pretty(cache).context("failed to serialize cache")?;
    fs::write(path, format!("{json}\n"))
        .with_context(|| format!("failed to write cache file at {}", path.display()))?;
    Ok(())
}
