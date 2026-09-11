mod cache;
mod fetch;
mod output;
mod tradeable_items;
mod types;

use std::env;

use anyhow::{Context, Result, bail};

use crate::cache::is_cache_fresh;
use crate::fetch::build_dictionary;
use crate::output::{write_dictionary, write_tradeable_items};
use crate::tradeable_items::build_tradeable_items_run;

const DICTIONARY_PATH: &str = "data/dictionary.json";
const TRADEABLE_ITEMS_PATH: &str = "data/tradeable_items.json";
const CACHE_MAX_AGE_HOURS: i64 = 24;
const DEFAULT_REQUESTS_PER_SECOND: f64 = 2.5;
const DEFAULT_DEBUG_FETCH_OFFSET: Option<usize> = None;
const DEFAULT_DEBUG_FETCH_LIMIT: Option<usize> = None;

fn main() -> Result<()> {
    if is_cache_fresh(DICTIONARY_PATH, CACHE_MAX_AGE_HOURS) {
        println!(
            "Skipping fetch: {} is newer than {} hours.",
            DICTIONARY_PATH, CACHE_MAX_AGE_HOURS
        );
    } else {
        let dictionary = build_dictionary()?;
        write_dictionary(DICTIONARY_PATH, &dictionary)?;

        println!(
            "Wrote {} tradeable items to {}",
            dictionary.tradeable_items.len(),
            DICTIONARY_PATH
        );
    }

    let requests_per_second = configured_requests_per_second()?;
    let fetch_offset = configured_fetch_offset()?;
    let fetch_limit = configured_fetch_limit()?;
    let run = build_tradeable_items_run(
        DICTIONARY_PATH,
        TRADEABLE_ITEMS_PATH,
        requests_per_second,
        fetch_offset,
        fetch_limit,
    )?;
    write_tradeable_items(TRADEABLE_ITEMS_PATH, &run)?;

    println!(
        "Wrote {} item snapshots to {}",
        run.tradeable_items.len(),
        TRADEABLE_ITEMS_PATH
    );

    Ok(())
}

fn configured_requests_per_second() -> Result<f64> {
    match env::var("LUCRUM_REQUESTS_PER_SECOND") {
        Ok(value) => {
            let parsed = value
                .parse::<f64>()
                .context("LUCRUM_REQUESTS_PER_SECOND must be a number")?;
            if !(parsed.is_finite() && parsed > 0.0) {
                bail!("LUCRUM_REQUESTS_PER_SECOND must be greater than zero");
            }
            Ok(parsed)
        }
        Err(env::VarError::NotPresent) => Ok(DEFAULT_REQUESTS_PER_SECOND),
        Err(error) => Err(error).context("failed to read LUCRUM_REQUESTS_PER_SECOND"),
    }
}

fn configured_fetch_offset() -> Result<Option<usize>> {
    match env::var("LUCRUM_FETCH_OFFSET") {
        Ok(value) => {
            let parsed = value
                .parse::<usize>()
                .context("LUCRUM_FETCH_OFFSET must be an integer")?;
            if parsed == 0 {
                Ok(None)
            } else {
                Ok(Some(parsed))
            }
        }
        Err(env::VarError::NotPresent) => Ok(DEFAULT_DEBUG_FETCH_OFFSET),
        Err(error) => Err(error).context("failed to read LUCRUM_FETCH_OFFSET"),
    }
}

fn configured_fetch_limit() -> Result<Option<usize>> {
    match env::var("LUCRUM_FETCH_LIMIT") {
        Ok(value) => {
            let parsed = value
                .parse::<usize>()
                .context("LUCRUM_FETCH_LIMIT must be an integer")?;
            if parsed == 0 {
                Ok(None)
            } else {
                Ok(Some(parsed))
            }
        }
        Err(env::VarError::NotPresent) => Ok(DEFAULT_DEBUG_FETCH_LIMIT),
        Err(error) => Err(error).context("failed to read LUCRUM_FETCH_LIMIT"),
    }
}
