use anyhow::{Context, Result};
use std::path::{Path, PathBuf};
use tokio::fs;
use tokio::io::AsyncWriteExt;
use tracing::debug;

const DOWNLOAD_TIMEOUT_SECS: u64 = 30;

/// Download a file from `url` into `dir`, returning the local path.
/// The filename is derived from the URL path.
pub async fn download_file(url: &str, dir: &Path) -> Result<PathBuf> {
    let parsed: reqwest::Url = url.parse().context("invalid download URL")?;
    let basename = parsed
        .path_segments()
        .and_then(|s| s.last())
        .unwrap_or("download");
    let dst = dir.join(basename);

    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(DOWNLOAD_TIMEOUT_SECS))
        .build()?;

    let resp = client
        .get(url)
        .send()
        .await
        .context("download: GET failed")?;

    if !resp.status().is_success() {
        anyhow::bail!("download: unexpected status {} for {}", resp.status(), url);
    }

    let bytes = resp.bytes().await.context("download: read body")?;
    let mut f = fs::File::create(&dst).await.context("download: create file")?;
    f.write_all(&bytes).await.context("download: write file")?;
    f.flush().await?;

    debug!(url, path = %dst.display(), "downloaded file");
    Ok(dst)
}

/// Download a file from pr0gramm CDN with the required headers.
pub async fn download_pr0gramm_file(url: &str, dir: &Path) -> Result<PathBuf> {
    let parsed: reqwest::Url = url.parse().context("invalid pr0gramm URL")?;
    let basename = parsed
        .path_segments()
        .and_then(|s| s.last())
        .unwrap_or("download");
    let dst = dir.join(basename);

    let client = reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(DOWNLOAD_TIMEOUT_SECS))
        .build()?;

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

    let bytes = resp.bytes().await.context("pr0gramm download: read body")?;
    let mut f = fs::File::create(&dst).await.context("pr0gramm download: create file")?;
    f.write_all(&bytes)
        .await
        .context("pr0gramm download: write file")?;
    f.flush().await?;

    debug!(url, path = %dst.display(), "downloaded pr0gramm file");
    Ok(dst)
}
