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
pub(crate) fn ffmpeg_thread_count() -> u32 {
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

pub(crate) fn parse_dimensions(stdout: &[u8]) -> (i32, i32) {
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

// ── Unit tests ────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::Path;
    use tempfile::TempDir;

    // ── parse_dimensions ────────────────────────────────────────────────────────

    #[test]
    fn test_parse_dimensions_standard() {
        assert_eq!(parse_dimensions(b"1920,1080\n"), (1920, 1080));
    }

    #[test]
    fn test_parse_dimensions_no_newline() {
        assert_eq!(parse_dimensions(b"640,480"), (640, 480));
    }

    #[test]
    fn test_parse_dimensions_with_whitespace() {
        assert_eq!(parse_dimensions(b"1280,720 \n"), (1280, 720));
    }

    #[test]
    fn test_parse_dimensions_empty_input() {
        assert_eq!(parse_dimensions(b""), (0, 0));
    }

    #[test]
    fn test_parse_dimensions_single_value() {
        // Only one number — must fall back to (0, 0).
        assert_eq!(parse_dimensions(b"1920"), (0, 0));
    }

    #[test]
    fn test_parse_dimensions_non_numeric() {
        assert_eq!(parse_dimensions(b"abc,def"), (0, 0));
    }

    #[test]
    fn test_parse_dimensions_partial_non_numeric() {
        // First field non-numeric → both default to 0.
        assert_eq!(parse_dimensions(b"abc,1080"), (0, 1080));
    }

    #[test]
    fn test_parse_dimensions_4k() {
        assert_eq!(parse_dimensions(b"3840,2160"), (3840, 2160));
    }

    #[test]
    fn test_parse_dimensions_extra_fields() {
        // Extra comma-separated fields should be ignored (first two win).
        assert_eq!(parse_dimensions(b"1920,1080,30"), (1920, 1080));
    }

    // ── compute_scaled_dims ──────────────────────────────────────────────────

    #[test]
    fn test_scaled_dims_no_max_width() {
        // max_width=0 means no scaling; even crop only.
        assert_eq!(compute_scaled_dims(1920, 1080, 0), (1920, 1080));
    }

    #[test]
    fn test_scaled_dims_under_max() {
        // Source already smaller than max_width → no scaling.
        assert_eq!(compute_scaled_dims(800, 600, 1920), (800, 600));
    }

    #[test]
    fn test_scaled_dims_exact_max() {
        // Source width equals max_width → no scaling.
        assert_eq!(compute_scaled_dims(1920, 1080, 1920), (1920, 1080));
    }

    #[test]
    fn test_scaled_dims_downscale_4k_to_1080p() {
        let (w, h) = compute_scaled_dims(3840, 2160, 1920);
        assert_eq!(w, 1920);
        assert_eq!(h, 1080);
    }

    #[test]
    fn test_scaled_dims_zero_source() {
        assert_eq!(compute_scaled_dims(0, 0, 1920), (0, 0));
    }

    #[test]
    fn test_scaled_dims_zero_w_only() {
        assert_eq!(compute_scaled_dims(0, 1080, 1920), (0, 0));
    }

    #[test]
    fn test_scaled_dims_even_output() {
        // All output dimensions must be even (YUV420 requirement).
        let (w, h) = compute_scaled_dims(1919, 1079, 0);
        assert_eq!(w % 2, 0, "width must be even");
        assert_eq!(h % 2, 0, "height must be even");
    }

    #[test]
    fn test_scaled_dims_odd_max_width_rounds_down() {
        // max_width=921 is odd → out_w should be even (920).
        let (w, _h) = compute_scaled_dims(1920, 1080, 921);
        assert_eq!(w % 2, 0);
        assert!(w <= 921);
    }

    #[test]
    fn test_scaled_dims_tall_portrait() {
        // Portrait video: 1080×1920, scale width to 920.
        // scale = 920/1080 ≈ 0.851, so height becomes 1080 * (1920/1080) * (920/1080)
        // i.e., height = (1920 * 920/1080).round() = 1636 (< 1920).
        let (w, h) = compute_scaled_dims(1080, 1920, 920);
        assert_eq!(w, 920);
        // Aspect ratio preserved: h ≈ 1636 (even, within 2px rounding).
        let expected_h = ((1920f64 * 920.0 / 1080.0).round() as i32) & !1;
        assert!(
            (h - expected_h).abs() <= 2,
            "height {} should be near {} for portrait scaling",
            h,
            expected_h
        );
    }

    // ── ffmpeg_thread_count ───────────────────────────────────────────────────

    #[test]
    fn test_ffmpeg_thread_count_at_least_one() {
        assert!(ffmpeg_thread_count() >= 1);
    }

    // ── get_dimensions (requires ffprobe on PATH) ───────────────────────────

    #[tokio::test]
    async fn test_get_dimensions_real_jpeg() {
        // Skip if ffprobe is not available.
        if std::process::Command::new("ffprobe")
            .arg("-version")
            .output()
            .is_err()
        {
            return;
        }
        let tmp = TempDir::new().unwrap();
        let path = tmp.path().join("test.jpg");
        // Write a minimal synthetic JPEG via the image crate.
        let img = image::DynamicImage::ImageRgb8(
            image::ImageBuffer::from_pixel(320, 240, image::Rgb([128u8, 64, 200]))
        );
        img.save(&path).unwrap();

        let (w, h) = get_dimensions(&path).await;
        assert_eq!(w, 320, "ffprobe width");
        assert_eq!(h, 240, "ffprobe height");
    }

    #[tokio::test]
    async fn test_get_dimensions_nonexistent_returns_zero() {
        let (w, h) = get_dimensions(Path::new("/nonexistent/path/file.jpg")).await;
        assert_eq!((w, h), (0, 0));
    }

    // ── normalize_to_jpeg (requires ffmpeg on PATH) ────────────────────────

    #[tokio::test]
    async fn test_normalize_to_jpeg_from_png() {
        if std::process::Command::new("ffmpeg")
            .arg("-version")
            .output()
            .is_err()
        {
            return;
        }
        let tmp = TempDir::new().unwrap();
        let src = tmp.path().join("input.png");
        let dst = tmp.path().join("output.jpg");
        let img = image::DynamicImage::ImageRgb8(
            image::ImageBuffer::from_pixel(64, 64, image::Rgb([200u8, 100, 50]))
        );
        img.save(&src).unwrap();

        normalize_to_jpeg(&src, &dst).await.unwrap();

        assert!(dst.exists(), "output JPEG must exist");
        assert!(std::fs::metadata(&dst).unwrap().len() > 0, "output must not be empty");
    }

    // ── convert_to_avif (requires ffmpeg on PATH) ─────────────────────────────

    #[tokio::test]
    async fn test_convert_to_avif_with_jpeg_input() {
        if std::process::Command::new("ffmpeg").arg("-version").output().is_err() {
            return;
        }
        let tmp = TempDir::new().unwrap();
        let src = tmp.path().join("input.jpg");
        let dst = tmp.path().join("output.avif");

        let img = image::DynamicImage::ImageRgb8(
            image::ImageBuffer::from_pixel(64, 64, image::Rgb([128u8, 200, 50])),
        );
        img.save(&src).unwrap();

        let result = convert_to_avif(&src, &dst, 32, 12, 0).await;
        assert!(result.is_ok(), "convert_to_avif must succeed: {:?}", result.err());
        assert!(dst.exists(), "output AVIF must exist");
        assert!(
            std::fs::metadata(&dst).unwrap().len() > 0,
            "output AVIF must be non-empty"
        );
    }

    #[tokio::test]
    async fn test_convert_to_avif_with_max_width_downscales() {
        if std::process::Command::new("ffmpeg").arg("-version").output().is_err() {
            return;
        }
        let tmp = TempDir::new().unwrap();
        let src = tmp.path().join("wide.jpg");
        let dst = tmp.path().join("scaled.avif");

        // 200×200 with max_width=100 → 100×100 (svtav1 requires height ≥ 64).
        let img = image::DynamicImage::ImageRgb8(
            image::ImageBuffer::from_pixel(200, 200, image::Rgb([100u8, 100, 100])),
        );
        img.save(&src).unwrap();

        let (w, _h) = convert_to_avif(&src, &dst, 32, 12, 100).await.unwrap();
        assert!(dst.exists(), "scaled AVIF must exist");
        // w==0 when ffprobe is unavailable; otherwise check scaling.
        if w > 0 {
            assert!(w <= 100, "output width {w} must be ≤ 100");
        }
    }

    #[tokio::test]
    async fn test_convert_to_avif_nonexistent_input_fails() {
        let tmp = TempDir::new().unwrap();
        let result = convert_to_avif(
            Path::new("/nonexistent/input.jpg"),
            &tmp.path().join("out.avif"),
            32,
            12,
            0,
        )
        .await;
        assert!(result.is_err(), "must fail on nonexistent input");
    }

    // ── extract_video_frame (requires ffmpeg with libx264) ────────────────────

    /// Synthesise a tiny MP4 test fixture using ffmpeg lavfi; returns None when
    /// ffmpeg is unavailable or libx264 is not compiled in.
    async fn make_test_mp4(dir: &Path) -> Option<std::path::PathBuf> {
        let out = dir.join("test.mp4");
        let o = tokio::process::Command::new("ffmpeg")
            .args([
                "-y",
                "-f", "lavfi",
                "-i", "testsrc2=size=64x64:duration=2",
                "-c:v", "libx264",
                "-pix_fmt", "yuv420p",
                "-t", "2",
                out.to_str().unwrap(),
            ])
            .output()
            .await
            .ok()?;
        if o.status.success() { Some(out) } else { None }
    }

    #[tokio::test]
    async fn test_extract_video_frame_produces_jpeg() {
        if std::process::Command::new("ffmpeg").arg("-version").output().is_err() {
            return;
        }
        let tmp = TempDir::new().unwrap();
        let video = match make_test_mp4(tmp.path()).await {
            Some(v) => v,
            None => return, // libx264 not compiled in
        };
        let frame = tmp.path().join("frame.jpg");

        extract_video_frame(&video, &frame).await.unwrap();

        assert!(frame.exists(), "frame JPEG must exist");
        assert!(
            std::fs::metadata(&frame).unwrap().len() > 0,
            "frame must be non-empty"
        );
    }

    #[tokio::test]
    async fn test_extract_video_frame_decodable_by_image_crate() {
        if std::process::Command::new("ffmpeg").arg("-version").output().is_err() {
            return;
        }
        let tmp = TempDir::new().unwrap();
        let video = match make_test_mp4(tmp.path()).await {
            Some(v) => v,
            None => return,
        };
        let frame = tmp.path().join("frame.jpg");
        extract_video_frame(&video, &frame).await.unwrap();

        let img = image::open(&frame).expect("frame must be a decodable image");
        assert!(img.width() > 0 && img.height() > 0, "decoded frame must have non-zero dimensions");
    }

    #[tokio::test]
    async fn test_extract_video_frame_nonexistent_video_fails() {
        let tmp = TempDir::new().unwrap();
        let result = extract_video_frame(
            Path::new("/nonexistent/video.mp4"),
            &tmp.path().join("frame.jpg"),
        )
        .await;
        assert!(result.is_err(), "must fail on nonexistent input");
    }
}
