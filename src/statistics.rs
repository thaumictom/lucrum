use std::collections::HashMap;
use std::env;
use std::fs;
use std::path::Path;
use std::thread;
use std::time::{Duration as StdDuration, Instant};

use anyhow::{Context, Result, anyhow, bail};
use chrono::{DateTime, Duration, Utc};
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use serde_json::Value;

const DEFAULT_REQUESTS_PER_SECOND: f64 = 2.0;
const MAX_ATTEMPTS: usize = 3;
const RETRY_DELAY: StdDuration = StdDuration::from_secs(60);
const PROGRESS_LOG_INTERVAL: usize = 100;
const DEFAULT_REQUEST_TIMEOUT: StdDuration = StdDuration::from_secs(30);
const STATISTICS_URL_TEMPLATE: &str = "https://api.warframe.market/v1/items/{slug}/statistics";

#[derive(Debug, Serialize, Deserialize)]
pub struct StatisticsRun {
    pub run_start: DateTime<Utc>,
    pub run_end: DateTime<Utc>,
    pub last_error: String,
    pub item_statistics: Vec<ItemStatistics>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ItemStatistics {
    pub item: String,
    pub last_fetched_at: DateTime<Utc>,
    pub liquidity: u64,
    pub statistics_yesterday: Value,
    pub statistics_today: Value,
    #[serde(default)]
    pub current_offers: Value,
}

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

struct StatisticsScraper {
    client: Client,
    min_request_interval: StdDuration,
    last_request_started_at: Option<Instant>,
}

impl StatisticsScraper {
    fn new(requests_per_second: f64) -> Result<Self> {
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

    // Keep the scraper below the configured request rate.
    fn wait_for_rate_limit(&mut self) {
        if let Some(previous_request) = self.last_request_started_at {
            let elapsed = previous_request.elapsed();
            if elapsed < self.min_request_interval {
                thread::sleep(self.min_request_interval - elapsed);
            }
        }

        self.last_request_started_at = Some(Instant::now());
    }

    fn fetch_item_statistics_with_retry(&mut self, slug: &str) -> Result<ItemStatistics> {
        for attempt in 1..=MAX_ATTEMPTS {
            self.wait_for_rate_limit();
            log_info(&format!(
                "Fetching statistics for {slug} (attempt {attempt}/{MAX_ATTEMPTS})."
            ));

            match self.fetch_item_statistics_once(slug) {
                Ok(statistics) => return Ok(statistics),
                Err(error) if attempt < MAX_ATTEMPTS => {
                    log_warn(&format!(
                        "Attempt {attempt}/{MAX_ATTEMPTS} failed for {slug}: {}. Retrying in {} minutes.",
                        format_error(&error),
                        RETRY_DELAY.as_secs() / 60
                    ));
                    thread::sleep(RETRY_DELAY);
                }
                Err(error) => return Err(error),
            }
        }

        unreachable!("retry loop always returns or errors")
    }

    fn fetch_item_statistics_once(&self, slug: &str) -> Result<ItemStatistics> {
        let fetched_at = Utc::now();
        let url = STATISTICS_URL_TEMPLATE.replace("{slug}", slug);
        let response = self
            .client
            .get(&url)
            .send()
            .map_err(|error| anyhow!("request failed for {slug}: {error}"))?;

        let status = response.status();
        if !status.is_success() {
            let reason = response
                .text()
                .ok()
                .map(|body| body.trim().to_owned())
                .filter(|body| !body.is_empty())
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

        build_item_statistics(slug, fetched_at, body)
    }
}

// Scrape every item slug and write the aggregated market statistics JSON to disk.
pub fn scrape_and_write_statistics(
    slugs: &[String],
    output_path: &Path,
    run_start: DateTime<Utc>,
) -> Result<usize> {
    let requests_per_second = configured_requests_per_second()?;
    let mut scraper = StatisticsScraper::new(requests_per_second)?;
    let cached_statistics_by_slug = load_cached_statistics_by_slug(output_path)?;
    let mut scraped_count = 0usize;
    let mut skipped_count = 0usize;

    initialize_statistics_run(output_path, run_start)?;

    log_info(&format!(
        "Starting market statistics scrape for {} items at {:.2} requests/second.",
        slugs.len(),
        requests_per_second
    ));

    for (index, slug) in slugs.iter().enumerate() {
        if index % PROGRESS_LOG_INTERVAL == 0 || index + 1 == slugs.len() {
            log_info(&format!(
                "Scraping item {}/{}: {}",
                index + 1,
                slugs.len(),
                slug
            ));
        }

        if let Some(cached_statistics) = cached_statistics_by_slug.get(slug)
            && should_skip_fetch(cached_statistics, run_start)
        {
            log_info(&format!(
                "Skipping {slug}; cache is still fresh for liquidity {}.",
                cached_statistics.liquidity
            ));
            persist_item_statistics(output_path, run_start, cached_statistics.clone())?;
            skipped_count += 1;
            continue;
        }

        match scraper.fetch_item_statistics_with_retry(slug) {
            Ok(statistics) => {
                persist_item_statistics(output_path, run_start, statistics)?;
                scraped_count += 1;
            }
            Err(error) => {
                let last_error = format!("{}: {}", slug, format_error(&error));
                if let Err(write_error) =
                    update_statistics_error(output_path, run_start, &last_error)
                {
                    log_error(&format!(
                        "Failed to write partial statistics output after error: {}",
                        format_error(&write_error)
                    ));
                }

                panic!("Shutting down after {MAX_ATTEMPTS} failed attempts for {last_error}");
            }
        }
    }

    finalize_statistics_run(output_path, run_start)?;
    log_info(&format!(
        "Finished market statistics scrape for {} items and reused {} cached items.",
        scraped_count, skipped_count
    ));

    Ok(scraped_count)
}

fn build_item_statistics(
    slug: &str,
    fetched_at: DateTime<Utc>,
    response: StatisticsResponse,
) -> Result<ItemStatistics> {
    let filtered_history = response
        .payload
        .statistics_closed
        .ninety_days
        .into_iter()
        .filter(|stat| matches!(stat.get("mod_rank").and_then(Value::as_u64), None | Some(0)))
        .collect::<Vec<_>>();

    let statistics_today_date = fetched_at.date_naive() - Duration::days(1);
    let statistics_yesterday_date = fetched_at.date_naive() - Duration::days(2);
    let current_offers = find_stat_for_date(
        &response.payload.statistics_live.ninety_days,
        statistics_today_date,
        Some("sell"),
    )
    .unwrap_or_else(|| Value::Object(Default::default()));

    let statistics_today = find_stat_for_date(&filtered_history, statistics_today_date, None)
        .unwrap_or_else(|| Value::Object(Default::default()));
    let statistics_yesterday =
        find_stat_for_date(&filtered_history, statistics_yesterday_date, None)
            .unwrap_or_else(|| Value::Object(Default::default()));

    let liquidity = statistics_today
        .get("volume")
        .and_then(Value::as_u64)
        .unwrap_or(0);

    Ok(ItemStatistics {
        item: slug.to_owned(),
        last_fetched_at: fetched_at,
        liquidity,
        statistics_yesterday,
        statistics_today,
        current_offers,
    })
}

fn find_stat_for_date(
    history: &[Value],
    target_date: chrono::NaiveDate,
    order_type: Option<&str>,
) -> Option<Value> {
    history.iter().find_map(|stat| {
        let stat_date = stat
            .get("datetime")
            .and_then(Value::as_str)
            .and_then(|datetime| DateTime::parse_from_rfc3339(datetime).ok())
            .map(|datetime| datetime.date_naive());
        let order_type_matches = order_type.is_none_or(|expected| {
            stat.get("order_type").and_then(Value::as_str) == Some(expected)
        });

        (stat_date == Some(target_date) && order_type_matches).then(|| stat.clone())
    })
}

fn configured_requests_per_second() -> Result<f64> {
    match env::var("LUCRUM_REQUESTS_PER_SECOND") {
        Ok(value) => {
            let parsed = value
                .parse::<f64>()
                .context("LUCRUM_REQUESTS_PER_SECOND must be a valid number")?;

            if !(parsed.is_finite() && parsed > 0.0) {
                bail!("LUCRUM_REQUESTS_PER_SECOND must be greater than zero");
            }

            Ok(parsed)
        }
        Err(env::VarError::NotPresent) => Ok(DEFAULT_REQUESTS_PER_SECOND),
        Err(error) => Err(anyhow!(
            "failed to read LUCRUM_REQUESTS_PER_SECOND: {error}"
        )),
    }
}

fn should_skip_fetch(item_statistics: &ItemStatistics, now: DateTime<Utc>) -> bool {
    let max_cache_age = if item_statistics.liquidity > 200 {
        Duration::hours(1)
    } else if item_statistics.liquidity > 100 {
        Duration::hours(2)
    } else if item_statistics.liquidity > 20 {
        Duration::hours(6)
    } else {
        Duration::hours(18)
    };

    now.signed_duration_since(item_statistics.last_fetched_at) < max_cache_age
}

fn write_statistics_run(path: &Path, run: &StatisticsRun) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).with_context(|| {
            format!("failed to create statistics directory {}", parent.display())
        })?;
    }

    let json = serde_json::to_string_pretty(run)
        .context("failed to serialize market statistics output")?;
    fs::write(path, format!("{json}\n"))
        .with_context(|| format!("failed to write statistics file at {}", path.display()))?;

    Ok(())
}

fn initialize_statistics_run(path: &Path, run_start: DateTime<Utc>) -> Result<()> {
    write_statistics_run(
        path,
        &StatisticsRun {
            run_start,
            run_end: run_start,
            last_error: String::new(),
            item_statistics: Vec::new(),
        },
    )
}

fn load_cached_statistics_by_slug(path: &Path) -> Result<HashMap<String, ItemStatistics>> {
    let Some(existing_run) = load_existing_statistics_run(path)? else {
        return Ok(HashMap::new());
    };

    Ok(existing_run
        .item_statistics
        .into_iter()
        .map(|item_statistics| (item_statistics.item.clone(), item_statistics))
        .collect())
}

fn persist_item_statistics(
    path: &Path,
    run_start: DateTime<Utc>,
    mut item_statistics: ItemStatistics,
) -> Result<()> {
    sanitize_item_statistics(&mut item_statistics);

    let mut run = load_statistics_run(path, run_start)?;
    run.run_end = Utc::now();
    run.last_error.clear();
    run.item_statistics.push(item_statistics);
    write_statistics_run(path, &run)
}

fn sanitize_item_statistics(item_statistics: &mut ItemStatistics) {
    remove_useless_fields(&mut item_statistics.statistics_yesterday);
    remove_useless_fields(&mut item_statistics.statistics_today);
    remove_useless_fields(&mut item_statistics.current_offers);
}

fn remove_useless_fields(value: &mut Value) {
    match value {
        Value::Object(map) => {
            map.remove("datetime");
            map.remove("id");

            for nested_value in map.values_mut() {
                remove_useless_fields(nested_value);
            }
        }
        Value::Array(items) => {
            for item in items {
                remove_useless_fields(item);
            }
        }
        _ => {}
    }
}

fn load_existing_statistics_run(path: &Path) -> Result<Option<StatisticsRun>> {
    if !path.exists() {
        return Ok(None);
    }

    let raw = fs::read_to_string(path)
        .with_context(|| format!("failed to read statistics file at {}", path.display()))?;
    let run = serde_json::from_str(&raw)
        .with_context(|| format!("failed to parse statistics file at {}", path.display()))?;

    Ok(Some(run))
}

fn update_statistics_error(path: &Path, run_start: DateTime<Utc>, last_error: &str) -> Result<()> {
    let mut run = load_statistics_run(path, run_start)?;
    run.run_end = Utc::now();
    run.last_error = last_error.to_owned();
    write_statistics_run(path, &run)
}

fn finalize_statistics_run(path: &Path, run_start: DateTime<Utc>) -> Result<()> {
    let mut run = load_statistics_run(path, run_start)?;
    run.run_end = Utc::now();
    run.last_error.clear();
    write_statistics_run(path, &run)
}

fn load_statistics_run(path: &Path, run_start: DateTime<Utc>) -> Result<StatisticsRun> {
    Ok(
        load_existing_statistics_run(path)?.unwrap_or(StatisticsRun {
            run_start,
            run_end: run_start,
            last_error: String::new(),
            item_statistics: Vec::new(),
        }),
    )
}

fn format_error(error: &anyhow::Error) -> String {
    error.to_string()
}

fn log_info(message: &str) {
    println!("[{}] INFO {message}", Utc::now().to_rfc3339());
}

fn log_warn(message: &str) {
    eprintln!("[{}] WARN {message}", Utc::now().to_rfc3339());
}

fn log_error(message: &str) {
    eprintln!("[{}] ERROR {message}", Utc::now().to_rfc3339());
}

#[cfg(test)]
mod tests {
    use super::{
        ItemStatistics, StatisticsResponse, build_item_statistics, sanitize_item_statistics,
        should_skip_fetch,
    };
    use chrono::{DateTime, Duration, Utc};
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

        let result = build_item_statistics("secura_dual_cestra", fixed_fetch_time(), response)
            .expect("statistics should build from sample payload");

        assert_eq!(result.item, "secura_dual_cestra");
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

        let result =
            build_item_statistics("orokin_derelict_plaza_scene", fixed_fetch_time(), response)
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

        let result = build_item_statistics("secura_dual_cestra", fixed_fetch_time(), response)
            .expect("today's live sell offer should be collected");

        assert_eq!(result.current_offers["order_type"], "sell");
        assert_eq!(result.current_offers["platinum"], 14);
    }

    #[test]
    fn strips_datetime_and_id_before_persisting() {
        let mut item_statistics = ItemStatistics {
            item: "secura_dual_cestra".to_owned(),
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

        sanitize_item_statistics(&mut item_statistics);

        assert_eq!(item_statistics.statistics_yesterday.get("datetime"), None);
        assert_eq!(item_statistics.statistics_yesterday.get("id"), None);
        assert_eq!(
            item_statistics.statistics_yesterday["nested"].get("id"),
            None
        );
        assert_eq!(item_statistics.statistics_today.get("datetime"), None);
        assert_eq!(item_statistics.statistics_today.get("id"), None);
        assert_eq!(item_statistics.current_offers.get("datetime"), None);
        assert_eq!(item_statistics.current_offers.get("id"), None);
        assert_eq!(item_statistics.statistics_today["volume"], 21);
    }

    #[test]
    fn skips_high_liquidity_items_only_for_one_hour() {
        let now = Utc::now();
        let cached = ItemStatistics {
            item: "secura_dual_cestra".to_owned(),
            last_fetched_at: now - Duration::minutes(59),
            liquidity: 201,
            statistics_yesterday: json!({}),
            statistics_today: json!({}),
            current_offers: json!({}),
        };

        assert!(should_skip_fetch(&cached, now));
        assert!(!should_skip_fetch(
            &ItemStatistics {
                last_fetched_at: now - Duration::minutes(61),
                ..cached
            },
            now
        ));
    }

    #[test]
    fn skips_low_liquidity_items_for_eighteen_hours() {
        let now = Utc::now();
        let cached = ItemStatistics {
            item: "irradiating_disarm".to_owned(),
            last_fetched_at: now - Duration::hours(17),
            liquidity: 5,
            statistics_yesterday: json!({}),
            statistics_today: json!({}),
            current_offers: json!({}),
        };

        assert!(should_skip_fetch(&cached, now));
        assert!(!should_skip_fetch(
            &ItemStatistics {
                last_fetched_at: now - Duration::hours(19),
                ..cached
            },
            now
        ));
    }
}
