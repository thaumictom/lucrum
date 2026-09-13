use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Deserialize)]
pub struct ItemsResponse {
    pub data: Vec<ApiItem>,
}

#[derive(Debug, Deserialize)]
pub struct ApiItem {
    pub slug: String,
    #[serde(default, rename = "maxRank")]
    pub max_rank: Option<u32>,
    #[serde(default)]
    pub vaulted: Option<bool>,
    #[serde(default)]
    pub ducats: Option<u32>,
    #[serde(default)]
    pub tags: Vec<String>,
    #[serde(default)]
    pub i18n: ApiI18n,
}

#[derive(Debug, Default, Deserialize)]
pub struct ApiI18n {
    #[serde(default)]
    pub en: ApiEnglish,
}

#[derive(Debug, Default, Deserialize)]
pub struct ApiEnglish {
    pub name: Option<String>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct Dictionary {
    pub last_fetched_at: String,
    pub tradeable_items: Vec<TradeableItem>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct TradeableItem {
    pub slug: String,
    pub name: String,
    pub tags: Vec<String>,
    #[serde(rename = "maxRank", skip_serializing_if = "Option::is_none")]
    pub max_rank: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub vaulted: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ducats: Option<u32>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct TradeableItemsRun {
    pub run_start: String,
    #[serde(default)]
    pub run_end: String,
    pub tradeable_items: Vec<TradeableItemSnapshot>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct TradeableItemSnapshot {
    pub slug: String,
    pub last_fetched_at: String,
    pub liquidity: u64,
    pub statistics_yesterday: Vec<Value>,
    pub statistics_today: Vec<Value>,
    pub current_offers: Vec<Value>,
}
