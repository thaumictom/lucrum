use std::time::Duration;

use anyhow::{Context, Result};
use chrono::{SecondsFormat, Utc};
use reqwest::blocking::Client;

use crate::types::{Dictionary, ItemsResponse, TradeableItem};

const ITEMS_ENDPOINT: &str = "https://api.warframe.market/v2/items";

pub fn build_dictionary() -> Result<Dictionary> {
    let client = Client::builder()
        .timeout(Duration::from_secs(60))
        .build()
        .context("failed to create HTTP client")?;

    let response = client
        .get(ITEMS_ENDPOINT)
        .send()
        .context("failed to request items")?
        .error_for_status()
        .context("items request returned an error status")?;

    let payload: ItemsResponse = response
        .json()
        .context("failed to parse items response JSON")?;

    let tradeable_items = payload
        .data
        .into_iter()
        .map(|item| TradeableItem {
            // Fallback keeps output valid even if English localization is missing.
            name: item.i18n.en.name.unwrap_or_else(|| item.slug.clone()),
            slug: item.slug,
            tags: item.tags,
            vaulted: item.vaulted,
        })
        .collect();

    Ok(Dictionary {
        last_fetched_at: Utc::now().to_rfc3339_opts(SecondsFormat::Nanos, true),
        tradeable_items,
    })
}
