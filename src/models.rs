//! Data types shared across the application.

use chrono::{DateTime, Duration, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value;

// ── Item cache ────────────────────────────────────────────────────────────

/// Cached list of tradeable item slugs with a staleness check.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TradeableItemsCache {
    pub last_fetched_at: DateTime<Utc>,
    /// Accepts the legacy field name `"data"` for backwards compatibility.
    #[serde(alias = "data")]
    pub tradeable_items: Vec<String>,
}

impl TradeableItemsCache {
    /// Treat cached data as stale once it is older than 72 hours.
    pub fn is_stale(&self) -> bool {
        self.last_fetched_at + Duration::hours(72) <= Utc::now()
    }
}

// ── Warframe Market API ───────────────────────────────────────────────────

/// Top-level envelope returned by the Warframe Market items endpoint.
#[derive(Debug, Deserialize)]
pub struct MarketItemsResponse {
    pub data: Vec<MarketItem>,
}

/// A single item from the market items list; only the slug is kept.
#[derive(Debug, Deserialize)]
pub struct MarketItem {
    pub slug: String,
}

// ── Statistics ────────────────────────────────────────────────────────────

/// A complete statistics scrape run written to disk.
#[derive(Debug, Serialize, Deserialize)]
pub struct StatisticsRun {
    pub run_start: DateTime<Utc>,
    pub run_end: DateTime<Utc>,
    pub last_error: String,
    pub item_statistics: Vec<ItemStatistics>,
}

/// Aggregated market statistics for a single tradeable item.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ItemStatistics {
    pub item: String,
    pub last_fetched_at: DateTime<Utc>,
    pub liquidity: u64,
    pub statistics_yesterday: Value,
    pub statistics_today: Value,
    #[serde(default)]
    pub current_offers: Value,
}
