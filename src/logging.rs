//! Timestamped logging helpers.
//!
//! Provides simple structured log output to stdout (info) and stderr
//! (warnings and errors) with RFC 3339 timestamps.

use chrono::Utc;

pub fn log_info(message: &str) {
    println!("[{}] INFO {message}", Utc::now().to_rfc3339());
}

pub fn log_warn(message: &str) {
    eprintln!("[{}] WARN {message}", Utc::now().to_rfc3339());
}

pub fn log_error(message: &str) {
    eprintln!("[{}] ERROR {message}", Utc::now().to_rfc3339());
}
