//! In-process AVIF encoder using SVT-AV1 native bindings.
//!
//! Replaces the ffmpeg subprocess pipeline for image→AVIF conversion:
//! - **No subprocess overhead** (no fork/exec, no ffprobe, no ffmpeg)
//! - **No double decode** (reuses the already-decoded `DynamicImage`)
//! - **SVT-AV1 v2.3.0** (newer = ~15-25% faster than v1.4.1 in ffmpeg)
//! - **Fine-grained thread control** via `lp` (level of parallelism)
//!
//! The raw AV1 bitstream from SVT-AV1 is wrapped in a valid AVIF container
//! using the `avif-serialize` crate (same crate that `ravif` uses internally).

use anyhow::{Context, Result};
use image::DynamicImage;
use std::path::Path;
use svt_av1_enc::{Frame, SvtAv1EncoderConfig};
use tracing::{debug, warn};

/// Maximum number of logical processors SVT-AV1 may use **per encode**.
///
/// When `WORKER_CONCURRENCY` is > 1, multiple images are encoded simultaneously.
/// The thread budget is split: `max(1, total_cpus / (2 * concurrency))`.
/// This avoids severe oversubscription that makes batch processing much slower
/// than the single-image benchmark.
///
/// Override with `SVT_AV1_THREADS` to set an explicit value.
fn encoder_thread_count() -> u32 {
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
    // Reserve ~half the CPUs for tokio runtime + thumbnail encoding (ravif).
    (cpus / (2 * concurrency)).max(1)
}

/// Encode a `DynamicImage` to AVIF using SVT-AV1 in-process.
///
/// - `crf`: quality (0 = lossless, 63 = worst; 18 ≈ visually lossless)
/// - `preset`: SVT-AV1 speed (0 = slowest, 13 = fastest; 8 = good balance)
/// - `max_width`: scale down so width ≤ this value (0 = no scaling)
///
/// Returns `(width, height)` of the encoded output.
///
/// Falls back to `ravif` automatically when:
/// - SVT-AV1 fails for any reason (encode error, library misbehavior, etc.)
/// - Image dimensions are below the SVT-AV1 minimum (64×64)
pub fn encode_avif(
    img: &DynamicImage,
    dst: &Path,
    crf: u32,
    preset: u32,
    max_width: u32,
) -> Result<(i32, i32)> {
    // ── Step 1: Resize if needed ─────────────────────────────────────────────
    let (src_w, src_h) = (img.width(), img.height());
    let img = if max_width > 0 && src_w > max_width {
        let scale = max_width as f64 / src_w as f64;
        let new_w = max_width;
        let new_h = ((src_h as f64 * scale).round() as u32).max(1);
        // Ensure even dimensions for YUV420
        let new_w = new_w & !1;
        let new_h = new_h & !1;
        std::borrow::Cow::Owned(img.resize_exact(new_w, new_h, image::imageops::FilterType::CatmullRom))
    } else {
        // Ensure even dimensions
        let ew = src_w & !1;
        let eh = src_h & !1;
        if ew != src_w || eh != src_h {
            std::borrow::Cow::Owned(img.crop_imm(0, 0, ew, eh))
        } else {
            std::borrow::Cow::Borrowed(img)
        }
    };

    let width = img.width();
    let height = img.height();

    // ── Fallback: dimensions below SVT-AV1 64×64 minimum ────────────────────
    if width < 64 || height < 64 {
        debug!(width, height, "image below 64×64 minimum, using ravif directly");
        return encode_avif_ravif(&img, dst);
    }

    // ── Step 2: Convert RGB → YUV420 (BT.601 limited range) ─────────────────
    let rgb = img.to_rgb8();
    let (y_plane, u_plane, v_plane) = rgb_to_yuv420(&rgb, width, height);

    // ── Step 3: Encode with SVT-AV1 ─────────────────────────────────────────
    match encode_av1_raw(
        &y_plane,
        &u_plane,
        &v_plane,
        width,
        height,
        crf,
        preset,
    ) {
        Ok(av1_data) => {
            // ── Step 4: Wrap in AVIF container ───────────────────────────────
            let avif_data = wrap_avif_container(&av1_data, width, height)?;

            // ── Step 5: Write to disk ─────────────────────────────────────────
            std::fs::write(dst, &avif_data).context("write AVIF file")?;

            debug!(
                dst = %dst.display(),
                width,
                height,
                av1_bytes = av1_data.len(),
                avif_bytes = avif_data.len(),
                "AVIF encoded in-process via SVT-AV1"
            );

            Ok((width as i32, height as i32))
        }
        Err(svt_err) => {
            // SVT-AV1 failed — log and fall back to ravif so the image is not lost.
            let err_chain = svt_err
                .chain()
                .map(|c| c.to_string())
                .collect::<Vec<_>>()
                .join(": ");
            warn!(
                width,
                height,
                err = err_chain,
                "SVT-AV1 encode failed, falling back to ravif"
            );
            // Remove any partially-written file before retrying.
            let _ = std::fs::remove_file(dst);
            encode_avif_ravif(&img, dst)
                .with_context(|| format!("ravif fallback (SVT-AV1 error: {})", err_chain))
        }
    }
}

/// Convert an RGB image to YUV420 planar format (BT.601 limited range).
///
/// BT.601 coefficients (same as what ffmpeg uses for sRGB source):
///   Y  =  16 + (65.481 * R + 128.553 * G +  24.966 * B) / 255
///   Cb = 128 + (-37.797 * R -  74.203 * G + 112.0   * B) / 255
///   Cr = 128 + (112.0   * R -  93.786 * G -  18.214 * B) / 255
///
/// Using fixed-point arithmetic (<<16) for speed.
fn rgb_to_yuv420(rgb: &image::RgbImage, width: u32, height: u32) -> (Vec<u8>, Vec<u8>, Vec<u8>) {
    let w = width as usize;
    let h = height as usize;
    let cw = w / 2;
    let ch = h / 2;

    let mut y_plane = vec![0u8; w * h];
    let mut u_plane = vec![0u8; cw * ch];
    let mut v_plane = vec![0u8; cw * ch];

    let raw = rgb.as_raw();

    // Compute Y for every pixel
    for row in 0..h {
        for col in 0..w {
            let idx = (row * w + col) * 3;
            let r = raw[idx] as i32;
            let g = raw[idx + 1] as i32;
            let b = raw[idx + 2] as i32;

            // Y = 16 + (66*R + 129*G + 25*B + 128) >> 8
            let y = 16 + ((66 * r + 129 * g + 25 * b + 128) >> 8);
            y_plane[row * w + col] = y.clamp(16, 235) as u8;
        }
    }

    // Compute U (Cb) and V (Cr) at half resolution — average 2×2 blocks
    for cy in 0..ch {
        for cx in 0..cw {
            let row = cy * 2;
            let col = cx * 2;

            // Average the 2×2 block
            let mut sum_r = 0i32;
            let mut sum_g = 0i32;
            let mut sum_b = 0i32;
            for dy in 0..2 {
                for dx in 0..2 {
                    let idx = ((row + dy) * w + (col + dx)) * 3;
                    sum_r += raw[idx] as i32;
                    sum_g += raw[idx + 1] as i32;
                    sum_b += raw[idx + 2] as i32;
                }
            }
            let r = sum_r / 4;
            let g = sum_g / 4;
            let b = sum_b / 4;

            // Cb = 128 + (-38*R - 74*G + 112*B + 128) >> 8
            let u = 128 + ((-38 * r - 74 * g + 112 * b + 128) >> 8);
            // Cr = 128 + (112*R - 94*G - 18*B + 128) >> 8
            let v = 128 + ((112 * r - 94 * g - 18 * b + 128) >> 8);

            u_plane[cy * cw + cx] = u.clamp(16, 240) as u8;
            v_plane[cy * cw + cx] = v.clamp(16, 240) as u8;
        }
    }

    (y_plane, u_plane, v_plane)
}

/// Encode raw YUV420 data to AV1 bitstream using SVT-AV1.
fn encode_av1_raw(
    y_plane: &[u8],
    u_plane: &[u8],
    v_plane: &[u8],
    width: u32,
    height: u32,
    crf: u32,
    preset: u32,
) -> Result<Vec<u8>> {
    let mut cfg = SvtAv1EncoderConfig::new(width, height, Some(preset as i8));

    // CRF mode (rate_control_mode=0 means CQP/CRF)
    cfg.config.rate_control_mode = 0;
    cfg.config.qp = crf;

    // LOW_DELAY_P prediction structure: the encoder outputs each frame
    // immediately without buffering a GOP.  Essential for single-frame
    // still-image encoding — the default RANDOM_ACCESS (2) buffers frames
    // and may produce no output for a one-frame stream even after send_eos.
    // SVT-AV1 v2.3.0 only accepts 1 (LOW_DELAY_P) or 2 (RANDOM_ACCESS);
    // the old value 0 was removed.
    cfg.config.pred_structure = 1;

    // Let the encoder auto-set the intra period based on pred_structure.
    // -1 means auto; combined with LOW_DELAY_P the single frame is always
    // a keyframe.
    cfg.config.intra_period_length = -1;

    // Thread control
    cfg.config.logical_processors = encoder_thread_count();

    let encoder = cfg.into_encoder().map_err(|e| anyhow::anyhow!("SVT-AV1 init: {:?}", e))?;

    let frame = Frame::new(
        y_plane,
        u_plane,
        v_plane,
        width,          // y_stride
        width / 2,      // cb_stride
        width / 2,      // cr_stride
        (width * height * 3 / 2) as u32, // total frame size
    );

    encoder
        .send_picture(frame, Some(0), true)
        .map_err(|e| anyhow::anyhow!("SVT-AV1 send_picture: {:?}", e))?;

    encoder
        .send_eos()
        .map_err(|e| anyhow::anyhow!("SVT-AV1 send_eos: {:?}", e))?;

    let packet = encoder
        .get_packet(1)
        .map_err(|e| anyhow::anyhow!("SVT-AV1 get_packet: {:?}", e))?;

    Ok(packet.to_vec())
}

/// Encode a (possibly already-resized) `DynamicImage` to AVIF using `ravif`.
///
/// Used as a fallback when SVT-AV1 is unavailable or fails, and as the primary
/// encoder for images smaller than SVT-AV1's 64×64 minimum.
///
/// Quality 80.0 / speed 4 balances file size with encode time for full-res images.
fn encode_avif_ravif(img: &DynamicImage, dst: &Path) -> Result<(i32, i32)> {
    use ravif::{Encoder, Img};
    use rgb::RGB8;

    let rgb = img.to_rgb8();
    let (width, height) = (rgb.width(), rgb.height());
    let w = width as usize;
    let h = height as usize;

    let pixels: Vec<RGB8> = rgb
        .as_raw()
        .chunks_exact(3)
        .map(|c| RGB8 { r: c[0], g: c[1], b: c[2] })
        .collect();

    let encoded = Encoder::new()
        .with_quality(80.0)
        .with_speed(4)
        .encode_rgb(Img::new(pixels.as_slice(), w, h))
        .map_err(|e| anyhow::anyhow!("ravif encode: {}", e))?;

    std::fs::write(dst, encoded.avif_file).context("write AVIF file (ravif)")?;

    debug!(
        dst = %dst.display(),
        width,
        height,
        "AVIF encoded via ravif fallback"
    );

    Ok((width as i32, height as i32))
}

/// Wrap a raw AV1 bitstream in an AVIF container (ISOBMFF/HEIF).
fn wrap_avif_container(av1_data: &[u8], width: u32, height: u32) -> Result<Vec<u8>> {
    let avif = avif_serialize::Aviffy::new()
        .to_vec(av1_data, None, width, height, 8);

    Ok(avif)
}
