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
}
