//! Wrappers around ffmpeg and ffprobe.
//!
//! Priority strategy: instead of wrapping every invocation with `nice -n 19
//! ionice -c3` (which creates 3 process forks per call: nice→ionice→program),
//! we set CPU and I/O priority directly inside each child process via a
//! `pre_exec` hook.  This reduces per-call overhead from 3 forks → 1 fork.

use anyhow::{Context, Result};
use std::path::Path;
use std::process::Stdio;
use tokio::process::Command;
use tracing::debug;

// ── Priority helpers ──────────────────────────────────────────────────────────

/// Return the configured ffmpeg thread count from `FFMPEG_THREADS` env var.
/// Defaults to `max(1, total_cpus / 4)` — reasonable for a concurrency-4 worker.
fn ffmpeg_thread_count() -> u32 {
    if let Ok(v) = std::env::var("FFMPEG_THREADS") {
        if let Ok(n) = v.parse::<u32>() {
            if n > 0 {
                return n;
            }
        }
    }
    (num_cpus::get() as u32 / 4).max(1)
}

/// Build a `Command` that runs `program` at the lowest CPU and I/O priority.
/// Priority is set inside the child process via `pre_exec` (one fork, not three).
fn priority_cmd(program: &str, args: &[&str]) -> Command {
    let mut cmd = Command::new(program);
    cmd.args(args);
    cmd.stdout(Stdio::piped());
    cmd.stderr(Stdio::piped());
    // SAFETY: called between fork and exec; only async-signal-safe syscalls used.
    unsafe {
        cmd.pre_exec(|| {
            // nice 19 — lowest CPU priority
            libc::setpriority(libc::PRIO_PROCESS, 0, 19);
            // ionice -c3 (idle I/O class) via ioprio_set syscall
            // IOPRIO_PRIO_VALUE(IOPRIO_CLASS_IDLE=3, 0) = 3 << 13
            libc::syscall(libc::SYS_ioprio_set, 1i64, 0i64, (3i64 << 13) | 0i64);
            Ok(())
        });
    }
    cmd
}

// ── SVT-AV1 dimension limits ──────────────────────────────────────────────────

const SVT_MAX_WIDTH: i32 = 8192;
const SVT_MAX_HEIGHT: i32 = 8704;

// ── Public API ────────────────────────────────────────────────────────────────

/// Probe the width and height of the first video stream via ffprobe.
/// Returns `(0, 0)` on failure (non-fatal — caller handles gracefully).
pub async fn get_dimensions(path: &Path) -> (i32, i32) {
    let path_str = path.to_string_lossy();
    let output = priority_cmd(
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
        Ok(o) if o.status.success() => parse_dimensions(&o.stdout),
        _ => (0, 0),
    }
}

/// Convert an image to AVIF using ffmpeg / SVT-AV1 (falls back to libaom).
///
/// Returns `(out_width, out_height)` — computed from the probed source
/// dimensions so the caller avoids a redundant `ffprobe` on the output.
///
/// - `crf`: quality (0 = lossless, 63 = worst; 18 ≈ visually lossless)
/// - `preset`: SVT-AV1 speed (0 = slowest, 13 = fastest)
/// - `max_width`: scale down so width ≤ this value (0 = no scaling)
pub async fn convert_to_avif(
    input: &Path,
    output: &Path,
    crf: u32,
    preset: u32,
    max_width: u32,
) -> Result<(i32, i32)> {
    let (w, h) = get_dimensions(input).await;
    let use_svt = (w == 0 && h == 0) || (w <= SVT_MAX_WIDTH && h <= SVT_MAX_HEIGHT);

    // Compute expected output dimensions for the caller (no extra ffprobe needed).
    let (out_w, out_h) = compute_scaled_dims(w, h, max_width);

    // Build the -vf filter chain.
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
    let threads = ffmpeg_thread_count().to_string();

    let args: Vec<&str> = if use_svt {
        vec![
            "-y",
            "-threads",
            &threads,
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
            "-threads",
            &threads,
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

    let out = priority_cmd("ffmpeg", &args)
        .output()
        .await
        .context("ffmpeg avif")?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        anyhow::bail!("ffmpeg avif failed: {}", stderr);
    }

    debug!(
        input = %input.display(),
        output = %output.display(),
        out_w,
        out_h,
        "converted to avif"
    );
    Ok((out_w, out_h))
}

/// Normalize an exotic image format to JPEG via ffmpeg so the `image` crate
/// can decode it (avif, jxl …).  Only call this for formats not natively
/// supported by the `image` crate (i.e. NOT jpeg/png/webp/gif/bmp/tiff).
pub async fn normalize_to_jpeg(input: &Path, output: &Path) -> Result<()> {
    let input_str = input.to_string_lossy();
    let output_str = output.to_string_lossy();
    let threads = ffmpeg_thread_count().to_string();

    let args = [
        "-y",
        "-threads",
        &threads,
        "-i",
        &input_str,
        "-frames:v",
        "1",
        "-q:v",
        "2",
        &output_str,
    ];

    let out = priority_cmd("ffmpeg", &args)
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
    let threads = ffmpeg_thread_count().to_string();

    let args = [
        "-threads",
        &threads,
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

    let out = priority_cmd("ffmpeg", &args)
        .output()
        .await
        .context("ffmpeg video frame")?;

    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        anyhow::bail!("ffmpeg video frame failed: {}", stderr);
    }

    Ok(())
}

// ── Internal helpers ──────────────────────────────────────────────────────────

fn parse_dimensions(stdout: &[u8]) -> (i32, i32) {
    let s = String::from_utf8_lossy(stdout);
    let parts: Vec<&str> = s.trim().split(',').collect();
    if parts.len() >= 2 {
        let w = parts[0].parse().unwrap_or(0);
        let h = parts[1].parse().unwrap_or(0);
        (w, h)
    } else {
        (0, 0)
    }
}

/// Compute output dimensions after applying the scale='min(max_w,iw)':-2 filter.
/// Returns `(0, 0)` when source dimensions are unknown.
pub fn compute_scaled_dims(w: i32, h: i32, max_width: u32) -> (i32, i32) {
    if w == 0 || h == 0 {
        return (0, 0);
    }
    if max_width == 0 || w <= max_width as i32 {
        // No scaling — even-dimension crop at most clips 1px each axis.
        return (w & !1, h & !1);
    }
    let out_w = max_width as i32 & !1;
    let scale = max_width as f64 / w as f64;
    let out_h = ((h as f64 * scale).round() as i32) & !1;
    (out_w, out_h)
}
