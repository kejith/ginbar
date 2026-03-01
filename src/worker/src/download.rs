//! File download helpers.
//!
//! Accepts a shared `reqwest::Client` (created once at startup for TLS/TCP
//! connection reuse) and streams the response body directly to disk rather
//! than buffering the entire body in memory first.

use anyhow::{Context, Result};
use futures_util::StreamExt;
use std::path::{Path, PathBuf};
use tokio::fs;
use tokio::io::AsyncWriteExt;
use tracing::debug;

const DOWNLOAD_TIMEOUT_SECS: u64 = 30;

/// Build a `reqwest::Client` suitable for file downloads.
/// Call once at startup and clone the handle throughout the program.
pub fn build_client() -> Result<reqwest::Client> {
    reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(DOWNLOAD_TIMEOUT_SECS))
        .build()
        .context("build reqwest client")
}

/// Download a file from `url` into `dir`, streaming the body directly to disk.
/// The filename is derived from the URL path.
pub async fn download_file(client: &reqwest::Client, url: &str, dir: &Path) -> Result<PathBuf> {
    let parsed: reqwest::Url = url.parse().context("invalid download URL")?;
    let basename = parsed
        .path_segments()
        .and_then(|s| s.last())
        .unwrap_or("download");
    let dst = dir.join(basename);

    let resp = client
        .get(url)
        .send()
        .await
        .context("download: GET failed")?;

    if !resp.status().is_success() {
        anyhow::bail!("download: unexpected status {} for {}", resp.status(), url);
    }

    stream_to_file(resp, &dst).await.context("download: stream to file")?;

    debug!(url, path = %dst.display(), "downloaded file");
    Ok(dst)
}

/// Download a file from pr0gramm CDN with the required headers, streaming to disk.
pub async fn download_pr0gramm_file(
    client: &reqwest::Client,
    url: &str,
    dir: &Path,
) -> Result<PathBuf> {
    let parsed: reqwest::Url = url.parse().context("invalid pr0gramm URL")?;
    let basename = parsed
        .path_segments()
        .and_then(|s| s.last())
        .unwrap_or("download");
    let dst = dir.join(basename);

    let resp = client
        .get(url)
        .header("User-Agent", "Mozilla/5.0 (compatible; Wallium/1.0)")
        .header("Referer", "https://pr0gramm.com/")
        .send()
        .await
        .context("pr0gramm download: GET failed")?;

    if !resp.status().is_success() {
        anyhow::bail!(
            "pr0gramm download: unexpected status {} for {}",
            resp.status(),
            url
        );
    }

    stream_to_file(resp, &dst).await.context("pr0gramm download: stream to file")?;

    debug!(url, path = %dst.display(), "downloaded pr0gramm file");
    Ok(dst)
}

/// Stream a response body directly to `dst`, writing in chunks.
async fn stream_to_file(resp: reqwest::Response, dst: &Path) -> Result<()> {
    let mut f = fs::File::create(dst).await.context("create file")?;
    let mut stream = resp.bytes_stream();

    while let Some(chunk) = stream.next().await {
        let chunk = chunk.context("read response chunk")?;
        f.write_all(&chunk).await.context("write chunk")?;
    }

    f.flush().await.context("flush file")?;
    Ok(())
}

// ── Unit tests ────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    // ── build_client ──────────────────────────────────────────────────────────

    #[test]
    fn test_build_client_succeeds() {
        // TLS backend initialisation and socket config must not fail.
        let result = build_client();
        assert!(result.is_ok(), "build_client must succeed: {:?}", result.err());
    }

    #[test]
    fn test_build_client_returns_usable_handle() {
        // Each call produces an independent, usable client handle.
        let a = build_client().expect("first client");
        let b = build_client().expect("second client");
        // Both built successfully — drop them to avoid any Tokio runtime warnings.
        drop(a);
        drop(b);
    }

    #[test]
    fn test_download_timeout_constant_is_positive() {
        // Sanity-check: the configured timeout must be > 0.
        assert!(
            DOWNLOAD_TIMEOUT_SECS > 0,
            "download timeout must be positive"
        );
    }
}
