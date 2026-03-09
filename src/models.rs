use chrono::{DateTime, Duration, Utc};
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TradeableItemsCache {
    pub last_fetched_at: DateTime<Utc>,
    #[serde(alias = "data")]
    pub tradeable_items: Vec<String>,
}

impl TradeableItemsCache {
    // Treat cached data as stale once it is older than 72 hours.
    pub fn is_stale(&self) -> bool {
        self.last_fetched_at + Duration::hours(72) <= Utc::now()
    }
}

#[derive(Debug, Deserialize)]
pub struct MarketItemsResponse {
    pub data: Vec<MarketItem>,
}

#[derive(Debug, Deserialize)]
pub struct MarketItem {
    pub slug: String,
}
