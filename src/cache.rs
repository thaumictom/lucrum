use std::fs;
use std::path::Path;

use chrono::{DateTime, Duration, Utc};
use serde::Deserialize;

#[derive(Debug, Deserialize)]
struct CacheHeader {
    last_fetched_at: DateTime<Utc>,
}

pub fn is_cache_fresh(path: &str, max_age_hours: i64) -> bool {
    let cache_path = Path::new(path);
    if !cache_path.exists() {
        return false;
    }

    let raw = match fs::read_to_string(cache_path) {
        Ok(raw) => raw,
        Err(_) => return false,
    };

    let header: CacheHeader = match serde_json::from_str(&raw) {
        Ok(header) => header,
        Err(_) => return false,
    };

    Utc::now().signed_duration_since(header.last_fetched_at) < Duration::hours(max_age_hours)
}
