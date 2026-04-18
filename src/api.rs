//! Warframe Market API client.

use anyhow::{Context, Result, bail};

use crate::logging::{log_info, log_warn};
use crate::models::MarketItemsResponse;

const ITEMS_URL: &str = "https://api.warframe.market/v2/items";

/// Fetch the public item list and keep only the slug for each item.
pub fn fetch_tradeable_item_slugs() -> Result<Vec<String>> {
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

    let slugs: Vec<String> = response.data.into_iter().map(|item| item.slug).collect();
    log_info(
        "api",
        &format!(
            "Received {} tradeable item slugs from Warframe Market.",
            slugs.len()
        ),
    );

    Ok(slugs)
}
