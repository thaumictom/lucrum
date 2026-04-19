use std::collections::HashMap;
use std::fs;
use std::thread;
use std::time::{Duration, Instant};

use anyhow::{Context, Result};
use chrono::{DateTime, SecondsFormat, Utc};
use reqwest::blocking::Client;
use serde::Deserialize;
use serde_json::Value;

use crate::types::{Dictionary, TradeableItemSnapshot, TradeableItemsRun};

const STATISTICS_URL_TEMPLATE: &str = "https://api.warframe.market/v1/items/{slug}/statistics";

#[derive(Debug, Deserialize)]
struct StatisticsResponse {
    payload: StatisticsPayload,
}

#[derive(Debug, Default, Deserialize)]
struct StatisticsPayload {
    #[serde(default)]
    statistics_closed: ClosedStatistics,
    #[serde(default)]
    statistics_live: LiveStatistics,
}

#[derive(Debug, Default, Deserialize)]
struct ClosedStatistics {
    #[serde(rename = "90days", default)]
    ninety_days: Vec<Value>,
}

#[derive(Debug, Default, Deserialize)]
struct LiveStatistics {
    #[serde(rename = "48hours", default)]
    forty_eight_hours: Vec<Value>,
}

pub fn build_tradeable_items_run(
    dictionary_path: &str,
    previous_run_path: &str,
    requests_per_second: f64,
    fetch_limit: Option<usize>,
) -> Result<TradeableItemsRun> {
    let dictionary = read_dictionary(dictionary_path)?;
    let cached_by_slug = load_cached_snapshots(previous_run_path)?;
    let run_start = Utc::now();

    let slugs: Vec<String> = dictionary
        .tradeable_items
        .into_iter()
        .map(|item| item.slug)
        .collect();

    let selected_slugs: Vec<String> = match fetch_limit {
        Some(limit) => slugs.into_iter().take(limit).collect(),
        None => slugs,
    };

    let client = Client::builder()
        .timeout(Duration::from_secs(60))
        .build()
        .context("failed to create statistics HTTP client")?;

    let min_interval = Duration::from_secs_f64(1.0 / requests_per_second);
    let mut last_request_started_at: Option<Instant> = None;
    let mut items = Vec::with_capacity(selected_slugs.len());

    for slug in selected_slugs {
        let cached_liquidity = cached_by_slug.get(&slug).map(|item| item.liquidity);
        let decision_time = Utc::now();
        if let Some(cached) = cached_by_slug.get(&slug)
            && should_use_cached(cached, decision_time)
        {
            println!("(skip) slug={} liquidity={}", slug, cached.liquidity);
            items.push(cached.clone());
            continue;
        }

        let decision_liquidity = cached_liquidity
            .map(|value| value.to_string())
            .unwrap_or_else(|| "unknown".to_string());
        println!("(fetch) slug={} liquidity={}", slug, decision_liquidity);

        if let Some(previous_start) = last_request_started_at {
            let elapsed = previous_start.elapsed();
            if elapsed < min_interval {
                thread::sleep(min_interval - elapsed);
            }
        }

        let called_at = Utc::now();
        last_request_started_at = Some(Instant::now());

        let response = client
            .get(STATISTICS_URL_TEMPLATE.replace("{slug}", &slug))
            .send()
            .with_context(|| format!("failed to request statistics for slug {slug}"))?
            .error_for_status()
            .with_context(|| format!("statistics endpoint returned error for slug {slug}"))?;

        let payload: StatisticsResponse = response
            .json()
            .with_context(|| format!("failed to parse statistics payload for slug {slug}"))?;

        let today = called_at.date_naive();
        let yesterday = today - chrono::Duration::days(1);

        let statistics_today =
            filter_by_effective_day(&payload.payload.statistics_closed.ninety_days, today);
        let statistics_yesterday =
            filter_by_effective_day(&payload.payload.statistics_closed.ninety_days, yesterday);
        let current_offers = filter_current_hour_sell(
            &payload.payload.statistics_live.forty_eight_hours,
            called_at,
        );
        let liquidity = sum_volumes(&statistics_today);

        items.push(TradeableItemSnapshot {
            slug,
            last_fetched_at: called_at.to_rfc3339_opts(SecondsFormat::Nanos, true),
            liquidity,
            statistics_yesterday,
            statistics_today,
            current_offers,
        });
    }

    let run_end = Utc::now();

    Ok(TradeableItemsRun {
        run_start: run_start.to_rfc3339_opts(SecondsFormat::Nanos, true),
        run_end: run_end.to_rfc3339_opts(SecondsFormat::Nanos, true),
        tradeable_items: items,
    })
}

fn read_dictionary(path: &str) -> Result<Dictionary> {
    let raw = fs::read_to_string(path)
        .with_context(|| format!("failed to read dictionary file at {path}"))?;

    let dictionary: Dictionary = serde_json::from_str(&raw)
        .with_context(|| format!("failed to parse dictionary file at {path}"))?;

    Ok(dictionary)
}

fn load_cached_snapshots(path: &str) -> Result<HashMap<String, TradeableItemSnapshot>> {
    let raw = match fs::read_to_string(path) {
        Ok(raw) => raw,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(HashMap::new()),
        Err(error) => {
            return Err(error)
                .with_context(|| format!("failed to read previous tradeable items run at {path}"));
        }
    };

    let run: TradeableItemsRun = match serde_json::from_str(&raw) {
        Ok(run) => run,
        Err(_) => return Ok(HashMap::new()),
    };

    Ok(run
        .tradeable_items
        .into_iter()
        .map(|item| (item.slug.clone(), item))
        .collect())
}

fn should_use_cached(item: &TradeableItemSnapshot, now: DateTime<Utc>) -> bool {
    let Some(last_fetched_at) = parse_datetime_utc(&item.last_fetched_at) else {
        return false;
    };

    let elapsed_hours = rounded_elapsed_hours(now, last_fetched_at);
    elapsed_hours < freshness_hours_for_liquidity(item.liquidity)
}

fn freshness_hours_for_liquidity(liquidity: u64) -> i64 {
    if liquidity <= 20 {
        24
    } else if liquidity <= 50 {
        6
    } else {
        1
    }
}

fn rounded_elapsed_hours(now: DateTime<Utc>, last_fetched_at: DateTime<Utc>) -> i64 {
    let elapsed_seconds = now
        .signed_duration_since(last_fetched_at)
        .num_seconds()
        .max(0) as f64;
    (elapsed_seconds / 3600.0).round() as i64
}

fn filter_by_effective_day(entries: &[Value], day: chrono::NaiveDate) -> Vec<Value> {
    entries
        .iter()
        .filter(|entry| {
            let day_matches = entry
                .get("datetime")
                .and_then(Value::as_str)
                .and_then(parse_datetime_utc)
                // Closed-day rows are timestamped at interval start, so shift +1 day.
                .map(|dt| (dt + chrono::Duration::days(1)).date_naive() == day)
                .unwrap_or(false);

            day_matches && keep_mod_rank_zero_or_absent(entry)
        })
        .map(sanitize_entry)
        .collect()
}

fn filter_current_hour_sell(entries: &[Value], now: DateTime<Utc>) -> Vec<Value> {
    entries
        .iter()
        .filter(|entry| {
            let is_sell = entry.get("order_type").and_then(Value::as_str) == Some("sell");
            let same_hour = entry
                .get("datetime")
                .and_then(Value::as_str)
                .and_then(parse_datetime_utc)
                // Live-hour rows are timestamped at interval start, so shift +1 hour.
                .map(|dt| {
                    (dt + chrono::Duration::hours(1)).timestamp() / 3600 == now.timestamp() / 3600
                })
                .unwrap_or(false);

            is_sell && same_hour && keep_mod_rank_zero_or_absent(entry)
        })
        .map(sanitize_entry)
        .collect()
}

fn keep_mod_rank_zero_or_absent(entry: &Value) -> bool {
    match entry.get("mod_rank") {
        None => true,
        Some(rank) => {
            rank.as_i64() == Some(0) || rank.as_u64() == Some(0) || rank.as_f64() == Some(0.0)
        }
    }
}

fn sanitize_entry(entry: &Value) -> Value {
    let mut cleaned = entry.clone();
    if let Value::Object(map) = &mut cleaned {
        map.remove("id");
        map.remove("datetime");
        map.remove("order_type");
    }

    cleaned
}

fn sum_volumes(entries: &[Value]) -> u64 {
    entries
        .iter()
        .map(|entry| match entry.get("volume") {
            Some(value) => value
                .as_u64()
                .or_else(|| value.as_i64().and_then(|v| (v >= 0).then_some(v as u64)))
                .unwrap_or(0),
            None => 0,
        })
        .sum()
}

fn parse_datetime_utc(value: &str) -> Option<DateTime<Utc>> {
    DateTime::parse_from_rfc3339(value)
        .ok()
        .map(|dt| dt.with_timezone(&Utc))
}
