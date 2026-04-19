mod cache;
mod fetch;
mod output;
mod types;

use anyhow::Result;

use crate::cache::is_cache_fresh;
use crate::fetch::build_dictionary;
use crate::output::write_dictionary;

const DICTIONARY_PATH: &str = "data/dictionary.json";
const CACHE_MAX_AGE_HOURS: i64 = 72;

fn main() -> Result<()> {
    if is_cache_fresh(DICTIONARY_PATH, CACHE_MAX_AGE_HOURS) {
        println!(
            "Skipping fetch: {} is newer than {} hours.",
            DICTIONARY_PATH, CACHE_MAX_AGE_HOURS
        );
        return Ok(());
    }

    let dictionary = build_dictionary()?;
    write_dictionary(DICTIONARY_PATH, &dictionary)?;

    println!(
        "Wrote {} tradeable items to {}",
        dictionary.tradeable_items.len(),
        DICTIONARY_PATH
    );

    Ok(())
}
