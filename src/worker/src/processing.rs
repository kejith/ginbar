//! Image and video processing pipeline.
//!
//! Key optimizations:
//!
//! 1. **Fully in-process AVIF encoding** – SVT-AV1 v2.3.0 native bindings
//!    encode both full-res and thumbnails entirely in-process.  No ffmpeg or
//!    ffprobe subprocess for images (eliminates fork/exec overhead, double
//!    decode, and IPC).  ffmpeg is only used for video operations.
//!
//! 2. **Turbojpeg JPEG decode** – JPEG files (≈90 % of input) are decoded
//!    via libjpeg-turbo's TurboJPEG API for SIMD-accelerated decoding.
//!    When the JPEG is more than 2× wider than the output target, DCT
//!    downscaling decodes at half resolution (~4× fewer pixels).
//!
//! 3. **Skip ffmpeg normalization for common formats** – JPEG, PNG, WebP,
//!    GIF, BMP and TIFF are decoded directly by the `image` crate.
//!
//! 4. **Parallel encode** – after phash, the image is split: a 150×150
//!    thumbnail crop is extracted (cheap, ~5 ms), then both thumbnail and
//!    full-res SVT-AV1 encodes run simultaneously via `tokio::join!` on
//!    separate blocking threads. No pixel-buffer clone — the crop produces
//!    a small 150×150 buffer, and the full image is moved to the other thread.
//!
//! 5. **Smart thread budget** – thumbnail encodes use 1 thread (negligible
//!    benefit from more at 150 px / preset 10), full-res encodes get the
//!    remaining cores: `max(1, cpus/concurrency - 1)`.
//!
//! 6. **Resize skip + Triangle filter** – when the source is within 15 %
//!    of `max_width`, the resize is skipped entirely (saves 300-700 ms).
//!    For larger downscales, `Triangle` (bilinear) replaces `CatmullRom`
//!    (bicubic) — ~2× faster with imperceptible quality difference after
//!    AV1 compression.
//!
//! 7. **Content-aware thumbnails** – gradient-saliency crop replaces the
//!    naïve center crop, matching the Go `smartcrop` behaviour.
//!
//! 8. **UUID filenames** prevent timestamp-collision races.
//!
//! 9. **DCT perceptual hash** in-process (no `img_hash` dep).
//!
//! 10. **Download/process pipeline** – downloads stream into processing via a
//!     bounded channel, so CPU-bound workers are busy from the first download.

use anyhow::{Context, Result};
use image::imageops::FilterType;
use std::path::{Path, PathBuf};
use tokio::fs;
use tracing::{debug, warn};

use crate::avif;
use crate::ffmpeg;

/// Thumbnail target size (px).
const THUMB_SIZE: u32 = 150;

// ── Directory layout ──────────────────────────────────────────────────────────

/// Media directory paths (mirrors Go's `utils.Directories`).
#[derive(Debug, Clone)]
pub struct Directories {
    pub image: PathBuf,
    pub thumbnail: PathBuf,
    pub video: PathBuf,
    pub tmp: PathBuf,
    pub upload: PathBuf,
}

impl Directories {
    /// Build default paths relative to the current working directory.
    pub fn from_cwd() -> Self {
        let cwd = std::env::current_dir().unwrap_or_else(|_| PathBuf::from("."));
        Self {
            image: cwd.join("public/images"),
            thumbnail: cwd.join("public/images/thumbnails"),
            video: cwd.join("public/videos"),
            tmp: cwd.join("tmp"),
            upload: cwd.join("public/upload"),
        }
    }

    /// Ensure all directories exist.
    pub async fn ensure(&self) -> Result<()> {
        for d in [
            &self.image,
            &self.thumbnail,
            &self.video,
            &self.tmp,
            &self.upload,
            &self.tmp.join("thumbnails"),
        ] {
            fs::create_dir_all(d).await?;
        }
        Ok(())
    }
}

// ── Image processing ──────────────────────────────────────────────────────────

/// Result of processing an image.
pub struct ImageResult {
    pub filename: String,
    pub thumbnail_filename: String,
    pub uploaded_filename: String,
    /// Four 64-bit perceptual-hash components (DCT 16×16 hash).
    pub p_hash: [i64; 4],
    pub width: i32,
    pub height: i32,
}

/// Returns `true` for formats the `image` crate can decode natively without
/// an ffmpeg subprocess (JPEG, PNG, WebP, GIF, BMP, TIFF).
pub(crate) fn can_decode_natively(path: &Path) -> bool {
    let ext = path
        .extension()
        .map(|e| e.to_string_lossy().to_lowercase())
        .unwrap_or_default();
    matches!(
        ext.as_str(),
        "jpg" | "jpeg" | "png" | "webp" | "gif" | "bmp" | "tiff" | "tif"
    )
}

/// Decode a JPEG file using turbojpeg (libjpeg-turbo) for SIMD-accelerated
/// decoding with optional DCT downscaling.
///
/// When `max_decode_width > 0` and the JPEG is more than 2× wider than
/// `max_decode_width`, the image is decoded at half resolution using
/// libjpeg-turbo's built-in IDCT scaling.  This computes only 1/4 of the
/// DCT coefficients, making decode roughly 4× faster for large images.
///
/// The resulting `DynamicImage` is still large enough for all downstream
/// work (phash, thumbnail, full-res encode) since `max_decode_width` is
/// set to the AVIF output width (920 px).
pub fn decode_jpeg_turbo(path: &Path, max_decode_width: u32) -> Result<image::DynamicImage> {
    let data = std::fs::read(path).context("read JPEG file")?;

    let mut decompressor = turbojpeg::Decompressor::new()
        .map_err(|e| anyhow::anyhow!("turbojpeg init: {}", e))?;

    let header = decompressor.read_header(&data)
        .map_err(|e| anyhow::anyhow!("turbojpeg header: {}", e))?;

    // DCT downscaling: if the JPEG is much larger than our output target,
    // decode at half resolution. libjpeg-turbo's IDCT scaling is very
    // efficient — roughly 4× fewer pixels to process.
    let use_half = max_decode_width > 0
        && header.width > max_decode_width as usize * 2;

    let (width, height) = if use_half {
        let scale = turbojpeg::ScalingFactor::new(1, 2);
        decompressor.set_scaling_factor(scale)
            .map_err(|e| anyhow::anyhow!("turbojpeg set scale: {}", e))?;
        let scaled = header.scaled(scale);
        debug!(
            original_w = header.width,
            original_h = header.height,
            scaled_w = scaled.width,
            scaled_h = scaled.height,
            "decode_jpeg_turbo: DCT downscale 1/2"
        );
        (scaled.width, scaled.height)
    } else {
        (header.width, header.height)
    };

    let pitch = width * 3; // RGB, tightly packed
    let mut pixels = vec![0u8; pitch * height];
    let dest = turbojpeg::Image {
        pixels: &mut pixels[..],
        width,
        pitch,
        height,
        format: turbojpeg::PixelFormat::RGB,
    };

    decompressor.decompress(&data, dest)
        .map_err(|e| anyhow::anyhow!("turbojpeg decompress: {}", e))?;

    let rgb = image::RgbImage::from_raw(width as u32, height as u32, pixels)
        .context("convert decoded pixels to RgbImage")?;

    Ok(image::DynamicImage::ImageRgb8(rgb))
}

/// Number of threads for the thumbnail SVT-AV1 encode.
///
/// Thumbnails are 150×150 at preset 10 — so fast that multi-threading has
/// no measurable benefit. Always returns 1.
pub(crate) fn thumbnail_encode_threads() -> u32 {
    1
}

/// Number of threads for the full-resolution SVT-AV1 encode.
///
/// Each worker runs one thumbnail encode (1 thread) and one full-res encode
/// (N threads) simultaneously.  Budget: `N = max(1, cpus/concurrency - 1)`
/// so total threads ≈ `concurrency × (1 + N) ≤ cpus`.
///
/// Override with `SVT_AV1_THREADS` to set an explicit value.
pub(crate) fn fullres_encode_threads() -> u32 {
    if let Ok(v) = std::env::var("SVT_AV1_THREADS") {
        if let Ok(n) = v.parse::<u32>() {
            if n > 0 {
                return n;
            }
        }
    }
    let cpus = num_cpus::get() as u32;
    let concurrency: u32 = std::env::var("WORKER_CONCURRENCY")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(4);
    // Each worker's thumbnail uses 1 thread; give the rest to full-res.
    ((cpus / concurrency).saturating_sub(1)).max(1)
}

/// Load an image into memory.
///
/// For JPEG files, uses turbojpeg (libjpeg-turbo) for SIMD-accelerated
/// decoding with optional DCT downscaling.  When the JPEG is more than
/// 2× wider than `max_decode_width`, it is decoded at half resolution
/// directly during the IDCT step — roughly 4× fewer pixels to process.
///
/// For other natively-decodable formats (PNG/WebP/GIF/BMP/TIFF) the file
/// is opened via the `image` crate — no subprocess required.
///
/// For everything else (AVIF, JXL, …) an ffmpeg subprocess normalizes it to
/// a temporary JPEG first, which is then decoded and cleaned up.
///
/// Returns `(image, Option<tmp_path_to_remove>)`.
async fn load_image(
    input: &Path,
    dirs: &Directories,
) -> Result<(image::DynamicImage, Option<PathBuf>)> {
    if can_decode_natively(input) {
        let is_jpeg = matches!(
            input.extension().map(|e| e.to_string_lossy().to_lowercase()).as_deref(),
            Some("jpg" | "jpeg")
        );
        let input = input.to_path_buf();
        let decode_start = std::time::Instant::now();
        let img = if is_jpeg {
            // Fast path: turbojpeg with optional DCT downscaling.
            let p = input.clone();
            match tokio::task::spawn_blocking(move || decode_jpeg_turbo(&p, 920)).await? {
                Ok(img) => {
                    debug!(
                        elapsed_ms = decode_start.elapsed().as_millis(),
                        "load_image: turbojpeg decode"
                    );
                    img
                }
                Err(e) => {
                    // Fallback to image crate if turbojpeg fails.
                    warn!(err = %e, "turbojpeg decode failed, falling back to image::open");
                    let fb_start = std::time::Instant::now();
                    let img = tokio::task::spawn_blocking(move || image::open(&input))
                        .await?
                        .context("decode image (fallback)")?;
                    debug!(
                        elapsed_ms = fb_start.elapsed().as_millis(),
                        "load_image: fallback native decode"
                    );
                    img
                }
            }
        } else {
            let img = tokio::task::spawn_blocking(move || image::open(&input))
                .await?
                .context("decode image")?;
            debug!(
                elapsed_ms = decode_start.elapsed().as_millis(),
                "load_image: native decode"
            );
            img
        };
        return Ok((img, None));
    }

    // Exotic format — normalize to JPEG via ffmpeg.
    let tmp_name = format!("{}.jpg", generate_name());
    let tmp_path = dirs.tmp.join("thumbnails").join(&tmp_name);

    let ffmpeg_start = std::time::Instant::now();
    ffmpeg::normalize_to_jpeg(input, &tmp_path).await?;
    debug!(
        path = %input.display(),
        elapsed_ms = ffmpeg_start.elapsed().as_millis(),
        "load_image: ffmpeg normalization to JPEG"
    );

    let tmp_clone = tmp_path.clone();
    let decode_start = std::time::Instant::now();
    let img = tokio::task::spawn_blocking(move || image::open(&tmp_clone))
        .await?
        .context("decode normalized JPEG")?;
    debug!(
        elapsed_ms = decode_start.elapsed().as_millis(),
        "load_image: decode of normalized JPEG"
    );

    Ok((img, Some(tmp_path)))
}

/// Process a single image: encode to AVIF, compute perceptual hash, create thumbnail.
///
/// Everything runs in-process via SVT-AV1 native bindings — no ffmpeg subprocess.
///
/// **Fast path** (JPEG ≤ 1058 px wide):
///   1. Read bytes; concurrently decode to:
///      a. Full-res YUV420 planes (turbojpeg native — skips rgb_to_yuv420,
///         saving ~80-110 ms).
///      b. ≤512 px RGB image (for phash and thumbnail).
///   2. Three threads/tasks run simultaneously:
///      - Thread A: compute perceptual hash on the 512 px image.
///      - Thread B: smart-crop + resize → encode thumbnail AVIF.
///      - Task  C: encode full-res AVIF directly from YUV420 planes.
///
/// **Fallback** (non-JPEG or JPEG > 1058 px):
///   1. Decode via load_image; downscale to 512 px.
///   2. Same three-way parallel layout, but with the standard limited-range
///      RGB→YUV420 conversion in the full-res encode.
///
/// Typical speedup vs. the old sequential pipeline: ~1.7× for 1052 px JPEGs.
pub async fn process_image(input: &Path, dirs: &Directories) -> Result<ImageResult> {
    let fn_start = std::time::Instant::now();
    let file_name   = input
        .file_name()
        .context("no filename")?
        .to_string_lossy()
        .to_string();
    let avif_name   = format!("{}.avif", file_name);
    let output_path = dirs.image.join(&avif_name);

    // Fast path only applies to JPEG input.
    let is_jpeg = matches!(
        input.extension().map(|e| e.to_string_lossy().to_lowercase()).as_deref(),
        Some("jpg" | "jpeg")
    );
    if !is_jpeg {
        return process_image_fallback(input, dirs).await;
    }

    // Read bytes once; share between the two concurrent decode tasks.
    let data = std::sync::Arc::new(
        tokio::fs::read(input).await.context("read JPEG bytes")?,
    );

    // Check JPEG width: only use YUV-native path when no resize is needed.
    // max_width=920, resize-skip ratio=1.15 → threshold ≈ 1058 px.
    const MAX_ENCODE_WIDTH: u32 = 920;
    const RESIZE_SKIP_RATIO: f64 = 1.15;
    let threshold_w = (MAX_ENCODE_WIDTH as f64 * RESIZE_SKIP_RATIO) as u32;

    let hdr = turbojpeg::read_header(&*data)
        .map_err(|e| anyhow::anyhow!("turbojpeg header peek: {}", e))?;
    // Fall back if the image needs resizing OR if the JPEG chroma subsampling
    // is not 4:2:0.  `turbojpeg::decompress_to_yuv` always outputs in the
    // *source* subsampling (the `subsamp` field in `YuvImage` is ignored by
    // libjpeg-turbo).  SVT-AV1 requires 4:2:0 input, so 4:4:4 or 4:2:2
    // source images must go through the standard RGB→YUV420 conversion path.
    if hdr.width as u32 > threshold_w
        || hdr.subsamp != turbojpeg::Subsamp::Sub2x2
    {
        return process_image_fallback(input, dirs).await;
    }

    let data_yuv = data.clone();
    let data_rgb = data.clone();

    // Decode full-res → YUV420 planes (no RGB conversion) and ≤512 px RGB
    // (for phash + thumbnail), concurrently.
    let (yuv_r, small_r) = tokio::join!(
        tokio::task::spawn_blocking(move || -> Result<(Vec<u8>, u32, u32, usize, usize)> {
            decode_jpeg_to_yuv_planes(&data_yuv)
        }),
        tokio::task::spawn_blocking(move || -> Result<image::DynamicImage> {
            let img = decode_jpeg_turbo_from_data(&data_rgb, 512)?;
            let img = if img.width() > 512 {
                img.resize(512, 512, image::imageops::FilterType::Triangle)
            } else {
                img
            };
            Ok(img)
        }),
    );

    let (yuv_buf, yuv_w, yuv_h, y_len, uv_len) =
        yuv_r.context("YUV decode task panic")?.context("YUV decode failed")?;
    let small =
        small_r.context("RGB-512 decode task panic")?.context("RGB-512 decode failed")?;

    // Ensure even dimensions for SVT-AV1 / YUV420.
    let enc_w      = yuv_w & !1;
    let enc_h      = yuv_h & !1;
    let y_stride   = enc_w as usize;
    let uv_stride  = enc_w as usize / 2;
    let y_enc_len  = y_stride * enc_h as usize;
    let uv_enc_len = uv_stride * enc_h as usize / 2;

    let thumb_name  = format!("{}.avif", file_name);
    let thumb_path  = dirs.thumbnail.join(&thumb_name);
    let avif_output = output_path.clone();

    // Three-way parallel: (phash ∥ crop→thumb-encode)  vs  full-res YUV encode.
    let (phash_thumb_result, fullres_result) = tokio::join!(
        tokio::task::spawn_blocking(move || -> Result<([i64; 4], ())> {
            let img = &small;
            let mut p_hash = [0i64; 4];
            let mut thumb_img: Option<image::DynamicImage> = None;
            std::thread::scope(|s| {
                let ph = s.spawn(|| compute_phash(img));
                let th = s.spawn(|| prepare_thumbnail_pixels(img));
                p_hash    = ph.join().expect("phash panic");
                thumb_img = Some(th.join().expect("crop panic"));
            });
            avif::encode_avif(
                &thumb_img.unwrap(), &thumb_path, 40, 10, 150,
                thumbnail_encode_threads(),
            )
            .context("thumbnail AVIF encode failed")?;
            Ok((p_hash, ()))
        }),
        tokio::task::spawn_blocking(move || -> Result<(i32, i32)> {
            let y = &yuv_buf[..y_enc_len];
            let u = &yuv_buf[y_len..y_len + uv_enc_len];
            let v = &yuv_buf[y_len + uv_len..y_len + uv_len + uv_enc_len];
            avif::encode_avif_from_yuv_planes(
                y, u, v, enc_w, enc_h, &avif_output,
                18, 8, fullres_encode_threads(),
            )
        }),
    );

    let (p_hash, _) = phash_thumb_result?.context("phash/thumb task failed")?;
    let (out_w, out_h) = fullres_result?.context("full-res YUV encode failed")?;

    debug!(
        path = %input.display(),
        total_elapsed_ms = fn_start.elapsed().as_millis(),
        "processing: process_image complete (yuv-native path)"
    );
    Ok(ImageResult {
        filename:           avif_name,
        thumbnail_filename: thumb_name,
        uploaded_filename:  file_name,
        p_hash,
        width:  out_w,
        height: out_h,
    })
}

// ── Decode helpers for the parallel pipeline ─────────────────────────────────

/// Decode JPEG bytes to tight-packed (align=1) full-range YUV420 planes.
///
/// Returns `(buf, width, height, y_len, uv_len)`.  The YUV buffer layout is
/// `[Y | Cb | Cr]` with no row padding (strides: y_stride = width,
/// uv_stride = ceil(width/2)).
///
/// **Caller contract**: only call when the source JPEG uses 4:2:0 subsampling
/// (`hdr.subsamp == Sub2x2`).  `turbojpeg::decompress_to_yuv` ignores the
/// `subsamp` field in `YuvImage` and always outputs in the *source* format, so
/// calling this for 4:4:4 or 4:2:2 images would overflow the buffer and cause
/// undefined behaviour.  Returns an error if the source is not Sub2x2.
pub fn decode_jpeg_to_yuv_planes(
    data: &[u8],
) -> Result<(Vec<u8>, u32, u32, usize, usize)> {
    let mut d = turbojpeg::Decompressor::new()
        .map_err(|e| anyhow::anyhow!("turbojpeg init: {}", e))?;
    let header = d
        .read_header(data)
        .map_err(|e| anyhow::anyhow!("turbojpeg header: {}", e))?;

    // Safety: turbojpeg always outputs in the *source* subsampling.
    // Buffer is allocated for Sub2x2, which is only correct if the source is
    // already Sub2x2.  Return early for any other subsampling.
    anyhow::ensure!(
        header.subsamp == turbojpeg::Subsamp::Sub2x2,
        "decode_jpeg_to_yuv_planes: source subsamp is {:?}, require Sub2x2 (4:2:0)",
        header.subsamp
    );

    let w = header.width;
    let h = header.height;

    // Allocate tight-packed YUV buffer (align=1 → no row padding).
    // Buffer size is for the SOURCE Sub2x2 layout (validated above).
    let total = turbojpeg::yuv_pixels_len(w, 1, h, turbojpeg::Subsamp::Sub2x2)
        .map_err(|e| anyhow::anyhow!("yuv_pixels_len: {}", e))?;
    let mut buf = vec![0u8; total];

    let yuv_out = turbojpeg::YuvImage {
        pixels: &mut buf[..],
        width:  w,
        align:  1,
        height: h,
        subsamp: turbojpeg::Subsamp::Sub2x2, // field is ignored by turbojpeg
    };
    d.decompress_to_yuv(data, yuv_out)
        .map_err(|e| anyhow::anyhow!("turbojpeg YUV decode: {}", e))?;

    // Compute per-plane lengths using Sub2x2 metadata.
    let meta = turbojpeg::YuvImage {
        pixels: &[] as &[u8],
        width:  w,
        align:  1,
        height: h,
        subsamp: turbojpeg::Subsamp::Sub2x2,
    };
    let y_len  = meta.y_width()  * meta.y_height();
    let uv_len = meta.uv_width() * meta.uv_height();

    Ok((buf, w as u32, h as u32, y_len, uv_len))
}

/// Decode JPEG from in-memory bytes to a `DynamicImage`, with optional DCT
/// downscaling.
///
/// Same logic as [`decode_jpeg_turbo`] but accepts pre-read bytes instead of a
/// file path — useful when the data is already in memory for variant D where
/// the same bytes are also decoded to YUV in a concurrent task.
pub fn decode_jpeg_turbo_from_data(
    data: &[u8],
    max_decode_width: u32,
) -> Result<image::DynamicImage> {
    let mut decompressor = turbojpeg::Decompressor::new()
        .map_err(|e| anyhow::anyhow!("turbojpeg init: {}", e))?;
    let header = decompressor
        .read_header(data)
        .map_err(|e| anyhow::anyhow!("turbojpeg header: {}", e))?;

    let use_half = max_decode_width > 0
        && header.width > max_decode_width as usize * 2;

    let (width, height) = if use_half {
        let scale = turbojpeg::ScalingFactor::new(1, 2);
        decompressor
            .set_scaling_factor(scale)
            .map_err(|e| anyhow::anyhow!("turbojpeg set scale: {}", e))?;
        let scaled = header.scaled(scale);
        (scaled.width, scaled.height)
    } else {
        (header.width, header.height)
    };

    let pitch = width * 3;
    let mut pixels = vec![0u8; pitch * height];
    let dest = turbojpeg::Image {
        pixels: &mut pixels[..],
        width,
        pitch,
        height,
        format: turbojpeg::PixelFormat::RGB,
    };

    decompressor
        .decompress(data, dest)
        .map_err(|e| anyhow::anyhow!("turbojpeg decompress: {}", e))?;

    let rgb = image::RgbImage::from_raw(width as u32, height as u32, pixels)
        .context("convert decoded pixels to RgbImage")?;
    Ok(image::DynamicImage::ImageRgb8(rgb))
}

// ── Fallback pipeline (large / non-JPEG images) ─────────────────────────────

/// Three-way concurrent pipeline used when `process_image` cannot take the
/// YUV-native fast path (non-JPEG or JPEG > 1058 px).
///
/// After a one-time 512 px downscale, three operations run simultaneously:
///   1. Thread A: compute phash on the small image.
///   2. Thread B: smart-crop + resize → encode thumbnail AVIF.
///   3. Task  C: SVT-AV1 full-resolution encode (with RGB→YUV420 conversion).
async fn process_image_fallback(input: &Path, dirs: &Directories) -> Result<ImageResult> {
    let fn_start = std::time::Instant::now();
    let file_name   = input
        .file_name()
        .context("no filename")?
        .to_string_lossy()
        .to_string();
    let avif_name   = format!("{}.avif", file_name);
    let output_path = dirs.image.join(&avif_name);

    let (img, tmp_path) = load_image(input, dirs).await?;
    let img = std::sync::Arc::new(img);

    // Pre-downscale to ≤512 px for phash and thumbnail.
    let img_c = img.clone();
    let small = tokio::task::spawn_blocking(move || {
        img_c.resize(512, 512, image::imageops::FilterType::Triangle)
    })
    .await?;

    let thumb_name  = format!("{}.avif", file_name);
    let thumb_path  = dirs.thumbnail.join(&thumb_name);
    let avif_output = output_path.clone();
    let img_full    = img.clone();

    // Three-way parallel: (phash ∥ crop→thumb-encode)  vs  full-res encode.
    let (phash_thumb_result, fullres_result) = tokio::join!(
        tokio::task::spawn_blocking(move || -> Result<([i64; 4], ())> {
            let img = &small;
            let mut p_hash = [0i64; 4];
            let mut thumb_img: Option<image::DynamicImage> = None;
            std::thread::scope(|s| {
                let ph = s.spawn(|| compute_phash(img));
                let th = s.spawn(|| prepare_thumbnail_pixels(img));
                p_hash    = ph.join().expect("phash panic");
                thumb_img = Some(th.join().expect("crop panic"));
            });
            avif::encode_avif(
                &thumb_img.unwrap(), &thumb_path, 40, 10, 150,
                thumbnail_encode_threads(),
            )
            .context("thumbnail AVIF encode failed")?;
            Ok((p_hash, ()))
        }),
        tokio::task::spawn_blocking(move || {
            avif::encode_avif(&*img_full, &avif_output, 18, 8, 920,
                              fullres_encode_threads())
        }),
    );

    let (p_hash, _) = phash_thumb_result?.context("phash/thumb task failed")?;
    let (out_w, out_h) = fullres_result?.context("full-res AVIF encode failed")?;

    if let Some(p) = tmp_path {
        let _ = fs::remove_file(&p).await;
    }

    debug!(
        path = %input.display(),
        total_elapsed_ms = fn_start.elapsed().as_millis(),
        "processing: process_image complete (fallback path)"
    );
    Ok(ImageResult {
        filename:           avif_name,
        thumbnail_filename: thumb_name,
        uploaded_filename:  file_name,
        p_hash,
        width:  out_w,
        height: out_h,
    })
}

/// **Variant B** – pre-downscale to 512 px, then parallel phash + crop.
///
/// A one-time 512 px resize reduces phash input (needs 256 px) and thumbnail
/// crop input (needs 150 px) to ~25 % of the original pixel count, so both
/// complete much faster.  Full-res encode still uses the original image.
pub async fn process_image_v_b(input: &Path, dirs: &Directories) -> Result<ImageResult> {
    let fn_start = std::time::Instant::now();
    let file_name = input
        .file_name()
        .context("no filename")?
        .to_string_lossy()
        .to_string();
    let avif_name   = format!("{}.avif", file_name);
    let output_path = dirs.image.join(&avif_name);

    let (img, tmp_path) = load_image(input, dirs).await?;
    let img = std::sync::Arc::new(img);

    // One-time downscale to ≤512 px (feeds phash and thumbnail crop).
    let img_b = img.clone();
    let small = tokio::task::spawn_blocking(move || {
        img_b.resize(512, 512, image::imageops::FilterType::Triangle)
    })
    .await?;
    let small = std::sync::Arc::new(small);

    // Parallel phash + crop on the small image.
    let small_a = small.clone();
    let (p_hash, thumb_img) = tokio::task::spawn_blocking(move || {
        let s = &*small_a;
        let mut p_hash = [0i64; 4];
        let mut thumb: Option<image::DynamicImage> = None;
        std::thread::scope(|sc| {
            let ph = sc.spawn(|| compute_phash(s));
            let th = sc.spawn(|| prepare_thumbnail_pixels(s));
            p_hash = ph.join().expect("phash panic");
            thumb  = Some(th.join().expect("crop panic"));
        });
        (p_hash, thumb.unwrap())
    })
    .await?;

    let thumb_name  = format!("{}.avif", file_name);
    let thumb_path  = dirs.thumbnail.join(&thumb_name);
    let avif_output = output_path.clone();
    let img_full    = img.clone();

    let (thumb_result, fullres_result) = tokio::join!(
        tokio::task::spawn_blocking(move || {
            avif::encode_avif(&thumb_img, &thumb_path, 40, 10, 150,
                              thumbnail_encode_threads())
        }),
        tokio::task::spawn_blocking(move || {
            avif::encode_avif(&*img_full, &avif_output, 18, 8, 920,
                              fullres_encode_threads())
        }),
    );

    thumb_result?.context("thumbnail AVIF encode failed")?;
    let (out_w, out_h) = fullres_result?.context("full-res AVIF encode failed")?;

    if let Some(p) = tmp_path {
        let _ = fs::remove_file(&p).await;
    }

    debug!(
        total_elapsed_ms = fn_start.elapsed().as_millis(),
        "process_image_v_b complete"
    );
    Ok(ImageResult {
        filename:           avif_name,
        thumbnail_filename: thumb_name,
        uploaded_filename:  file_name,
        p_hash,
        width:  out_w,
        height: out_h,
    })
}

/// **Variant C** – three-way concurrent pipeline (recommended).
///
/// After a one-time 512 px downscale, three operations run simultaneously:
///   1. Thread A: compute phash on the small image.
///   2. Thread B: smart-crop + resize, then thumbnail AVIF encode.
///   3. Task  C: SVT-AV1 full-resolution encode.
///
/// Threads A and B are launched via `std::thread::scope` inside one
/// `spawn_blocking` task; task C runs in a separate `spawn_blocking` task.
/// Both blocking tasks are driven concurrently via `tokio::join!`.
pub async fn process_image_v_c(input: &Path, dirs: &Directories) -> Result<ImageResult> {
    let fn_start = std::time::Instant::now();
    let file_name   = input
        .file_name()
        .context("no filename")?
        .to_string_lossy()
        .to_string();
    let avif_name   = format!("{}.avif", file_name);
    let output_path = dirs.image.join(&avif_name);

    let (img, tmp_path) = load_image(input, dirs).await?;
    let img = std::sync::Arc::new(img);

    // Pre-downscale to ≤512 px.
    let img_c = img.clone();
    let small = tokio::task::spawn_blocking(move || {
        img_c.resize(512, 512, image::imageops::FilterType::Triangle)
    })
    .await?;

    let thumb_name  = format!("{}.avif", file_name);
    let thumb_path  = dirs.thumbnail.join(&thumb_name);
    let avif_output = output_path.clone();
    let img_full    = img.clone();

    // Three-way parallel: (phash ∥ crop→thumb-encode)  vs  full-res encode.
    let (phash_thumb_result, fullres_result) = tokio::join!(
        tokio::task::spawn_blocking(move || -> Result<([i64; 4], ())> {
            let img = &small;
            let mut p_hash = [0i64; 4];
            let mut thumb_img: Option<image::DynamicImage> = None;
            std::thread::scope(|s| {
                let ph = s.spawn(|| compute_phash(img));
                let th = s.spawn(|| prepare_thumbnail_pixels(img));
                p_hash    = ph.join().expect("phash panic");
                thumb_img = Some(th.join().expect("crop panic"));
            });
            avif::encode_avif(
                &thumb_img.unwrap(), &thumb_path, 40, 10, 150,
                thumbnail_encode_threads(),
            )
            .context("thumbnail AVIF encode failed")?;
            Ok((p_hash, ()))
        }),
        tokio::task::spawn_blocking(move || {
            avif::encode_avif(&*img_full, &avif_output, 18, 8, 920,
                              fullres_encode_threads())
        }),
    );

    let (p_hash, _) = phash_thumb_result?.context("phash/thumb task failed")?;
    let (out_w, out_h) = fullres_result?.context("full-res AVIF encode failed")?;

    if let Some(p) = tmp_path {
        let _ = fs::remove_file(&p).await;
    }

    debug!(
        total_elapsed_ms = fn_start.elapsed().as_millis(),
        "process_image_v_c complete"
    );
    Ok(ImageResult {
        filename:           avif_name,
        thumbnail_filename: thumb_name,
        uploaded_filename:  file_name,
        p_hash,
        width:  out_w,
        height: out_h,
    })
}

/// **Variant D** – turbojpeg YUV-native full-res decode + variant-C parallelism.
///
/// For JPEG input whose width is ≤ `MAX_ENCODE_WIDTH × RESIZE_SKIP_RATIO`
/// (920 × 1.15 ≈ 1058 px), avoids the `rgb_to_yuv420` conversion by decoding
/// the full-resolution image directly to YUV420 planes and feeding them
/// straight to SVT-AV1.  A second concurrent turbojpeg decode produces the
/// 512 px RGB image used for phash and thumbnail.
///
/// For larger images (width > 1058 px) or non-JPEG input the function falls
/// back to [`process_image_v_c`], which applies the usual resize and
/// limited-range RGB→YUV conversion.
pub async fn process_image_v_d(input: &Path, dirs: &Directories) -> Result<ImageResult> {
    let fn_start = std::time::Instant::now();
    let file_name   = input
        .file_name()
        .context("no filename")?
        .to_string_lossy()
        .to_string();
    let avif_name   = format!("{}.avif", file_name);
    let output_path = dirs.image.join(&avif_name);

    // Non-JPEG: turbojpeg YUV decode not applicable → fall back.
    let is_jpeg = matches!(
        input.extension().map(|e| e.to_string_lossy().to_lowercase()).as_deref(),
        Some("jpg" | "jpeg")
    );
    if !is_jpeg {
        return process_image_v_c(input, dirs).await;
    }

    // Read bytes once; share between the two concurrent decode tasks.
    let data = std::sync::Arc::new(
        tokio::fs::read(input).await.context("read JPEG bytes")?,
    );

    // Peek at JPEG dimensions to decide whether variant D applies.
    // max_width=920, resize-skip ratio=1.15 → threshold ≈ 1058 px.
    // Images wider than this get resized by encode_avif; our YUV path encodes
    // at native resolution (no resize), so the output would be too large.
    const MAX_ENCODE_WIDTH: u32 = 920;
    const RESIZE_SKIP_RATIO: f64 = 1.15;
    let threshold_w = (MAX_ENCODE_WIDTH as f64 * RESIZE_SKIP_RATIO) as u32;

    let hdr = turbojpeg::read_header(&*data)
        .map_err(|e| anyhow::anyhow!("turbojpeg header peek: {}", e))?;
    if hdr.width as u32 > threshold_w {
        return process_image_v_c(input, dirs).await;
    }

    let data_yuv = data.clone();
    let data_rgb = data.clone();

    // Decode full-res → YUV420 planes (for SVT-AV1, no RGB conversion).
    // Decode full-res → ≤512 px RGB (for phash + thumbnail), concurrently.
    let (yuv_r, small_r) = tokio::join!(
        tokio::task::spawn_blocking(move || -> Result<(Vec<u8>, u32, u32, usize, usize)> {
            decode_jpeg_to_yuv_planes(&data_yuv)
        }),
        tokio::task::spawn_blocking(move || -> Result<image::DynamicImage> {
            let img = decode_jpeg_turbo_from_data(&data_rgb, 512)?;
            // Resize further if turbojpeg DCT downscale didn't reach ≤512.
            let img = if img.width() > 512 {
                img.resize(512, 512, image::imageops::FilterType::Triangle)
            } else {
                img
            };
            Ok(img)
        }),
    );

    let (yuv_buf, yuv_w, yuv_h, y_len, uv_len) =
        yuv_r.context("YUV decode task panic")?.context("YUV decode failed")?;
    let small =
        small_r.context("RGB-512 decode task panic")?.context("RGB-512 decode failed")?;

    // Ensure even dimensions for SVT-AV1 / YUV420.
    // turbojpeg rounds the allocated buffer up to even rows, so we can safely
    // slice the buffer to enc_w × enc_h without referencing out-of-range data.
    let enc_w      = yuv_w & !1;
    let enc_h      = yuv_h & !1;
    let y_stride   = enc_w as usize;
    let uv_stride  = enc_w as usize / 2;          // enc_w is guaranteed even
    let y_enc_len  = y_stride * enc_h as usize;
    let uv_enc_len = uv_stride * enc_h as usize / 2;

    let thumb_name  = format!("{}.avif", file_name);
    let thumb_path  = dirs.thumbnail.join(&thumb_name);
    let avif_output = output_path.clone();

    // Three-way parallel: (phash ∥ crop→thumb-encode)  vs  full-res YUV encode.
    let (phash_thumb_result, fullres_result) = tokio::join!(
        tokio::task::spawn_blocking(move || -> Result<([i64; 4], ())> {
            let img = &small;
            let mut p_hash = [0i64; 4];
            let mut thumb_img: Option<image::DynamicImage> = None;
            std::thread::scope(|s| {
                let ph = s.spawn(|| compute_phash(img));
                let th = s.spawn(|| prepare_thumbnail_pixels(img));
                p_hash    = ph.join().expect("phash panic");
                thumb_img = Some(th.join().expect("crop panic"));
            });
            avif::encode_avif(
                &thumb_img.unwrap(), &thumb_path, 40, 10, 150,
                thumbnail_encode_threads(),
            )
            .context("thumbnail AVIF encode failed")?;
            Ok((p_hash, ()))
        }),
        tokio::task::spawn_blocking(move || -> Result<(i32, i32)> {
            // Slice the tight-packed YUV buffer into separate planes.
            // y_len / uv_len are the *allocated* sizes (may include a padding
            // row when the original height was odd); y_enc_len / uv_enc_len
            // are the *encoding* sizes (always even-dimensioned).
            let y = &yuv_buf[..y_enc_len];
            let u = &yuv_buf[y_len..y_len + uv_enc_len];
            let v = &yuv_buf[y_len + uv_len..y_len + uv_len + uv_enc_len];
            avif::encode_avif_from_yuv_planes(
                y, u, v, enc_w, enc_h, &avif_output,
                18, 8, fullres_encode_threads(),
            )
        }),
    );

    let (p_hash, _) = phash_thumb_result?.context("phash/thumb task failed")?;
    let (out_w, out_h) = fullres_result?.context("full-res YUV encode failed")?;

    debug!(
        total_elapsed_ms = fn_start.elapsed().as_millis(),
        "process_image_v_d complete"
    );
    Ok(ImageResult {
        filename:           avif_name,
        thumbnail_filename: thumb_name,
        uploaded_filename:  file_name,
        p_hash,
        width:  out_w,
        height: out_h,
    })
}

/// Extract a 150×150 thumbnail-ready image via smart-crop + Triangle resize.
///
/// Uses `Triangle` (bilinear) instead of `CatmullRom` (bicubic) — about 2×
/// faster with imperceptible quality difference at 150 px.  This is a pure
/// CPU operation that runs synchronously. The resulting `DynamicImage` is
/// small (150×150 ≈ 67 KB RGB) and can be moved cheaply to a blocking
/// thread for encoding.
pub fn prepare_thumbnail_pixels(img: &image::DynamicImage) -> image::DynamicImage {
    let cropped = smart_crop(img, THUMB_SIZE, THUMB_SIZE);
    cropped.resize_exact(THUMB_SIZE, THUMB_SIZE, FilterType::Triangle)
}

// ── Perceptual hash (DCT, 256-bit) ────────────────────────────────────────────

/// Compute a 256-bit DCT perceptual hash, returned as four `i64` components.
///
/// Algorithm matches Go's `goimagehash.ExtPerceptionHash(img, 16, 16)`:
/// 1. Resize to 256×256 grayscale  (imgSize = hashW × hashH = 16 × 16 = 256).
/// 2. Apply a separable 2-D DCT-II (row-then-column), keeping only the
///    top-left 16×16 low-frequency coefficients.
/// 3. Compute the **median** of those 256 coefficients.
/// 4. Bit[i] = 1 if coeff[i] > median, else 0.
/// 5. Pack 64 bits per `i64` in big-endian bit order (MSB-first, matching Go).
pub fn compute_phash(img: &image::DynamicImage) -> [i64; 4] {
    const IMG_SIZE: usize = 256; // hashW * hashH  (Go: width * height)
    const HASH_W: usize = 16;
    const HASH_H: usize = 16;
    const HASH_BITS: usize = HASH_W * HASH_H; // 256

    // Step 1: resize to 256×256 grayscale.
    // Go uses resize.Bilinear → FilterType::Triangle is the equivalent.
    let small = img.resize_exact(IMG_SIZE as u32, IMG_SIZE as u32, FilterType::Triangle);
    let gray = small.to_luma8();

    // Build pixel buffer as f64 (matching Go's float64 precision).
    let mut pixels = vec![0f64; IMG_SIZE * IMG_SIZE];
    for y in 0..IMG_SIZE {
        for x in 0..IMG_SIZE {
            pixels[y * IMG_SIZE + x] = gray.get_pixel(x as u32, y as u32).0[0] as f64;
        }
    }

    // Step 2: separable 2-D DCT-II.
    // Only compute the first HASH_W coefficients per row (the rest are unused).
    let mut dct_rows = vec![0f64; IMG_SIZE * HASH_W];
    for y in 0..IMG_SIZE {
        dct1d_partial(
            &pixels[y * IMG_SIZE..(y + 1) * IMG_SIZE],
            &mut dct_rows[y * HASH_W..(y + 1) * HASH_W],
            HASH_W,
        );
    }

    // Column transform: for each of the 16 kept columns, compute only the
    // first HASH_H (16) output coefficients from 256 row-DCT values.
    let mut coeffs = [0f64; HASH_BITS];
    let mut col_in = vec![0f64; IMG_SIZE];
    let mut col_out = [0f64; HASH_H];
    for x in 0..HASH_W {
        for y in 0..IMG_SIZE {
            col_in[y] = dct_rows[y * HASH_W + x];
        }
        dct1d_partial(&col_in, &mut col_out, HASH_H);
        for y in 0..HASH_H {
            coeffs[y * HASH_W + x] = col_out[y];
        }
    }

    // Step 3: **median** of the 256 DCT coefficients (matching Go's etcs.MedianOfPixels).
    // Using mean here was the root cause of false-positive duplicates: the DC
    // coefficient dominates the mean → most bits are 0 for every image →
    // hamming distance between any two images is artificially low.
    let median = median_of_256(&coeffs);

    // Step 4: threshold and pack into 4 × i64, big-endian bit order (matching Go).
    // Go: indexOfBit = 64 - idx%64 - 1  →  first coefficient = MSB of phash[0].
    let mut result = [0i64; 4];
    for (i, &c) in coeffs.iter().enumerate() {
        if c > median {
            let array_idx = i / 64;
            let bit_idx = 63 - (i % 64);
            result[array_idx] |= 1i64 << bit_idx;
        }
    }
    result
}

/// 1-D DCT-II computing only the first `k_max` output coefficients from an
/// input of length `n`.  O(k_max × n) — for k_max=16, n=256 this is ~4 096
/// multiplies per call, perfectly adequate.
pub fn dct1d_partial(input: &[f64], output: &mut [f64], k_max: usize) {
    use std::f64::consts::PI;
    let n = input.len();
    let factor = PI / (2.0 * n as f64);
    for k in 0..k_max {
        let mut sum = 0f64;
        for (x, &v) in input.iter().enumerate() {
            sum += v * (factor * k as f64 * (2 * x + 1) as f64).cos();
        }
        output[k] = sum;
    }
}

/// Median of exactly 256 f64 values.
pub(crate) fn median_of_256(values: &[f64; HASH_BITS_LEN]) -> f64 {
    let mut sorted = values.to_vec();
    sorted.sort_by(|a, b| a.partial_cmp(b).unwrap_or(std::cmp::Ordering::Equal));
    // Even length: average of the two middle values (index 127 and 128).
    (sorted[127] + sorted[128]) / 2.0
}

const HASH_BITS_LEN: usize = 256;

// ── Thumbnail creation (fully in-process) ────────────────────────────────────

/// Create a 150×150 content-aware thumbnail, fully in-process.
///
/// Pipeline:
///   1. Smart-crop to a square (gradient saliency) — in-process, <5 ms.
///   2. Resize to 150×150 with CatmullRom (2× faster than Lanczos3) — in-process.
///   3. Encode as AVIF via SVT-AV1 — in-process on a blocking thread, ~5-10 ms.
///
/// No ffmpeg subprocess, no temporary JPEG files.
/// Used by the video path; images use `prepare_thumbnail_pixels` + parallel encode.
pub async fn create_thumbnail(img: &image::DynamicImage, dst: &Path, _dirs: &Directories) -> Result<()> {
    let crop_start = std::time::Instant::now();
    let thumb_img = prepare_thumbnail_pixels(img);
    debug!(
        elapsed_ms = crop_start.elapsed().as_millis(),
        "create_thumbnail: smart_crop + resize"
    );

    let encode_start = std::time::Instant::now();
    let dst = dst.to_path_buf();
    tokio::task::spawn_blocking(move || {
        avif::encode_avif(&thumb_img, &dst, 40, 10, 150, thumbnail_encode_threads())
    })
        .await?
        .context("SVT-AV1 thumbnail encode")?;
    debug!(
        elapsed_ms = encode_start.elapsed().as_millis(),
        "create_thumbnail: SVT-AV1 AVIF encode"
    );

    Ok(())
}

/// Encode a `DynamicImage` as AVIF using `ravif` (rav1e backend).
///
/// Kept as a fallback encoder for tests and edge cases where SVT-AV1
/// is not suitable. The primary image pipeline now uses `avif::encode_avif`
/// (SVT-AV1) for both thumbnails and full-res.
#[allow(dead_code)]
pub(crate) fn encode_avif_inprocess(img: &image::DynamicImage, dst: &Path) -> Result<()> {
    use ravif::{Encoder, Img};
    use rgb::RGB8;

    let rgb = img.to_rgb8();
    let (width, height) = (rgb.width() as usize, rgb.height() as usize);

    let pixels: Vec<RGB8> = rgb
        .as_raw()
        .chunks_exact(3)
        .map(|c| RGB8 { r: c[0], g: c[1], b: c[2] })
        .collect();

    let encoded = Encoder::new()
        .with_quality(65.0)
        .with_speed(6)
        .encode_rgb(Img::new(pixels.as_slice(), width, height))
        .map_err(|e| anyhow::anyhow!("ravif encode: {}", e))?;

    std::fs::write(dst, encoded.avif_file).context("write avif thumbnail")?;
    Ok(())
}

// ── Smart crop (content-aware, gradient saliency) ─────────────────────────────

/// Find the most visually interesting square crop of size `target_w × target_h`
/// using gradient saliency, similar to Go's `smartcrop` library.
///
/// Strategy:
/// 1. Downscale to ≤ 256px on the long side for fast analysis.
/// 2. Compute Sobel gradient magnitude on the luma channel.
/// 3. Slide the crop window and pick the region with maximum total energy.
/// 4. Map the crop back to original coordinates.
pub fn smart_crop(img: &image::DynamicImage, target_w: u32, target_h: u32) -> image::DynamicImage {
    let (src_w, src_h) = (img.width(), img.height());

    // If image is already smaller than target, return as-is.
    if src_w <= target_w && src_h <= target_h {
        return img.clone();
    }

    // Determine the crop size in source pixels (largest region fitting the AR).
    let (crop_w, crop_h) = if target_w == target_h {
        // Square thumbnail: crop the shorter axis.
        let side = src_w.min(src_h);
        (side, side)
    } else {
        let scale = (src_w as f32 / target_w as f32).min(src_h as f32 / target_h as f32);
        (
            (target_w as f32 * scale) as u32,
            (target_h as f32 * scale) as u32,
        )
    };

    // Downscale for fast analysis (max 256px on the long side).
    let analysis_scale = 256.0f32 / src_w.max(src_h) as f32;
    let analysis_scale = analysis_scale.min(1.0); // never upscale
    let an_w = ((src_w as f32 * analysis_scale).round() as u32).max(1);
    let an_h = ((src_h as f32 * analysis_scale).round() as u32).max(1);
    let an_crop_w = ((crop_w as f32 * analysis_scale).round() as u32).max(1).min(an_w);
    let an_crop_h = ((crop_h as f32 * analysis_scale).round() as u32).max(1).min(an_h);

    let small = img.resize_exact(an_w, an_h, FilterType::Triangle);
    let gray = small.to_luma8();

    // Slide crop window at step=2 px, find max-energy region.
    let step = 2u32;
    let mut best_x_an = (an_w.saturating_sub(an_crop_w)) / 2;
    let mut best_y_an = (an_h.saturating_sub(an_crop_h)) / 2;
    let mut best_energy = 0u64;

    let mut y_an = 0u32;
    loop {
        if y_an + an_crop_h > an_h { break; }
        let mut x_an = 0u32;
        loop {
            if x_an + an_crop_w > an_w { break; }
            let energy = region_gradient_energy(&gray, x_an, y_an, an_crop_w, an_crop_h, step);
            if energy > best_energy {
                best_energy = energy;
                best_x_an = x_an;
                best_y_an = y_an;
            }
            let next = x_an + step;
            if next + an_crop_w > an_w { break; }
            x_an = next;
        }
        let next = y_an + step;
        if next + an_crop_h > an_h { break; }
        y_an = next;
    }

    // Map best analysis-space crop back to source coordinates.
    let src_x = ((best_x_an as f32 / analysis_scale).round() as u32)
        .min(src_w.saturating_sub(crop_w));
    let src_y = ((best_y_an as f32 / analysis_scale).round() as u32)
        .min(src_h.saturating_sub(crop_h));

    img.crop_imm(src_x, src_y, crop_w, crop_h)
}

/// Sum of squared Sobel gradient magnitudes over a region, sampled at `step`
/// px intervals (for speed).
pub fn region_gradient_energy(
    gray: &image::GrayImage,
    rx: u32,
    ry: u32,
    rw: u32,
    rh: u32,
    step: u32,
) -> u64 {
    let (iw, ih) = gray.dimensions();
    let mut energy = 0u64;

    let mut y = ry;
    while y < ry + rh {
        let mut x = rx;
        while x < rx + rw {
            // Sobel kernel — clamp neighbours to image boundary.
            let get = |dx: i32, dy: i32| -> i32 {
                let nx = (x as i32 + dx).clamp(0, iw as i32 - 1) as u32;
                let ny = (y as i32 + dy).clamp(0, ih as i32 - 1) as u32;
                gray.get_pixel(nx, ny).0[0] as i32
            };
            let gx = get(1, -1) + 2 * get(1, 0) + get(1, 1)
                - get(-1, -1) - 2 * get(-1, 0) - get(-1, 1);
            let gy = get(-1, 1) + 2 * get(0, 1) + get(1, 1)
                - get(-1, -1) - 2 * get(0, -1) - get(1, -1);
            energy += (gx * gx + gy * gy) as u64;
            x += step;
        }
        y += step;
    }
    energy
}

// ── Video processing ──────────────────────────────────────────────────────────

/// Result of processing a video.
pub struct VideoResult {
    pub filename: String,
    pub thumbnail_filename: String,
    pub width: i32,
    pub height: i32,
}

/// Process a video: move to final directory, create thumbnail, probe dimensions.
pub async fn process_video(input: &Path, dirs: &Directories) -> Result<VideoResult> {
    let fn_start = std::time::Instant::now();
    let ext = input
        .extension()
        .map(|e| format!(".{}", e.to_string_lossy()))
        .unwrap_or_default();
    let dst_name = format!("{}{}", generate_name(), ext);
    let dst = dirs.video.join(&dst_name);

    // Move file to video directory.
    if fs::rename(input, &dst).await.is_err() {
        fs::copy(input, &dst).await.context("copy video file")?;
        let _ = fs::remove_file(input).await;
    }

    // Create thumbnail from first frame.
    let base = dst_name.strip_suffix(&ext).unwrap_or(&dst_name);
    let thumb_name = format!("{}.avif", base);
    let tmp_jpg = dirs.tmp.join(format!("{}.jpg", base));

    let frame_start = std::time::Instant::now();
    ffmpeg::extract_video_frame(&dst, &tmp_jpg).await?;
    debug!(
        path = %dst.display(),
        elapsed_ms = frame_start.elapsed().as_millis(),
        "processing: video frame extraction complete"
    );

    // Load the extracted frame and create thumbnail in-process.
    let decode_start = std::time::Instant::now();
    let tmp_clone = tmp_jpg.clone();
    let img = tokio::task::spawn_blocking(move || image::open(&tmp_clone))
        .await?
        .context("decode video frame")?;
    debug!(
        elapsed_ms = decode_start.elapsed().as_millis(),
        "processing: video frame decode complete"
    );

    let thumb_start = std::time::Instant::now();
    let thumb_dst = dirs.thumbnail.join(&thumb_name);
    create_thumbnail(&img, &thumb_dst, dirs).await?;
    let _ = fs::remove_file(&tmp_jpg).await;
    debug!(
        elapsed_ms = thumb_start.elapsed().as_millis(),
        "processing: video thumbnail complete"
    );

    // Probe dimensions.
    let probe_start = std::time::Instant::now();
    let (w, h) = ffmpeg::get_dimensions(&dst).await;
    debug!(
        width = w,
        height = h,
        elapsed_ms = probe_start.elapsed().as_millis(),
        "processing: video dimension probe complete"
    );

    debug!(
        path = %input.display(),
        width = w,
        height = h,
        total_elapsed_ms = fn_start.elapsed().as_millis(),
        "processing: process_video complete"
    );

    Ok(VideoResult {
        filename: dst_name,
        thumbnail_filename: thumb_name,
        width: w,
        height: h,
    })
}

// ── Utilities ─────────────────────────────────────────────────────────────────

/// Generate a unique filename using UUID v4 (collision-safe under concurrency).
/// Replaces the old nanosecond-timestamp approach which can collide when
/// multiple tasks run within the same nanosecond.
pub fn generate_name() -> String {
    uuid::Uuid::new_v4().to_string()
}

// ── Unit tests ────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use image::{DynamicImage, ImageBuffer, Luma, Rgb};
    use std::collections::HashSet;
    use tempfile::TempDir;

    // ── Helpers ───────────────────────────────────────────────────────────────

    fn solid_rgb(r: u8, g: u8, b: u8, w: u32, h: u32) -> DynamicImage {
        DynamicImage::ImageRgb8(ImageBuffer::from_pixel(w, h, Rgb([r, g, b])))
    }

    fn solid_gray(v: u8, w: u32, h: u32) -> image::GrayImage {
        ImageBuffer::from_pixel(w, h, Luma([v]))
    }

    /// Build a directory layout matching `Directories` inside a TempDir.
    fn make_dirs() -> (Directories, TempDir) {
        let tmp = TempDir::new().unwrap();
        let r = tmp.path();
        let dirs = Directories {
            image: r.join("images"),
            thumbnail: r.join("thumbnails"),
            video: r.join("videos"),
            tmp: r.join("tmp"),
            upload: r.join("upload"),
        };
        for d in [
            &dirs.image,
            &dirs.thumbnail,
            &dirs.video,
            &dirs.tmp,
            &dirs.upload,
            &dirs.tmp.join("thumbnails"),
        ] {
            std::fs::create_dir_all(d).unwrap();
        }
        (dirs, tmp)
    }

    // ── generate_name ─────────────────────────────────────────────────────────

    #[test]
    fn test_generate_name_is_nonempty() {
        let name = generate_name();
        assert!(!name.is_empty());
    }

    #[test]
    fn test_generate_name_unique() {
        let names: HashSet<String> = (0..500).map(|_| generate_name()).collect();
        assert_eq!(names.len(), 500, "all 500 names must be unique");
    }

    #[test]
    fn test_generate_name_is_uuid_v4() {
        let name = generate_name();
        // UUID v4: 8-4-4-4-12 hex chars separated by hyphens (36 chars total).
        assert_eq!(name.len(), 36, "UUID must be 36 characters, got {:?}", name);
        let parts: Vec<&str> = name.split('-').collect();
        assert_eq!(parts.len(), 5, "UUID must have 5 segments");
        assert_eq!(parts[0].len(), 8);
        assert_eq!(parts[1].len(), 4);
        assert_eq!(parts[2].len(), 4);
        assert_eq!(parts[3].len(), 4);
        assert_eq!(parts[4].len(), 12);
        // Version nibble: third segment starts with '4'.
        assert!(parts[2].starts_with('4'), "version nibble must be '4'");
    }

    // ── can_decode_natively ───────────────────────────────────────────────────

    #[test]
    fn test_can_decode_natively_jpeg() {
        assert!(can_decode_natively(std::path::Path::new("photo.jpg")));
        assert!(can_decode_natively(std::path::Path::new("photo.JPEG")));
        assert!(can_decode_natively(std::path::Path::new("photo.jpeg")));
    }

    #[test]
    fn test_can_decode_natively_other_supported() {
        for ext in &["png", "webp", "gif", "bmp", "tiff", "tif"] {
            let path = std::path::Path::new("file").with_extension(ext);
            assert!(
                can_decode_natively(&path),
                "expected native decode for .{}",
                ext
            );
        }
    }

    #[test]
    fn test_can_decode_natively_unsupported() {
        for ext in &["avif", "jxl", "mp4", "webm", "heic", "cr2"] {
            let path = std::path::Path::new("file").with_extension(ext);
            assert!(
                !can_decode_natively(&path),
                "expected no native decode for .{}",
                ext
            );
        }
    }

    #[test]
    fn test_can_decode_natively_no_extension() {
        assert!(!can_decode_natively(std::path::Path::new("noextension")));
    }

    // ── dct1d_partial ─────────────────────────────────────────────────────────

    #[test]
    fn test_dct1d_partial_dc_component_equals_sum() {
        // DCT-II k=0: output[0] = sum(input[x] * cos(0)) = sum(input)
        let input: Vec<f64> = (1..=8).map(|x| x as f64).collect(); // [1,2,3,4,5,6,7,8]
        let mut output = vec![0.0f64; 1];
        dct1d_partial(&input, &mut output, 1);
        let expected = input.iter().sum::<f64>();
        assert!(
            (output[0] - expected).abs() < 1e-9,
            "DC component {} should equal sum {}",
            output[0],
            expected
        );
    }

    #[test]
    fn test_dct1d_partial_zero_input_gives_zero_output() {
        let input = vec![0.0f64; 16];
        let mut output = vec![0.0f64; 4];
        dct1d_partial(&input, &mut output, 4);
        assert!(
            output.iter().all(|&v| v.abs() < 1e-12),
            "zero input must produce zero output"
        );
    }

    #[test]
    fn test_dct1d_partial_respects_k_max() {
        let input = vec![1.0f64; 256];
        let mut output = vec![99.0f64; 16]; // fill with sentinel
        dct1d_partial(&input, &mut output, 8);
        // Only first 8 values should be touched.
        for v in &output[..8] {
            assert!(*v != 99.0, "first k_max values must be written");
        }
        // Values beyond k_max should remain as the sentinel.
        for v in &output[8..] {
            assert_eq!(*v, 99.0, "values past k_max must not be touched");
        }
    }

    #[test]
    fn test_dct1d_partial_symmetry() {
        // For a constant input the only non-zero coefficient is DC (k=0).
        let input = vec![1.0f64; 256];
        let mut output = vec![0.0f64; 16];
        dct1d_partial(&input, &mut output, 16);
        // k=0 should be non-zero, k≥1 should be near 0 for constant input.
        for (k, &v) in output.iter().enumerate().skip(1) {
            assert!(
                v.abs() < 1e-6,
                "k={} coefficient {} should be near 0 for constant input",
                k,
                v
            );
        }
    }

    // ── median_of_256 ─────────────────────────────────────────────────────────

    #[test]
    fn test_median_of_256_sorted_range() {
        // Values 0.0, 1.0, …, 255.0 → median = (127.0 + 128.0) / 2 = 127.5
        let mut values = [0.0f64; 256];
        for (i, v) in values.iter_mut().enumerate() {
            *v = i as f64;
        }
        let m = median_of_256(&values);
        assert!((m - 127.5).abs() < 1e-10, "median should be 127.5, got {}", m);
    }

    #[test]
    fn test_median_of_256_all_same() {
        let values = [42.0f64; 256];
        let m = median_of_256(&values);
        assert!((m - 42.0).abs() < 1e-10, "median of constant array should be 42.0");
    }

    #[test]
    fn test_median_of_256_two_halves() {
        // First 128 values = 0.0, last 128 values = 1.0 → median = 0.5
        let mut values = [0.0f64; 256];
        for v in &mut values[128..] {
            *v = 1.0;
        }
        let m = median_of_256(&values);
        assert!((m - 0.5).abs() < 1e-10, "median should be 0.5, got {}", m);
    }

    #[test]
    fn test_median_of_256_reversed() {
        // Reversed order should give the same result as sorted.
        let mut ascending = [0.0f64; 256];
        let mut descending = [0.0f64; 256];
        for i in 0..256 {
            ascending[i] = i as f64;
            descending[255 - i] = i as f64;
        }
        let m_asc = median_of_256(&ascending);
        let m_desc = median_of_256(&descending);
        assert!((m_asc - m_desc).abs() < 1e-10, "order should not matter");
    }

    // ── compute_phash ─────────────────────────────────────────────────────────

    #[test]
    fn test_compute_phash_deterministic() {
        let img = solid_rgb(128, 64, 192, 256, 256);
        let hash_a = compute_phash(&img);
        let hash_b = compute_phash(&img);
        assert_eq!(hash_a, hash_b, "phash must be deterministic");
    }

    #[test]
    fn test_compute_phash_returns_four_components() {
        let img = solid_rgb(100, 150, 200, 64, 64);
        let hash = compute_phash(&img);
        assert_eq!(hash.len(), 4);
    }

    #[test]
    fn test_compute_phash_different_images_differ() {
        let bright = solid_rgb(230, 230, 230, 256, 256);
        let dark = solid_rgb(20, 20, 20, 256, 256);
        let h_bright = compute_phash(&bright);
        let h_dark = compute_phash(&dark);
        // Compute Hamming distance between the hashes.
        let hamming: u32 = h_bright
            .iter()
            .zip(h_dark.iter())
            .map(|(&a, &b)| (a ^ b).count_ones())
            .sum();
        assert!(
            hamming > 50,
            "bright vs dark image hamming distance {} should be > 50",
            hamming
        );
    }

    #[test]
    fn test_compute_phash_similar_images_low_hamming() {
        // Identical image must give hamming distance 0.
        let img_a: DynamicImage = {
            let mut buf = ImageBuffer::new(256u32, 256u32);
            for (x, y, p) in buf.enumerate_pixels_mut() {
                let v = ((x.wrapping_add(y)) % 256) as u8;
                *p = Rgb([v, v, v]);
            }
            DynamicImage::ImageRgb8(buf)
        };
        let ha = compute_phash(&img_a);
        let ha2 = compute_phash(&img_a);
        let hamming_same: u32 = ha
            .iter()
            .zip(ha2.iter())
            .map(|(&a, &b)| (a ^ b).count_ones())
            .sum();
        assert_eq!(hamming_same, 0, "same image must give hamming distance 0");

        // Slightly brighter version (+2 per channel): perceptual hash similarity
        // threshold is conventionally ≤ 10% of bits (≈ 25 for a 256-bit hash).
        // We allow < 64 to accommodate the DCT median shift for global brightness.
        let img_b: DynamicImage = {
            let mut buf = ImageBuffer::new(256u32, 256u32);
            for (x, y, p) in buf.enumerate_pixels_mut() {
                let v = ((x.wrapping_add(y)) % 256) as u8;
                *p = Rgb([v.saturating_add(2), v.saturating_add(2), v.saturating_add(2)]);
            }
            DynamicImage::ImageRgb8(buf)
        };
        let hb = compute_phash(&img_b);
        let hamming_similar: u32 = ha
            .iter()
            .zip(hb.iter())
            .map(|(&a, &b)| (a ^ b).count_ones())
            .sum();
        assert!(
            hamming_similar < 64,
            "near-identical images (±2 brightness) hamming distance {} should be < 64",
            hamming_similar
        );
    }

    // ── smart_crop ────────────────────────────────────────────────────────────

    #[test]
    fn test_smart_crop_landscape_to_square_size() {
        let img = solid_rgb(128, 128, 128, 400, 200);
        let cropped = smart_crop(&img, 150, 150);
        // Output must have square AR (shorter axis of source = 200)
        assert_eq!(cropped.width(), 200, "crop width should be 200 (short axis)");
        assert_eq!(cropped.height(), 200, "crop height should be 200");
    }

    #[test]
    fn test_smart_crop_portrait_to_square_size() {
        let img = solid_rgb(64, 64, 64, 200, 400);
        let cropped = smart_crop(&img, 150, 150);
        assert_eq!(cropped.width(), 200, "width should be 200 (short axis)");
        assert_eq!(cropped.height(), 200, "height should be 200");
    }

    #[test]
    fn test_smart_crop_small_image_returned_as_is() {
        // Image already smaller than target → returned unchanged.
        let img = solid_rgb(255, 0, 0, 50, 50);
        let cropped = smart_crop(&img, 150, 150);
        assert_eq!(cropped.width(), 50);
        assert_eq!(cropped.height(), 50);
    }

    #[test]
    fn test_smart_crop_equal_dims_unchanged() {
        // Image exactly matches target → returned as-is.
        let img = solid_rgb(0, 255, 0, 150, 150);
        let cropped = smart_crop(&img, 150, 150);
        assert_eq!(cropped.width(), 150);
        assert_eq!(cropped.height(), 150);
    }

    #[test]
    fn test_smart_crop_output_fits_in_source() {
        let img = solid_rgb(100, 100, 100, 1920, 1080);
        let cropped = smart_crop(&img, 150, 150);
        assert!(cropped.width() <= 1920);
        assert!(cropped.height() <= 1080);
    }

    // ── region_gradient_energy ────────────────────────────────────────────────

    #[test]
    fn test_region_gradient_energy_flat_region_is_zero() {
        // Uniform gray → no edges → Sobel = 0 → energy = 0.
        let gray = solid_gray(128, 64, 64);
        let energy = region_gradient_energy(&gray, 0, 0, 64, 64, 1);
        assert_eq!(energy, 0, "flat image must have zero gradient energy");
    }

    #[test]
    fn test_region_gradient_energy_edge_has_high_energy() {
        // Hard vertical edge: left half black, right half white.
        let mut gray: image::GrayImage = ImageBuffer::new(64, 64);
        for y in 0..64 {
            for x in 0..64 {
                gray.put_pixel(x, y, Luma([if x < 32 { 0u8 } else { 255 }]));
            }
        }
        let energy = region_gradient_energy(&gray, 0, 0, 64, 64, 1);
        assert!(energy > 0, "edge image must have positive gradient energy");
    }

    #[test]
    fn test_region_gradient_energy_sub_region() {
        // Two halves: left is flat (0 energy), right has an edge.
        let mut gray: image::GrayImage = ImageBuffer::new(64, 64);
        for y in 0..64 {
            for x in 32..64 {
                gray.put_pixel(x, y, Luma([255]));
            }
        }
        let flat_energy = region_gradient_energy(&gray, 0, 0, 30, 64, 1);
        let edge_energy = region_gradient_energy(&gray, 34, 0, 30, 64, 1);
        assert_eq!(flat_energy, 0, "flat sub-region energy must be 0");
        assert!(edge_energy == 0, "fully white sub-region also has no Sobel gradient");
    }

    // ── Directories ───────────────────────────────────────────────────────────

    #[test]
    fn test_directories_from_cwd_has_expected_suffixes() {
        let dirs = Directories::from_cwd();
        assert!(dirs.image.ends_with("images") || dirs.image.to_str().unwrap().contains("image"));
        assert!(dirs.thumbnail.to_str().unwrap().contains("thumbnail"));
        assert!(dirs.video.to_str().unwrap().contains("video"));
        assert!(dirs.upload.to_str().unwrap().contains("upload"));
    }

    #[tokio::test]
    async fn test_directories_ensure_creates_all() {
        let (dirs, _tmp) = make_dirs();
        // Re-run ensure on already-existing dirs → must not error.
        dirs.ensure().await.unwrap();
        assert!(dirs.image.exists());
        assert!(dirs.thumbnail.exists());
        assert!(dirs.video.exists());
        assert!(dirs.tmp.exists());
        assert!(dirs.upload.exists());
        assert!(dirs.tmp.join("thumbnails").exists());
    }

    // ── encode_avif_inprocess ─────────────────────────────────────────────────

    #[test]
    fn test_encode_avif_inprocess_creates_file() {
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("thumb.avif");
        let img = solid_rgb(80, 160, 240, 150, 150);
        encode_avif_inprocess(&img, &dst).unwrap();
        assert!(dst.exists(), "thumbnail AVIF must exist");
        assert!(std::fs::metadata(&dst).unwrap().len() > 0);
    }

    #[test]
    fn test_encode_avif_inprocess_small_image() {
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("tiny.avif");
        let img = solid_rgb(200, 100, 50, 8, 8);
        // ravif should handle even tiny images.
        let result = encode_avif_inprocess(&img, &dst);
        assert!(result.is_ok(), "inprocess encode failed: {:?}", result.err());
    }

    // ── create_thumbnail ──────────────────────────────────────────────────────

    #[tokio::test]
    async fn test_create_thumbnail_creates_avif_file() {
        let (dirs, _tmp) = make_dirs();
        let dst = dirs.thumbnail.join("thumb.avif");
        let img = solid_rgb(60, 120, 180, 300, 200);
        create_thumbnail(&img, &dst, &dirs).await.unwrap();
        assert!(dst.exists(), "thumbnail AVIF must exist");
        assert!(std::fs::metadata(&dst).unwrap().len() > 0);
    }

    #[tokio::test]
    async fn test_create_thumbnail_various_aspect_ratios() {
        let (dirs, _tmp) = make_dirs();
        for (w, h) in &[(100u32, 400u32), (400, 100), (150, 150), (1920, 1080)] {
            let dst = dirs.thumbnail.join(format!("thumb_{}x{}.avif", w, h));
            let img = solid_rgb(100, 100, 100, *w, *h);
            create_thumbnail(&img, &dst, &dirs)
                .await
                .expect(&format!("thumbnail should succeed for {}x{}", w, h));
            assert!(dst.exists());
        }
    }

    // ── process_image ─────────────────────────────────────────────────────────

    #[tokio::test]
    async fn test_process_image_returns_image_result() {
        let (dirs, tmp) = make_dirs();
        // Write a synthetic 256×256 JPEG to the upload dir.
        let src = dirs.upload.join("synthetic.jpg");
        let img = solid_rgb(100, 150, 200, 256, 256);
        img.save(&src).unwrap();

        let result = process_image(&src, &dirs).await.unwrap();

        assert!(!result.filename.is_empty());
        assert!(!result.thumbnail_filename.is_empty());
        assert_eq!(result.uploaded_filename, "synthetic.jpg");
        assert!(result.width > 0);
        assert!(result.height > 0);
        // Output AVIF file must exist.
        assert!(
            dirs.image.join(&result.filename).exists(),
            "AVIF output must exist"
        );
        // Thumbnail file must exist.
        assert!(
            dirs.thumbnail.join(&result.thumbnail_filename).exists(),
            "thumbnail must exist"
        );
        drop(tmp); // keep temp dir alive until here
    }

    #[tokio::test]
    async fn test_process_image_phash_is_nonzero() {
        let (dirs, _tmp) = make_dirs();
        let src = dirs.upload.join("gradient.jpg");
        // Use a gradient image so the hash is non-trivial.
        let mut buf = image::RgbImage::new(256, 256);
        for (x, y, p) in buf.enumerate_pixels_mut() {
            *p = Rgb([x as u8, y as u8, 128]);
        }
        DynamicImage::ImageRgb8(buf).save(&src).unwrap();

        let result = process_image(&src, &dirs).await.unwrap();
        // At least one of the four hash components must be non-zero.
        let any_nonzero = result.p_hash.iter().any(|&h| h != 0);
        assert!(any_nonzero, "phash of a non-trivial image should not be all zeros");
    }

    // ── process_video ─────────────────────────────────────────────────────────

    /// Create a tiny synthetic MP4 using ffmpeg lavfi; returns None when ffmpeg
    /// or libx264 is unavailable.
    fn make_test_mp4(dir: &std::path::Path) -> Option<std::path::PathBuf> {
        let out = dir.join("test_input.mp4");
        let o = std::process::Command::new("ffmpeg")
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
            .ok()?;
        if o.status.success() { Some(out) } else { None }
    }

    #[tokio::test]
    async fn test_process_video_returns_video_result() {
        if std::process::Command::new("ffmpeg").arg("-version").output().is_err() {
            return;
        }
        let (dirs, _tmp) = make_dirs();
        let video = match make_test_mp4(&dirs.upload) {
            Some(v) => v,
            None => return, // libx264 not available
        };

        let result = process_video(&video, &dirs).await.unwrap();

        assert!(!result.filename.is_empty(), "video filename must not be empty");
        assert!(!result.thumbnail_filename.is_empty(), "thumbnail must not be empty");
        assert!(
            dirs.video.join(&result.filename).exists(),
            "video file must exist in video dir"
        );
        assert!(
            dirs.thumbnail.join(&result.thumbnail_filename).exists(),
            "thumbnail AVIF must exist"
        );
    }

    #[tokio::test]
    async fn test_process_video_missing_input_returns_error() {
        let (dirs, _tmp) = make_dirs();
        let result = process_video(
            std::path::Path::new("/nonexistent/video.mp4"),
            &dirs,
        )
        .await;
        assert!(result.is_err(), "process_video must fail on nonexistent input");
    }

    #[tokio::test]
    async fn test_process_video_dimensions_are_nonzero() {
        if std::process::Command::new("ffmpeg").arg("-version").output().is_err() {
            return;
        }
        let (dirs, _tmp) = make_dirs();
        let video = match make_test_mp4(&dirs.upload) {
            Some(v) => v,
            None => return,
        };

        let result = process_video(&video, &dirs).await.unwrap();
        // ffprobe should detect the 64×64 dimension from the lavfi testsrc2.
        assert!(result.width > 0, "width must be > 0 when ffprobe is available");
        assert!(result.height > 0, "height must be > 0 when ffprobe is available");
    }

    // ── e2e: testset ──────────────────────────────────────────────────────────

    /// How many testset images to use per test run.
    ///
    /// Set env var `WALLIUM_TESTSET_SIZE=100` to exercise the full corpus.
    const TESTSET_SAMPLE_SIZE: usize = 10;

    /// Return paths to a sample of available testset images.
    ///
    /// Returns at most [`TESTSET_SAMPLE_SIZE`] entries (override with
    /// `WALLIUM_TESTSET_SIZE`).  Returns an empty vec when the dataset hasn't
    /// been downloaded yet — run `make test-data` once to populate.
    fn testset_images() -> Vec<std::path::PathBuf> {
        let manifest_path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("test_data")
            .join("manifest.json");

        if !manifest_path.exists() {
            eprintln!("SKIP testset: manifest not found — run `make test-data`");
            return vec![];
        }

        let json = match std::fs::read_to_string(&manifest_path) {
            Ok(s) => s,
            Err(e) => { eprintln!("SKIP testset: cannot read manifest: {e}"); return vec![]; }
        };

        let entries: Vec<serde_json::Value> = match serde_json::from_str(&json) {
            Ok(v) => v,
            Err(e) => { eprintln!("SKIP testset: invalid manifest JSON: {e}"); return vec![]; }
        };

        let limit = std::env::var("WALLIUM_TESTSET_SIZE")
            .ok()
            .and_then(|v| v.parse::<usize>().ok())
            .unwrap_or(TESTSET_SAMPLE_SIZE);

        let images_dir = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("test_data")
            .join("images");

        entries
            .iter()
            .filter_map(|entry| {
                let image = entry["image"].as_str()?;
                let filename = image.replace('/', "_");
                let path = images_dir.join(filename);
                if path.exists() { Some(path) } else { None }
            })
            .take(limit)
            .collect()
    }

    /// Run the full `process_image` pipeline against every testset image.
    ///
    /// Requires `make test-data` to have been run first.  Skips gracefully when
    /// no images are available — never fails the CI suite.
    #[tokio::test]
    async fn test_process_image_e2e_all_testset() {
        let images = testset_images();
        if images.is_empty() {
            return; // dataset not downloaded — skip without failure
        }
        eprintln!("e2e: running process_image against {} testset images", images.len());

        let mut failed_images: Vec<String> = Vec::new();

        for img_path in &images {
            let (dirs, _tmp) = make_dirs();
            let filename = img_path.file_name().unwrap();
            let src = dirs.upload.join(filename);
            if std::fs::copy(img_path, &src).is_err() {
                failed_images.push(format!("{:?}: copy failed", img_path));
                continue;
            }

            match process_image(&src, &dirs).await {
                Ok(result) => {
                    assert!(!result.filename.is_empty());
                    assert!(!result.thumbnail_filename.is_empty());
                    assert!(result.width > 0, "width > 0 for {:?}", img_path);
                    assert!(result.height > 0, "height > 0 for {:?}", img_path);
                    assert!(
                        dirs.image.join(&result.filename).exists(),
                        "AVIF must exist for {:?}", img_path
                    );
                    assert!(
                        dirs.thumbnail.join(&result.thumbnail_filename).exists(),
                        "thumbnail must exist for {:?}", img_path
                    );
                }
                Err(e) => {
                    failed_images.push(format!("{:?}: {e}", img_path));
                }
            }
        }

        assert!(
            failed_images.is_empty(),
            "{} images failed processing:\n  {}",
            failed_images.len(),
            failed_images.join("\n  ")
        );
    }

    /// Verify phash determinism across the entire testset.
    ///
    /// Requires `make test-data`. Skips gracefully when no images are available.
    #[test]
    fn test_phash_deterministic_across_testset() {
        let images = testset_images();
        if images.is_empty() {
            return;
        }
        for img_path in &images {
            // Only natively-decodable formats; avif/jxl require ffmpeg normalization.
            if !can_decode_natively(img_path) {
                continue;
            }
            let img = match image::open(img_path) {
                Ok(i) => i,
                Err(_) => continue,
            };
            let h1 = compute_phash(&img);
            let h2 = compute_phash(&img);
            assert_eq!(h1, h2, "phash must be deterministic for {:?}", img_path);
        }
    }

    // ── decode_jpeg_turbo ─────────────────────────────────────────────────────

    #[test]
    fn test_decode_jpeg_turbo_basic() {
        // Create a synthetic JPEG on disk and decode via turbojpeg.
        let tmp = TempDir::new().unwrap();
        let src = tmp.path().join("test.jpg");
        let img = solid_rgb(100, 150, 200, 256, 256);
        img.save(&src).unwrap();

        let decoded = decode_jpeg_turbo(&src, 0).unwrap();
        assert_eq!(decoded.width(), 256);
        assert_eq!(decoded.height(), 256);
    }

    #[test]
    fn test_decode_jpeg_turbo_dct_downscale() {
        // Image is 2000×2000 → with max_decode_width=920, it should
        // decode at 1/2 resolution (1000×1000) since 2000 > 920*2.
        let tmp = TempDir::new().unwrap();
        let src = tmp.path().join("large.jpg");
        let img = solid_rgb(80, 80, 80, 2000, 2000);
        img.save(&src).unwrap();

        let decoded = decode_jpeg_turbo(&src, 920).unwrap();
        // Half of 2000 = 1000
        assert_eq!(decoded.width(), 1000, "expected DCT 1/2 width");
        assert_eq!(decoded.height(), 1000, "expected DCT 1/2 height");
    }

    #[test]
    fn test_decode_jpeg_turbo_no_downscale_when_close() {
        // Image is 1200×800 → with max_decode_width=920, 1200 < 920*2=1840
        // so no DCT downscaling should occur.
        let tmp = TempDir::new().unwrap();
        let src = tmp.path().join("medium.jpg");
        let img = solid_rgb(60, 120, 180, 1200, 800);
        img.save(&src).unwrap();

        let decoded = decode_jpeg_turbo(&src, 920).unwrap();
        assert_eq!(decoded.width(), 1200, "no downscale expected");
        assert_eq!(decoded.height(), 800, "no downscale expected");
    }

    #[test]
    fn test_decode_jpeg_turbo_small_image() {
        let tmp = TempDir::new().unwrap();
        let src = tmp.path().join("small.jpg");
        let img = solid_rgb(255, 0, 0, 64, 64);
        img.save(&src).unwrap();

        let decoded = decode_jpeg_turbo(&src, 920).unwrap();
        assert_eq!(decoded.width(), 64);
        assert_eq!(decoded.height(), 64);
    }

    #[test]
    fn test_decode_jpeg_turbo_invalid_file_returns_error() {
        let tmp = TempDir::new().unwrap();
        let src = tmp.path().join("bad.jpg");
        std::fs::write(&src, b"not a jpeg").unwrap();

        let result = decode_jpeg_turbo(&src, 0);
        assert!(result.is_err(), "invalid JPEG should return error");
    }

    #[test]
    fn test_decode_jpeg_turbo_matches_image_crate() {
        // Verify that the turbojpeg decode produces the same dimensions
        // as the image crate decode.
        let tmp = TempDir::new().unwrap();
        let src = tmp.path().join("compare.jpg");
        let img = solid_rgb(128, 64, 192, 640, 480);
        img.save(&src).unwrap();

        let turbo = decode_jpeg_turbo(&src, 0).unwrap();
        let img_crate = image::open(&src).unwrap();
        assert_eq!(turbo.width(), img_crate.width());
        assert_eq!(turbo.height(), img_crate.height());
    }

    // ── decode_jpeg_to_yuv_planes ─────────────────────────────────────────────

    /// Create a synthetic JPEG in-memory using turbojpeg with the given
    /// subsampling, to exercise the YUV decode path with known format.
    fn make_jpeg_bytes(w: usize, h: usize, subsamp: turbojpeg::Subsamp) -> Vec<u8> {
        let pixels: Vec<u8> = (0..w * h * 3).map(|i| (i % 256) as u8).collect();
        let img = turbojpeg::Image {
            pixels: pixels.as_slice(),
            width:  w,
            pitch:  w * 3,
            height: h,
            format: turbojpeg::PixelFormat::RGB,
        };
        turbojpeg::compress(img, 85, subsamp)
            .expect("turbojpeg compress failed")
            .to_vec()
    }

    #[test]
    fn test_decode_jpeg_to_yuv_planes_basic() {
        // 4:2:0 JPEG → YUV planes should decode successfully with correct sizes.
        let data = make_jpeg_bytes(128, 64, turbojpeg::Subsamp::Sub2x2);
        let (buf, w, h, y_len, uv_len) = decode_jpeg_to_yuv_planes(&data)
            .expect("Sub2x2 JPEG should decode to YUV planes");
        assert_eq!(w, 128, "width from header");
        assert_eq!(h, 64,  "height from header");
        assert_eq!(y_len, 128 * 64,     "Y plane size for 128×64");
        assert_eq!(uv_len, 64 * 32,     "UV plane size for 128×64 Sub2x2");
        assert_eq!(buf.len(), y_len + 2 * uv_len, "total buffer = Y + U + V");
    }

    #[test]
    fn test_decode_jpeg_to_yuv_planes_rejects_non_420() {
        // 4:4:4 JPEG → must return an error (turbojpeg would overflow the buffer).
        let data = make_jpeg_bytes(64, 64, turbojpeg::Subsamp::None);
        let result = decode_jpeg_to_yuv_planes(&data);
        assert!(
            result.is_err(),
            "4:4:4 JPEG should be rejected by decode_jpeg_to_yuv_planes"
        );
        let msg = result.unwrap_err().to_string().to_lowercase();
        assert!(msg.contains("sub2x2") || msg.contains("subsamp"),
                "error should mention subsampling: {msg}");
    }

    #[test]
    fn test_decode_jpeg_to_yuv_planes_slices_non_overlapping() {
        // Verify that Y | U | V regions in the returned buffer don't overlap.
        let data = make_jpeg_bytes(256, 256, turbojpeg::Subsamp::Sub2x2);
        let (buf, _w, _h, y_len, uv_len) = decode_jpeg_to_yuv_planes(&data)
            .expect("decode should succeed");
        // Y: [0, y_len), U: [y_len, y_len+uv_len), V: [y_len+uv_len, end)
        assert!(y_len > 0);
        assert!(uv_len > 0);
        assert!(y_len + 2 * uv_len == buf.len(), "planes fill buffer exactly");
    }

    // ── decode_jpeg_turbo_from_data ──────────────────────────────────────────

    #[test]
    fn test_decode_jpeg_turbo_from_data_basic() {
        let data = make_jpeg_bytes(200, 150, turbojpeg::Subsamp::Sub2x2);
        let img = decode_jpeg_turbo_from_data(&data, 0)
            .expect("should decode from bytes");
        assert_eq!(img.width(), 200);
        assert_eq!(img.height(), 150);
    }

    #[test]
    fn test_decode_jpeg_turbo_from_data_dct_downscale() {
        // Image wider than max_decode_width * 2 → should be halved.
        let data = make_jpeg_bytes(2000, 1000, turbojpeg::Subsamp::Sub2x2);
        let img = decode_jpeg_turbo_from_data(&data, 920)
            .expect("should DCT-downscale");
        // libjpeg-turbo 1/2 scaling: 2000 → 1000
        assert_eq!(img.width(), 1000, "expected DCT 1/2 width");
        assert_eq!(img.height(), 500, "expected DCT 1/2 height");
    }

    #[test]
    fn test_decode_jpeg_turbo_from_data_matches_from_file() {
        // Bytes path and file path should produce the same dimensions.
        let tmp = TempDir::new().unwrap();
        let src = tmp.path().join("test.jpg");
        let img = solid_rgb(128, 200, 50, 320, 240);
        img.save(&src).unwrap();
        let data = std::fs::read(&src).unwrap();

        let from_file = decode_jpeg_turbo(&src, 0).unwrap();
        let from_data = decode_jpeg_turbo_from_data(&data, 0).unwrap();
        assert_eq!(from_file.width(), from_data.width());
        assert_eq!(from_file.height(), from_data.height());
    }

    // ── Thread budget helpers ─────────────────────────────────────────────────

    #[test]
    fn test_thumbnail_encode_threads_is_one() {
        assert_eq!(thumbnail_encode_threads(), 1);
    }

    #[test]
    fn test_fullres_encode_threads_at_least_one() {
        assert!(fullres_encode_threads() >= 1);
    }

    // ── Resize skip in encode_avif ────────────────────────────────────────────

    #[test]
    fn test_encode_avif_resize_skip_near_target() {
        // Source is 1000×1000 with max_width=920 → ratio=1.087 < 1.15
        // → should skip resize and output ~1000px instead of 920px.
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("skip.avif");
        let img = solid_rgb(128, 128, 128, 1000, 1000);
        let (w, h) = avif::encode_avif(&img, &dst, 18, 8, 920, 0).unwrap();
        // Width should remain near 1000 (even-clipped: 1000).
        assert!(w >= 998, "width {} should be near 1000 (skip resize)", w);
        assert!(h >= 998, "height {} should be near 1000 (skip resize)", h);
    }

    #[test]
    fn test_encode_avif_resize_applied_far_from_target() {
        // Source is 2000×2000 with max_width=920 → ratio=2.17 > 1.15
        // → should resize down to ≤920px.
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("resize.avif");
        let img = solid_rgb(80, 80, 80, 2000, 2000);
        let (w, _h) = avif::encode_avif(&img, &dst, 18, 8, 920, 0).unwrap();
        assert!(w <= 920, "width {} should be ≤ 920 after resize", w);
    }
}
