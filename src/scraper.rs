//! HTTP client for fetching and transforming per-item market statistics
//! from the Warframe Market API.

use std::thread;
use std::time::{Duration as StdDuration, Instant};

use anyhow::{Context, Result, anyhow, bail};
use chrono::{DateTime, Duration, Utc};
use reqwest::blocking::Client;
use serde::Deserialize;
use serde_json::Value;

use crate::logging::{log_info, log_warn};
use crate::models::ItemStatistics;

const MAX_ATTEMPTS: usize = 3;
const RETRY_DELAY: StdDuration = StdDuration::from_secs(60);
const DEFAULT_REQUEST_TIMEOUT: StdDuration = StdDuration::from_secs(30);
const STATISTICS_URL_TEMPLATE: &str = "https://api.warframe.market/v1/items/{slug}/statistics";

// ── API response types (private to this module) ───────────────────────────

#[derive(Debug, Deserialize)]
struct StatisticsResponse {
    payload: StatisticsPayload,
}

#[derive(Debug, Deserialize)]
struct StatisticsPayload {
    statistics_closed: ClosedStatistics,
    statistics_live: LiveStatistics,
}

#[derive(Debug, Deserialize)]
struct ClosedStatistics {
    #[serde(rename = "90days")]
    ninety_days: Vec<Value>,
}

#[derive(Debug, Deserialize)]
struct LiveStatistics {
    #[serde(rename = "90days")]
    ninety_days: Vec<Value>,
}

// ── Scraper ───────────────────────────────────────────────────────────────

/// Rate-limited HTTP client that fetches per-item statistics from Warframe Market.
pub struct StatisticsScraper {
    client: Client,
    min_request_interval: StdDuration,
    last_request_started_at: Option<Instant>,
}

impl StatisticsScraper {
    pub fn new(requests_per_second: f64) -> Result<Self> {
        if !(requests_per_second.is_finite() && requests_per_second > 0.0) {
            bail!("requests per second must be a positive number");
        }

        Ok(Self {
            client: Client::builder()
                .connect_timeout(DEFAULT_REQUEST_TIMEOUT)
                .timeout(DEFAULT_REQUEST_TIMEOUT)
                .build()
                .context("failed to build HTTP client")?,
            min_request_interval: StdDuration::from_secs_f64(1.0 / requests_per_second),
            last_request_started_at: None,
        })
    }

    /// Fetch statistics for `slug`, retrying up to `MAX_ATTEMPTS` times on failure.
    pub fn fetch_with_retry(&mut self, slug: &str, name: &str) -> Result<ItemStatistics> {
        for attempt in 1..=MAX_ATTEMPTS {
            self.wait_for_rate_limit();
            log_info(
                "scraper",
                &format!("Fetching statistics for {slug} (attempt {attempt}/{MAX_ATTEMPTS})."),
            );

            match self.fetch_once(slug, name) {
                Ok(stats) => {
                    log_info(
                        "scraper",
                        &format!("Fetch succeeded for {slug} on attempt {attempt}/{MAX_ATTEMPTS}."),
                    );
                    return Ok(stats);
                }
                Err(error) if attempt < MAX_ATTEMPTS => {
                    log_warn(
                        "scraper",
                        &format!(
                            "Attempt {attempt}/{MAX_ATTEMPTS} failed for {slug}: {error}. \
                         Retrying in {} minutes.",
                            RETRY_DELAY.as_secs() / 60
                        ),
                    );
                    thread::sleep(RETRY_DELAY);
                }
                Err(error) => return Err(error),
            }
        }

        unreachable!("retry loop always returns or errors")
    }

    /// Sleep if needed to stay below the configured request rate.
    fn wait_for_rate_limit(&mut self) {
        if let Some(previous) = self.last_request_started_at {
            let elapsed = previous.elapsed();
            if elapsed < self.min_request_interval {
                thread::sleep(self.min_request_interval - elapsed);
            }
        }
        self.last_request_started_at = Some(Instant::now());
    }

    /// Single (non-retried) statistics fetch for one item slug.
    fn fetch_once(&self, slug: &str, name: &str) -> Result<ItemStatistics> {
        let fetched_at = Utc::now();
        let url = STATISTICS_URL_TEMPLATE.replace("{slug}", slug);
        let response = self
            .client
            .get(&url)
            .send()
            .map_err(|e| anyhow!("request failed for {slug}: {e}"))?;

        let status = response.status();
        if !status.is_success() {
            let reason = response
                .text()
                .ok()
                .map(|b| b.trim().to_owned())
                .filter(|b| !b.is_empty())
                .unwrap_or_else(|| {
                    status
                        .canonical_reason()
                        .unwrap_or("unknown status")
                        .to_owned()
                });
            bail!("HTTP {} for {}: {}", status.as_u16(), slug, reason);
        }

        let body = response
            .json::<StatisticsResponse>()
            .with_context(|| format!("failed to deserialize statistics payload for {slug}"))?;

        build_item_statistics(slug, name, fetched_at, body)
    }
}

// ── Statistics transformation ─────────────────────────────────────────────

/// Build an `ItemStatistics` from the raw API response, extracting yesterday's
/// and today's closed statistics together with the latest live sell offer.
fn build_item_statistics(
    slug: &str,
    name: &str,
    fetched_at: DateTime<Utc>,
    response: StatisticsResponse,
) -> Result<ItemStatistics> {
    // Keep only base-rank (mod_rank 0 or absent) stats for both closed and live.
    let base_rank = |s: &Value| matches!(s.get("mod_rank").and_then(Value::as_u64), None | Some(0));

    let filtered_history: Vec<_> = response
        .payload
        .statistics_closed
        .ninety_days
        .into_iter()
        .filter(|s| base_rank(s))
        .collect();

    let filtered_live: Vec<_> = response
        .payload
        .statistics_live
        .ninety_days
        .into_iter()
        .filter(|s| base_rank(s))
        .collect();

    let today = fetched_at.date_naive() - Duration::days(1);
    let yesterday = fetched_at.date_naive() - Duration::days(2);
    let empty = || Value::Object(Default::default());

    // order_type is always "sell" here (used only to select the entry), so
    // drop it from the stored value to avoid redundant data.
    let current_offers = find_stat_for_date(&filtered_live, today, Some("sell"))
        .map(|mut s| {
            if let Value::Object(ref mut map) = s {
                map.remove("order_type");
            }
            s
        })
        .unwrap_or_else(empty);
    let statistics_today = find_stat_for_date(&filtered_history, today, None).unwrap_or_else(empty);
    let statistics_yesterday =
        find_stat_for_date(&filtered_history, yesterday, None).unwrap_or_else(empty);

    let liquidity = statistics_today
        .get("volume")
        .and_then(Value::as_u64)
        .unwrap_or(0);

    Ok(ItemStatistics {
        item: slug.to_owned(),
        name: name.to_owned(),
        last_fetched_at: fetched_at,
        liquidity,
        statistics_yesterday,
        statistics_today,
        current_offers,
    })
}

/// Find the first entry in `history` whose datetime matches `target_date`,
/// optionally filtering by `order_type`.
fn find_stat_for_date(
    history: &[Value],
    target_date: chrono::NaiveDate,
    order_type: Option<&str>,
) -> Option<Value> {
    history.iter().find_map(|stat| {
        let date = stat
            .get("datetime")
            .and_then(Value::as_str)
            .and_then(|dt| DateTime::parse_from_rfc3339(dt).ok())
            .map(|dt| dt.date_naive());
        let type_ok = order_type.is_none_or(|expected| {
            stat.get("order_type").and_then(Value::as_str) == Some(expected)
        });

        (date == Some(target_date) && type_ok).then(|| stat.clone())
    })
}

/// Strip `datetime` and `id` fields that add noise to the persisted JSON.
pub fn sanitize_item_statistics(item: &mut ItemStatistics) {
    strip_noise_fields(&mut item.statistics_yesterday);
    strip_noise_fields(&mut item.statistics_today);
    strip_noise_fields(&mut item.current_offers);
}

/// Recursively remove `datetime` and `id` keys from a JSON value.
fn strip_noise_fields(value: &mut Value) {
    match value {
        Value::Object(map) => {
            map.remove("datetime");
            map.remove("id");
            for v in map.values_mut() {
                strip_noise_fields(v);
            }
        }
        Value::Array(items) => items.iter_mut().for_each(strip_noise_fields),
        _ => {}
    }
}

// ── Tests ─────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn fixed_fetch_time() -> DateTime<Utc> {
        DateTime::parse_from_rfc3339("2026-03-09T12:00:00Z")
            .expect("fixed test datetime should parse")
            .with_timezone(&Utc)
    }

    #[test]
    fn builds_item_statistics_from_last_two_closed_days() {
        let response: StatisticsResponse =
            serde_json::from_str(include_str!("../sample.json")).expect("sample.json should parse");

        let result = build_item_statistics(
            "secura_dual_cestra",
            "Secura Dual Cestra",
            fixed_fetch_time(),
            response,
        )
        .expect("statistics should build from sample payload");

        assert_eq!(result.item, "secura_dual_cestra");
        assert_eq!(result.name, "Secura Dual Cestra");
        assert_eq!(result.liquidity, 67);
        assert_eq!(result.statistics_today["volume"], 67);
        assert_eq!(result.current_offers, json!({}));
    }

    #[test]
    fn leaves_missing_days_empty_instead_of_failing() {
        let response: StatisticsResponse = serde_json::from_value(json!({
            "payload": {
                "statistics_closed": {
                    "90days": [
                        {
                            "datetime": "2026-03-07T00:00:00.000+00:00",
                            "volume": 8
                        }
                    ]
                },
                "statistics_live": {
                    "90days": []
                }
            }
        }))
        .expect("test payload should parse");

        let result = build_item_statistics(
            "orokin_derelict_plaza_scene",
            "Orokin Derelict Plaza Scene",
            fixed_fetch_time(),
            response,
        )
        .expect("missing days should not fail");

        assert_eq!(result.liquidity, 0);
        assert_eq!(result.statistics_yesterday["volume"], 8);
        assert_eq!(result.statistics_today, json!({}));
        assert_eq!(result.current_offers, json!({}));
    }

    #[test]
    fn keeps_only_today_live_sell_offer() {
        let response: StatisticsResponse = serde_json::from_value(json!({
            "payload": {
                "statistics_closed": {
                    "90days": []
                },
                "statistics_live": {
                    "90days": [
                        {
                            "datetime": "2026-03-06T00:00:00.000+00:00",
                            "order_type": "sell",
                            "platinum": 12,
                            "user": { "ingame_name": "seller_one" }
                        },
                        {
                            "datetime": "2026-03-08T00:00:00.000+00:00",
                            "order_type": "buy",
                            "platinum": 8,
                            "user": { "ingame_name": "buyer_one" }
                        },
                        {
                            "datetime": "2026-03-08T00:00:00.000+00:00",
                            "order_type": "sell",
                            "platinum": 14,
                            "user": { "ingame_name": "seller_two" }
                        }
                    ]
                }
            }
        }))
        .expect("test payload should parse");

        let result = build_item_statistics(
            "secura_dual_cestra",
            "Secura Dual Cestra",
            fixed_fetch_time(),
            response,
        )
        .expect("today's live sell offer should be collected");

        // order_type is dropped because it is always "sell" and adds no information.
        assert_eq!(result.current_offers.get("order_type"), None);
        assert_eq!(result.current_offers["platinum"], 14);
    }

    #[test]
    fn strips_datetime_and_id_before_persisting() {
        let mut item = ItemStatistics {
            item: "secura_dual_cestra".to_owned(),
            name: "Secura Dual Cestra".to_owned(),
            last_fetched_at: fixed_fetch_time(),
            liquidity: 21,
            statistics_yesterday: json!({
                "datetime": "2026-03-07T00:00:00.000+00:00",
                "id": "abc",
                "volume": 27,
                "nested": { "id": "nested-id", "keep": true }
            }),
            statistics_today: json!({
                "datetime": "2026-03-08T00:00:00.000+00:00",
                "id": "def",
                "volume": 21
            }),
            current_offers: json!({
                "datetime": "2026-03-08T00:00:00.000+00:00",
                "id": "ghi",
                "order_type": "sell"
            }),
        };

        sanitize_item_statistics(&mut item);

        assert_eq!(item.statistics_yesterday.get("datetime"), None);
        assert_eq!(item.statistics_yesterday.get("id"), None);
        assert_eq!(item.statistics_yesterday["nested"].get("id"), None);
        assert_eq!(item.statistics_today.get("datetime"), None);
        assert_eq!(item.statistics_today.get("id"), None);
        assert_eq!(item.current_offers.get("datetime"), None);
        assert_eq!(item.current_offers.get("id"), None);
        assert_eq!(item.statistics_today["volume"], 21);
    }
}
