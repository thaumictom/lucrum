//! Timestamped logging helpers.
//!
//! Emits structured JSON logs to stdout (info) and stderr (warnings/errors)
//! with run-scoped indexing and elapsed timing metadata.

use std::sync::OnceLock;
use std::sync::atomic::{AtomicU64, Ordering};

use chrono::{DateTime, Utc};
use serde_json::json;

struct RunState {
    run_id: String,
    run_start_ms: i64,
}

static RUN_STATE: OnceLock<RunState> = OnceLock::new();
static EVENT_INDEX: AtomicU64 = AtomicU64::new(1);

/// Initialize run-scoped logging metadata.
pub fn init_run_logging(run_start: DateTime<Utc>) -> String {
    let generated_run_id = format!(
        "{}-pid{}",
        run_start.format("%Y%m%dT%H%M%SZ"),
        std::process::id()
    );

    if RUN_STATE
        .set(RunState {
            run_id: generated_run_id.clone(),
            run_start_ms: run_start.timestamp_millis(),
        })
        .is_ok()
    {
        EVENT_INDEX.store(1, Ordering::Relaxed);
        generated_run_id
    } else {
        RUN_STATE
            .get()
            .map(|state| state.run_id.clone())
            .unwrap_or(generated_run_id)
    }
}

fn emit(level: &str, component: &str, message: &str) {
    let now = Utc::now();
    let event_index = EVENT_INDEX.fetch_add(1, Ordering::Relaxed);
    let (run_id, elapsed_ms) = if let Some(state) = RUN_STATE.get() {
        (
            state.run_id.as_str(),
            (now.timestamp_millis() - state.run_start_ms).max(0),
        )
    } else {
        ("uninitialized", 0)
    };

    let line = json!({
        "timestamp": now.to_rfc3339(),
        "level": level,
        "run_id": run_id,
        "event_index": event_index,
        "elapsed_ms": elapsed_ms,
        "pid": std::process::id(),
        "component": component,
        "message": message,
    });

    if matches!(level, "WARN" | "ERROR") {
        eprintln!("{line}");
    } else {
        println!("{line}");
    }
}

pub fn log_info(component: &str, message: &str) {
    emit("INFO", component, message);
}

pub fn log_warn(component: &str, message: &str) {
    emit("WARN", component, message);
}

pub fn log_error(component: &str, message: &str) {
    emit("ERROR", component, message);
}
