use anyhow::{Context, Result};

use crate::models::MarketItemsResponse;

const ITEMS_URL: &str = "https://api.warframe.market/v2/items";

// Fetch the public item list and keep only the slug for each item.
pub fn fetch_tradeable_item_slugs() -> Result<Vec<String>> {
    let response = reqwest::blocking::get(ITEMS_URL)
        .context("failed to call Warframe Market items endpoint")?
        .error_for_status()
        .context("Warframe Market returned a non-success status")?
        .json::<MarketItemsResponse>()
        .context("failed to deserialize Warframe Market response")?;

    Ok(response.data.into_iter().map(|item| item.slug).collect())
}
