use std::fs;
use std::path::Path;

use anyhow::{Context, Result};

use crate::types::Dictionary;

pub fn write_dictionary(path: &str, dictionary: &Dictionary) -> Result<()> {
    let output_path = Path::new(path);

    if let Some(parent) = output_path.parent() {
        // Create parent directory so first boot works in a clean repo.
        fs::create_dir_all(parent)
            .with_context(|| format!("failed to create output directory {}", parent.display()))?;
    }

    let json =
        serde_json::to_string_pretty(dictionary).context("failed to serialize dictionary")?;

    fs::write(output_path, json)
        .with_context(|| format!("failed to write {}", output_path.display()))?;

    Ok(())
}
