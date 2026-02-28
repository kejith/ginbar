//! Image and video processing pipeline.
//!
//! Mirrors the Go `utils.ProcessImage` / `utils.ProcessVideo` logic:
//! - Images: AVIF encode + normalize to JPEG + perceptual hash + thumbnail
//! - Videos: move to final dir + extract thumbnail frame + probe dimensions

use anyhow::{Context, Result};
use image::imageops::FilterType;
use image::GenericImageView;
use img_hash::{HashAlg, HasherConfig};
use std::path::{Path, PathBuf};
use tokio::fs;
use tracing::debug;

use crate::ffmpeg;

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
    /// Four 64-bit perceptual-hash components (16×16 DCT blocks).
    pub p_hash: [i64; 4],
    pub width: i32,
    pub height: i32,
}

/// Process a single image: encode to AVIF, compute perceptual hash, create thumbnail.
///
/// This is the Rust equivalent of Go's `utils.ProcessImage`.
pub async fn process_image(input: &Path, dirs: &Directories) -> Result<ImageResult> {
    let file_name = input
        .file_name()
        .context("no filename")?
        .to_string_lossy()
        .to_string();
    let avif_name = format!("{}.avif", file_name);
    let output_path = dirs.image.join(&avif_name);

    // Step 1: Convert to AVIF (CRF 18, preset 8, max 920px wide).
    let avif_output = output_path.clone();
    let avif_input = input.to_path_buf();
    let avif_handle = tokio::spawn(async move {
        ffmpeg::convert_to_avif(&avif_input, &avif_output, 18, 8, 920).await
    });

    // Step 2: Normalize to JPEG for perceptual hashing (parallel with AVIF encode).
    let jpeg_name = format!("{}.jpg", generate_name());
    let jpeg_path = dirs.tmp.join("thumbnails").join(&jpeg_name);
    let jpeg_input = input.to_path_buf();
    let jpeg_out = jpeg_path.clone();
    let jpeg_handle =
        tokio::spawn(async move { ffmpeg::normalize_to_jpeg(&jpeg_input, &jpeg_out).await });

    // Await both.
    avif_handle
        .await?
        .context("AVIF encode failed")?;
    jpeg_handle
        .await?
        .context("JPEG normalize failed")?;

    // Step 3: Load the JPEG and compute perceptual hash.
    let jpeg_bytes = fs::read(&jpeg_path).await.context("read normalized JPEG")?;
    let img = image::load_from_memory(&jpeg_bytes).context("decode JPEG")?;

    let p_hash = compute_phash(&img);

    // Step 4: Create thumbnail (150×150 center crop → AVIF).
    let thumb_name = format!("{}.avif", file_name);
    let thumb_path = dirs.thumbnail.join(&thumb_name);
    create_thumbnail(&img, &thumb_path, dirs).await?;

    // Clean up temp JPEG.
    let _ = fs::remove_file(&jpeg_path).await;

    // Step 5: Probe output dimensions (may differ from source if scaled).
    let (out_w, out_h) = ffmpeg::get_dimensions(&output_path).await;
    let (w, h) = if out_w == 0 || out_h == 0 {
        (img.width() as i32, img.height() as i32)
    } else {
        (out_w, out_h)
    };

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

/// Compute a 16×16 DCT perceptual hash, returning four i64 components
/// (same layout as the Go `goimagehash.ExtPerceptionHash(img, 16, 16)`).
pub fn compute_phash(img: &image::DynamicImage) -> [i64; 4] {
    let hasher = HasherConfig::new()
        .hash_alg(HashAlg::DoubleGradient)
        .hash_size(16, 16)
        .to_hasher();

    let hash = hasher.hash_image(img);
    let bytes = hash.as_bytes();

    // The hash is 16×16 = 256 bits = 32 bytes = four i64s.
    let mut result = [0i64; 4];
    for (i, chunk) in bytes.chunks(8).take(4).enumerate() {
        let mut buf = [0u8; 8];
        let len = chunk.len().min(8);
        buf[..len].copy_from_slice(&chunk[..len]);
        result[i] = i64::from_be_bytes(buf);
    }
    result
}

/// Create a 150×150 center-crop thumbnail and encode as AVIF.
pub async fn create_thumbnail(
    img: &image::DynamicImage,
    dst: &Path,
    dirs: &Directories,
) -> Result<()> {
    let (w, h) = (img.width(), img.height());
    let side = w.min(h);
    let x = (w - side) / 2;
    let y = (h - side) / 2;

    let cropped = img.crop_imm(x, y, side, side);
    let resized = cropped.resize_exact(150, 150, FilterType::Lanczos3);

    // Save as temporary JPEG, then convert to AVIF.
    let tmp_name = format!("{}.jpg", generate_name());
    let tmp_path = dirs.tmp.join("thumbnails").join(&tmp_name);

    // Save JPEG synchronously on a blocking thread.
    let tmp_clone = tmp_path.clone();
    let save_result = tokio::task::spawn_blocking(move || {
        resized.save(&tmp_clone)
    })
    .await?;
    save_result.context("save thumbnail JPEG")?;

    // CRF 30, preset 6, no scaling (already 150px).
    ffmpeg::convert_to_avif(&tmp_path, dst, 30, 6, 0).await?;

    let _ = fs::remove_file(&tmp_path).await;
    Ok(())
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
///
/// This is the Rust equivalent of Go's `utils.ProcessVideo`.
pub async fn process_video(input: &Path, dirs: &Directories) -> Result<VideoResult> {
    let ext = input
        .extension()
        .map(|e| format!(".{}", e.to_string_lossy()))
        .unwrap_or_default();
    let dst_name = format!("{}{}", generate_name(), ext);
    let dst = dirs.video.join(&dst_name);

    // Move file to video directory.
    if fs::rename(input, &dst).await.is_err() {
        // rename fails across filesystems; fall back to copy+remove.
        fs::copy(input, &dst).await.context("copy video file")?;
        let _ = fs::remove_file(input).await;
    }

    // Create thumbnail.
    let base = dst_name.strip_suffix(&ext).unwrap_or(&dst_name);
    let thumb_name = format!("{}.avif", base);
    let tmp_jpg = dirs.tmp.join(format!("{}.jpg", base));

    ffmpeg::extract_video_frame(&dst, &tmp_jpg).await?;

    let thumb_dst = dirs.thumbnail.join(&thumb_name);
    create_thumbnail_from_file(&tmp_jpg, &thumb_dst, dirs).await?;
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

/// Load an image from disk and create a thumbnail.
async fn create_thumbnail_from_file(
    input: &Path,
    dst: &Path,
    dirs: &Directories,
) -> Result<()> {
    let bytes = fs::read(input).await.context("read image for thumbnail")?;
    let img = image::load_from_memory(&bytes).context("decode image for thumbnail")?;
    create_thumbnail(&img, dst, dirs).await
}

/// Generate a unique filename using nanosecond timestamp.
pub fn generate_name() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos();
    format!("{}", nanos)
}
