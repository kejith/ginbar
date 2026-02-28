//! Wrappers around ffmpeg and ffprobe that run external commands with
//! reduced scheduling priority (`nice -n 19`, `ionice -c3`) so the OS
//! scheduler always prefers the web-serving process.

use anyhow::{Context, Result};
use std::path::Path;
use std::process::Stdio;
use tokio::process::Command;
use tracing::debug;

/// Build an `exec::Command` prefixed with `nice -n 19 ionice -c3` so the
/// subprocess runs at the lowest CPU and I/O priority.
fn niced(program: &str, args: &[&str]) -> Command {
    let mut cmd = Command::new("nice");
    cmd.args(["-n", "19", "ionice", "-c3", program]);
    cmd.args(args);
    cmd.stdout(Stdio::piped());
    cmd.stderr(Stdio::piped());
    cmd
}

/// Probe the width and height of the first video stream via ffprobe.
/// Returns `(0, 0)` on failure (non-fatal).
pub async fn get_dimensions(path: &Path) -> (i32, i32) {
    let path_str = path.to_string_lossy();
    let output = niced(
        "ffprobe",
        &[
            "-v",
            "error",
            "-select_streams",
            "v:0",
            "-show_entries",
            "stream=width,height",
            "-of",
            "csv=p=0",
            &path_str,
        ],
    )
    .output()
    .await;

    match output {
        Ok(o) if o.status.success() => {
            let stdout = String::from_utf8_lossy(&o.stdout);
            let parts: Vec<&str> = stdout.trim().split(',').collect();
            if parts.len() >= 2 {
                let w = parts[0].parse().unwrap_or(0);
                let h = parts[1].parse().unwrap_or(0);
                (w, h)
            } else {
                (0, 0)
            }
        }
        _ => (0, 0),
    }
}

/// SVT-AV1 hard per-frame limits. Images exceeding either dimension fall
/// back to libaom-av1.
const SVT_MAX_WIDTH: i32 = 8192;
const SVT_MAX_HEIGHT: i32 = 8704;

/// Convert an image to AVIF using ffmpeg.
///
/// - `crf`: quality (0=lossless, 63=worst; 18≈visually lossless)
/// - `preset`: SVT-AV1 speed (0=slowest, 13=fastest)
/// - `max_width`: scale down to at most this width (0 = no scaling)
pub async fn convert_to_avif(
    input: &Path,
    output: &Path,
    crf: u32,
    preset: u32,
    max_width: u32,
) -> Result<()> {
    let (w, h) = get_dimensions(input).await;
    let use_svt = (w == 0 && h == 0) || (w <= SVT_MAX_WIDTH && h <= SVT_MAX_HEIGHT);

    // Build the video filter chain.
    let vf = if max_width > 0 && (w == 0 || w > max_width as i32) {
        format!(
            "scale='min({},iw)':-2,crop=trunc(iw/2)*2:trunc(ih/2)*2",
            max_width
        )
    } else {
        "crop=trunc(iw/2)*2:trunc(ih/2)*2".to_string()
    };

    let input_str = input.to_string_lossy();
    let output_str = output.to_string_lossy();
    let crf_str = crf.to_string();
    let preset_str = preset.to_string();

    let args: Vec<&str> = if use_svt {
        vec![
            "-y",
            "-i",
            &input_str,
            "-frames:v",
            "1",
            "-vf",
            &vf,
            "-c:v",
            "libsvtav1",
            "-crf",
            &crf_str,
            "-preset",
            &preset_str,
            "-g",
            "1",
            "-pix_fmt",
            "yuv420p",
            &output_str,
        ]
    } else {
        vec![
            "-y",
            "-i",
            &input_str,
            "-frames:v",
            "1",
            "-vf",
            &vf,
            "-c:v",
            "libaom-av1",
            "-crf",
            &crf_str,
            "-b:v",
            "0",
            "-cpu-used",
            "4",
            "-g",
            "1",
            "-pix_fmt",
            "yuv420p",
            &output_str,
        ]
    };

    let out = niced("ffmpeg", &args).output().await.context("ffmpeg avif")?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        anyhow::bail!("ffmpeg avif failed: {}", stderr);
    }

    debug!(input = %input.display(), output = %output.display(), "converted to avif");
    Ok(())
}

/// Normalize any image format to JPEG via ffmpeg so the Rust `image` crate can
/// decode it (avif, jxl, webp, gif, etc. → JPEG).
pub async fn normalize_to_jpeg(input: &Path, output: &Path) -> Result<()> {
    let input_str = input.to_string_lossy();
    let output_str = output.to_string_lossy();

    let args = [
        "-y",
        "-i",
        &input_str,
        "-frames:v",
        "1",
        "-q:v",
        "2",
        &output_str,
    ];

    let out = niced("ffmpeg", &args)
        .output()
        .await
        .context("ffmpeg normalize")?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        anyhow::bail!("ffmpeg normalize failed: {}", stderr);
    }

    Ok(())
}

/// Extract a single frame from a video at 1 second and save as JPEG.
pub async fn extract_video_frame(input: &Path, output: &Path) -> Result<()> {
    let input_str = input.to_string_lossy();
    let output_str = output.to_string_lossy();

    let args = [
        "-i",
        &input_str,
        "-ss",
        "00:00:01.000",
        "-vframes",
        "1",
        &output_str,
        "-hide_banner",
        "-loglevel",
        "panic",
    ];

    let out = niced("ffmpeg", &args)
        .output()
        .await
        .context("ffmpeg video frame")?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        anyhow::bail!("ffmpeg video frame failed: {}", stderr);
    }

    Ok(())
}
