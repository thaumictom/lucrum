//! Warframe Market API client.

use std::collections::HashMap;

use anyhow::{Context, Result, bail};

use crate::logging::{log_info, log_warn};
use crate::models::{MarketItem, MarketItemsResponse};

const ITEMS_URL: &str = "https://api.warframe.market/v2/items";

/// Fetch the public item list and keep only the slug for each item.
pub fn fetch_tradeable_item_slugs() -> Result<Vec<String>> {
    let items = fetch_tradeable_items()?;
    let slugs: Vec<String> = items.into_iter().map(|item| item.slug).collect();
    log_info(
        "api",
        &format!(
            "Received {} tradeable item slugs from Warframe Market.",
            slugs.len()
        ),
    );

    Ok(slugs)
}

/// Fetch the public item list and return slug -> English display name.
pub fn fetch_tradeable_item_names() -> Result<HashMap<String, String>> {
    let items = fetch_tradeable_items()?;
    let names = items
        .into_iter()
        .map(|item| {
            let name = item
                .i18n
                .get("en")
                .map(|entry| entry.name.as_str())
                .filter(|name| !name.is_empty())
                .unwrap_or(item.slug.as_str())
                .to_owned();
            (item.slug, name)
        })
        .collect::<HashMap<_, _>>();

    log_info(
        "api",
        &format!(
            "Received {} tradeable item names from Warframe Market.",
            names.len()
        ),
    );

    Ok(names)
}

fn fetch_tradeable_items() -> Result<Vec<MarketItem>> {
    log_info(
        "api",
        &format!("Requesting tradeable item list from {ITEMS_URL}."),
    );

    let response = reqwest::blocking::get(ITEMS_URL)
        .context("failed to call Warframe Market items endpoint")?;
    let status = response.status();

    if !status.is_success() {
        let reason = status.canonical_reason().unwrap_or("unknown status");
        log_warn(
            "api",
            &format!(
                "Tradeable item request failed with HTTP {} ({reason}).",
                status.as_u16()
            ),
        );
        bail!(
            "Warframe Market returned HTTP {} ({reason})",
            status.as_u16()
        );
    }

    let response = response
        .json::<MarketItemsResponse>()
        .context("failed to deserialize Warframe Market response")?;

    Ok(response.data)
}
