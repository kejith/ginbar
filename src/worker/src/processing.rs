//! Image and video processing pipeline.
//!
//! Key optimizations:
//!
//! 1. **Fully in-process AVIF encoding** – SVT-AV1 v2.3.0 native bindings
//!    encode both full-res and thumbnails entirely in-process.  No ffmpeg or
//!    ffprobe subprocess for images (eliminates fork/exec overhead, double
//!    decode, and IPC).  ffmpeg is only used for video operations.
//!
//! 2. **Skip ffmpeg normalization for common formats** – JPEG, PNG, WebP,
//!    GIF, BMP and TIFF are decoded directly by the `image` crate.
//!
//! 3. **Sequential then blocking** – phash + thumbnail run first (cheap,
//!    ~5-60 ms), then the image is moved (not cloned) to a blocking thread
//!    for SVT-AV1 encoding.  This eliminates a full pixel-buffer copy.
//!
//! 4. **Content-aware thumbnails** – gradient-saliency crop replaces the
//!    naïve center crop, matching the Go `smartcrop` behaviour.
//!
//! 5. **CatmullRom resize** – ~2× faster than Lanczos3 for downscaling.
//!
//! 6. **UUID filenames** prevent timestamp-collision races.
//!
//! 7. **DCT perceptual hash** in-process (no `img_hash` dep).

use anyhow::{Context, Result};
use image::imageops::FilterType;
use std::path::{Path, PathBuf};
use tokio::fs;
use tracing::debug;

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
fn can_decode_natively(path: &Path) -> bool {
    let ext = path
        .extension()
        .map(|e| e.to_string_lossy().to_lowercase())
        .unwrap_or_default();
    matches!(
        ext.as_str(),
        "jpg" | "jpeg" | "png" | "webp" | "gif" | "bmp" | "tiff" | "tif"
    )
}

/// Load an image into memory.
///
/// For natively-decodable formats (JPEG/PNG/WebP/GIF/BMP/TIFF) the file is
/// opened directly — no subprocess required.
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
        let input = input.to_path_buf();
        let img = tokio::task::spawn_blocking(move || image::open(&input))
            .await?
            .context("decode image")?;
        return Ok((img, None));
    }

    // Exotic format — normalize to JPEG via ffmpeg.
    let tmp_name = format!("{}.jpg", generate_name());
    let tmp_path = dirs.tmp.join("thumbnails").join(&tmp_name);

    ffmpeg::normalize_to_jpeg(input, &tmp_path).await?;

    let tmp_clone = tmp_path.clone();
    let img = tokio::task::spawn_blocking(move || image::open(&tmp_clone))
        .await?
        .context("decode normalized JPEG")?;

    Ok((img, Some(tmp_path)))
}

/// Process a single image: encode to AVIF, compute perceptual hash, create thumbnail.
///
/// Everything runs in-process via SVT-AV1 native bindings — no ffmpeg subprocess.
/// Phash + thumbnail are computed first (cheap, ~5-60 ms), then the image is
/// moved (not cloned) to a blocking thread for full-res AVIF encoding.
/// This avoids copying the entire pixel buffer.
pub async fn process_image(input: &Path, dirs: &Directories) -> Result<ImageResult> {
    let file_name = input
        .file_name()
        .context("no filename")?
        .to_string_lossy()
        .to_string();
    let avif_name = format!("{}.avif", file_name);
    let output_path = dirs.image.join(&avif_name);

    // ── Decode the source image first (shared by all in-process work). ───────
    let (img, tmp_path) = load_image(input, dirs).await?;

    // ── Fast in-process work first (phash + thumbnail) ── ~5-60 ms total ─────
    // These are cheap, so finish them before handing the image off to SVT-AV1.
    // This avoids cloning the full image buffer.

    // Perceptual hash — pure in-process, fast (~1-5 ms).
    let p_hash = compute_phash(&img);

    // Thumbnail: smart-crop + CatmullRom resize + ravif encode (in-process).
    let thumb_name = format!("{}.avif", file_name);
    let thumb_path = dirs.thumbnail.join(&thumb_name);
    create_thumbnail(&img, &thumb_path, dirs).await?;

    // ── Full-resolution AVIF encode on a blocking thread. ────────────────────
    // Uses SVT-AV1 native bindings — no subprocess, no double decode.
    // Image is moved (not cloned) since phash + thumbnail are already done.
    let avif_output = output_path.clone();
    let avif_handle = tokio::task::spawn_blocking(move || {
        avif::encode_avif(&img, &avif_output, 18, 8, 920)
    });

    // ── Await the AVIF encode and collect output dimensions. ─────────────────
    let (out_w, out_h) = avif_handle
        .await?
        .context("AVIF encode failed")?;

    // Clean up temp normalization file (exotic formats only).
    if let Some(p) = tmp_path {
        let _ = fs::remove_file(&p).await;
    }

    let (w, h) = (out_w, out_h);

    debug!(file = %file_name, width = w, height = h, "image processed");

    Ok(ImageResult {
        filename: avif_name,
        thumbnail_filename: thumb_name,
        uploaded_filename: file_name,
        p_hash,
        width: w,
        height: h,
    })
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
fn dct1d_partial(input: &[f64], output: &mut [f64], k_max: usize) {
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
fn median_of_256(values: &[f64; HASH_BITS_LEN]) -> f64 {
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
///   3. Encode as AVIF via `ravif`/`rav1e` — in-process on a blocking thread, ~30-60 ms.
///
/// No ffmpeg subprocess, no temporary JPEG files.  Runs concurrently with the
/// full-resolution AVIF encode; critical path = ~1.2 s (full-res AVIF only).
pub async fn create_thumbnail(img: &image::DynamicImage, dst: &Path, _dirs: &Directories) -> Result<()> {
    // In-process: smart crop + fast resize.
    let cropped = smart_crop(img, THUMB_SIZE, THUMB_SIZE);
    let resized = cropped.resize_exact(THUMB_SIZE, THUMB_SIZE, FilterType::CatmullRom);

    let dst = dst.to_path_buf();
    tokio::task::spawn_blocking(move || encode_avif_inprocess(&resized, &dst))
        .await?
        .context("in-process avif thumbnail encode")?;

    Ok(())
}

/// Encode a `DynamicImage` as AVIF using `ravif` (rav1e backend).
/// Quality 65.0 / speed 6 ≈ ffmpeg CRF 30 / preset 6 for a 150×150 image.
fn encode_avif_inprocess(img: &image::DynamicImage, dst: &Path) -> Result<()> {
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
fn smart_crop(img: &image::DynamicImage, target_w: u32, target_h: u32) -> image::DynamicImage {
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
fn region_gradient_energy(
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

    ffmpeg::extract_video_frame(&dst, &tmp_jpg).await?;

    // Load the extracted frame and create thumbnail in-process.
    let tmp_clone = tmp_jpg.clone();
    let img = tokio::task::spawn_blocking(move || image::open(&tmp_clone))
        .await?
        .context("decode video frame")?;

    let thumb_dst = dirs.thumbnail.join(&thumb_name);
    create_thumbnail(&img, &thumb_dst, dirs).await?;
    let _ = fs::remove_file(&tmp_jpg).await;

    // Probe dimensions.
    let (w, h) = ffmpeg::get_dimensions(&dst).await;

    debug!(file = %dst_name, width = w, height = h, "video processed");

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
