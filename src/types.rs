use serde::{Deserialize, Serialize};

#[derive(Debug, Deserialize)]
pub struct ItemsResponse {
    pub data: Vec<ApiItem>,
}

#[derive(Debug, Deserialize)]
pub struct ApiItem {
    pub slug: String,
    #[serde(default)]
    pub vaulted: Option<bool>,
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

#[derive(Debug, Serialize)]
pub struct Dictionary {
    pub last_fetched_at: String,
    pub tradeable_items: Vec<TradeableItem>,
}

#[derive(Debug, Serialize)]
pub struct TradeableItem {
    pub slug: String,
    pub name: String,
    pub tags: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub vaulted: Option<bool>,
}
