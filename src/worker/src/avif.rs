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

/// Redirect stdout (fd 1) and stderr (fd 2) to `/dev/null` for the duration of `f`,
/// then restore them.
///
/// SVT-AV1 prints verbose init messages to stdout/stderr on every encoder creation.
/// A global mutex serialises the fd swap so concurrent encodes don't interleave.
fn suppress_stdio_for<T, F: FnOnce() -> T>(f: F) -> T {
    use std::sync::Mutex;
    static FD_LOCK: Mutex<()> = Mutex::new(());
    let _guard = FD_LOCK.lock().unwrap_or_else(|p| p.into_inner());

    // RAII guard that restores the saved fds on drop (panic-safe).
    struct Restore(libc::c_int, libc::c_int);
    impl Drop for Restore {
        fn drop(&mut self) {
            unsafe {
                libc::dup2(self.0, 1);
                libc::dup2(self.1, 2);
                libc::close(self.0);
                libc::close(self.1);
            }
        }
    }

    unsafe {
        let devnull = libc::open(c"/dev/null".as_ptr(), libc::O_WRONLY);
        let saved_out = libc::dup(1);
        let saved_err = libc::dup(2);
        libc::dup2(devnull, 1);
        libc::dup2(devnull, 2);
        libc::close(devnull);
        let _restore = Restore(saved_out, saved_err);
        f()
    }
}

/// Maximum number of logical processors SVT-AV1 may use **per encode**.
///
/// When `WORKER_CONCURRENCY` is > 1, multiple images are encoded simultaneously.
/// The thread budget is split: `max(1, total_cpus / (2 * concurrency))`.
/// This avoids severe oversubscription that makes batch processing much slower
/// than the single-image benchmark.
///
/// Override with `SVT_AV1_THREADS` to set an explicit value.
pub(crate) fn encoder_thread_count() -> u32 {
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
/// - `threads`: SVT-AV1 thread count (0 = auto via `encoder_thread_count()`)
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
    threads: u32,
) -> Result<(i32, i32)> {
    let fn_start = std::time::Instant::now();
    let threads = if threads == 0 {
        encoder_thread_count()
    } else {
        threads
    };

    // ── Step 1: Resize if needed ─────────────────────────────────────────────
    //
    // Optimizations:
    // • **Skip threshold**: when the source is within 15 % of `max_width`,
    //   skip the resize entirely.  The quality impact on a CRF-18 AVIF is
    //   imperceptible, and the resize itself costs 300-700 ms.
    // • **Triangle filter**: for downscales that *are* needed, `Triangle`
    //   (bilinear) is ~2× faster than `CatmullRom` (bicubic).  At web
    //   resolutions the difference is invisible after AV1 compression.
    const RESIZE_SKIP_RATIO: f64 = 1.15; // 15 %
    let (src_w, src_h) = (img.width(), img.height());
    let resize_start = std::time::Instant::now();
    let img = if max_width > 0 && src_w > max_width {
        let ratio = src_w as f64 / max_width as f64;
        if ratio <= RESIZE_SKIP_RATIO {
            // Source is close enough to target — just ensure even dims.
            debug!(
                src_w,
                max_width,
                ratio = %format!("{:.2}", ratio),
                "encode_avif: skipping resize (within {:.0}% threshold)",
                (RESIZE_SKIP_RATIO - 1.0) * 100.0,
            );
            let ew = src_w & !1;
            let eh = src_h & !1;
            if ew != src_w || eh != src_h {
                std::borrow::Cow::Owned(img.crop_imm(0, 0, ew, eh))
            } else {
                std::borrow::Cow::Borrowed(img)
            }
        } else {
            let scale = max_width as f64 / src_w as f64;
            let new_w = max_width;
            let new_h = ((src_h as f64 * scale).round() as u32).max(1);
            // Ensure even dimensions for YUV420
            let new_w = new_w & !1;
            let new_h = new_h & !1;
            std::borrow::Cow::Owned(img.resize_exact(
                new_w,
                new_h,
                image::imageops::FilterType::Triangle,
            ))
        }
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
    debug!(
        src_w,
        src_h,
        out_w = width,
        out_h = height,
        elapsed_ms = resize_start.elapsed().as_millis(),
        "encode_avif: resize/crop step"
    );

    // ── Fallback: dimensions below SVT-AV1 64×64 minimum ────────────────────
    if width < 64 || height < 64 {
        debug!(
            width,
            height, "image below 64×64 minimum, using ravif directly"
        );
        return encode_avif_ravif(&img, dst);
    }

    // ── Step 2: Convert RGB → YUV420 (BT.601 full range) ──────────────────────
    //
    // Must use FULL-RANGE values so the encoded data matches the AVIF spec
    // default: an AVIF without an explicit `colr` box is assumed to be full
    // range by decoders.  Limited-range values (Y: 16-235) would be decoded
    // as if they were full-range, resulting in washed-out / incorrect colours.
    let yuv_start = std::time::Instant::now();
    let rgb = img.to_rgb8();
    let (y_plane, u_plane, v_plane) = rgb_to_yuv420_full_range(&rgb, width, height);
    debug!(
        width,
        height,
        elapsed_ms = yuv_start.elapsed().as_millis(),
        "encode_avif: RGB→YUV420 conversion (full range)"
    );

    // ── Step 3: Encode with SVT-AV1 ─────────────────────────────────────────
    let svt_start = std::time::Instant::now();
    match encode_av1_raw_full_range(
        &y_plane, &u_plane, &v_plane, width, height, crf, preset, threads,
    ) {
        Ok(av1_data) => {
            debug!(
                width,
                height,
                av1_bytes = av1_data.len(),
                elapsed_ms = svt_start.elapsed().as_millis(),
                "encode_avif: SVT-AV1 encode"
            );

            // ── Step 4: Wrap in AVIF container ───────────────────────────────
            let wrap_start = std::time::Instant::now();
            let avif_data = wrap_avif_container(&av1_data, width, height)?;

            // ── Step 5: Write to disk ─────────────────────────────────────────
            std::fs::write(dst, &avif_data).context("write AVIF file")?;
            debug!(
                avif_bytes = avif_data.len(),
                elapsed_ms = wrap_start.elapsed().as_millis(),
                "encode_avif: AVIF container wrap + write"
            );

            debug!(
                dst = %dst.display(),
                width,
                height,
                av1_bytes = av1_data.len(),
                avif_bytes = avif_data.len(),
                total_elapsed_ms = fn_start.elapsed().as_millis(),
                "encode_avif: complete (SVT-AV1)"
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

/// Convert an RGB image to YUV420 planar format (BT.601 **full range**).
///
/// Full-range BT.601 coefficients (JPEG/JFIF convention, Y: 0–255):
///   Y  =        (77·R + 150·G +  29·B + 128) >> 8      → [0, 255]
///   Cb = 128 + (-43·R -  85·G + 128·B + 128) >> 8      → [0, 255]
///   Cr = 128 + (128·R - 107·G -  21·B + 128) >> 8      → [0, 255]
///
/// This is the correct convention for AVIF without an explicit `colr` box:
/// the AVIF spec defaults to full-range when no colour-info box is present.
/// Feed the output to [`encode_av1_raw_full_range`] so the AV1 bitstream
/// colour-range signal matches the data values.
pub fn rgb_to_yuv420_full_range(
    rgb: &image::RgbImage,
    width: u32,
    height: u32,
) -> (Vec<u8>, Vec<u8>, Vec<u8>) {
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

            // Y = (77·R + 150·G + 29·B + 128) >> 8
            let y = (77 * r + 150 * g + 29 * b + 128) >> 8;
            y_plane[row * w + col] = y.clamp(0, 255) as u8;
        }
    }

    // Compute U (Cb) and V (Cr) at half resolution — average 2×2 blocks
    for cy in 0..ch {
        for cx in 0..cw {
            let row = cy * 2;
            let col = cx * 2;

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

            // Cb = 128 + (-43·R - 85·G + 128·B + 128) >> 8
            let u = 128 + ((-43 * r - 85 * g + 128 * b + 128) >> 8);
            // Cr = 128 + (128·R - 107·G - 21·B + 128) >> 8
            let v = 128 + ((128 * r - 107 * g - 21 * b + 128) >> 8);

            u_plane[cy * cw + cx] = u.clamp(0, 255) as u8;
            v_plane[cy * cw + cx] = v.clamp(0, 255) as u8;
        }
    }

    (y_plane, u_plane, v_plane)
}

/// Convert an RGB image to YUV420 planar format (BT.601 **limited range**).
///
/// Used only for tests and reference.  The primary encode path now uses
/// [`rgb_to_yuv420_full_range`] so output is consistent with the AVIF spec
/// default (full range when no `colr` box is present).
///
/// BT.601 limited-range coefficients:
///   Y  =  16 + (65.481 * R + 128.553 * G +  24.966 * B) / 255  → [16, 235]
///   Cb = 128 + (-37.797 * R -  74.203 * G + 112.0   * B) / 255 → [16, 240]
///   Cr = 128 + (112.0   * R -  93.786 * G -  18.214 * B) / 255 → [16, 240]
pub fn rgb_to_yuv420(
    rgb: &image::RgbImage,
    width: u32,
    height: u32,
) -> (Vec<u8>, Vec<u8>, Vec<u8>) {
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
#[allow(dead_code)]
#[allow(clippy::too_many_arguments)]
pub(crate) fn encode_av1_raw(
    y_plane: &[u8],
    u_plane: &[u8],
    v_plane: &[u8],
    width: u32,
    height: u32,
    crf: u32,
    preset: u32,
    threads: u32,
) -> Result<Vec<u8>> {
    // Wrap the entire config creation + encoder init in suppress_stdio_for so
    // SVT-AV1's "Svt[info]: ---…" banner (printed during EbInitEncoder inside
    // SvtAv1EncoderConfig::new) is fully silenced, not just the into_encoder step.
    let encoder = suppress_stdio_for(|| {
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
        cfg.config.logical_processors = threads;

        cfg.into_encoder()
    })
    .map_err(|e| anyhow::anyhow!("SVT-AV1 init: {:?}", e))?;

    let frame = Frame::new(
        y_plane,
        u_plane,
        v_plane,
        width,                  // y_stride
        width / 2,              // cb_stride
        width / 2,              // cr_stride
        width * height * 3 / 2, // total frame size
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
pub(crate) fn encode_avif_ravif(img: &DynamicImage, dst: &Path) -> Result<(i32, i32)> {
    use ravif::{Encoder, Img};
    use rgb::RGB8;

    let rgb = img.to_rgb8();
    let (width, height) = (rgb.width(), rgb.height());
    let w = width as usize;
    let h = height as usize;

    let pixels: Vec<RGB8> = rgb
        .as_raw()
        .chunks_exact(3)
        .map(|c| RGB8 {
            r: c[0],
            g: c[1],
            b: c[2],
        })
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
///
/// The `Av1CodecConfigurationRecord` (Av1C box) embedded in the AVIF
/// container **must** match the AV1 bitstream sequence header.  Chrome
/// validates these fields and rejects the file (rendering the image as a
/// completely white blank frame) when they differ.
///
/// We always encode YUV 4:2:0 at 8-bit depth, which maps to:
/// - AV1 Main profile (`seq_profile = 0`)
/// - chroma subsampled in both dimensions (`chroma_subsampling_x/y = true`)
///
/// `Aviffy::new()` defaults to profile 1 / 4:4:4, which is wrong.
pub fn wrap_avif_container(av1_data: &[u8], width: u32, height: u32) -> Result<Vec<u8>> {
    let mut aviffy = avif_serialize::Aviffy::new();
    // AV1 Main profile (0): supports 8-bit YUV420 — must match the SVT-AV1
    // bitstream which also encodes at profile 0.
    aviffy.set_seq_profile(0);
    // YUV 4:2:0: chroma is subsampled by 2 in both horizontal and vertical
    // directions.  Default (false, false) would declare 4:4:4 — wrong.
    aviffy.set_chroma_subsampling((true, true));
    let avif = aviffy.to_vec(av1_data, None, width, height, 8);
    Ok(avif)
}

/// Encode raw **full-range** YUV420 planes to an AV1 bitstream.
///
/// Identical to [`encode_av1_raw`] except that it sets
/// `color_range = CrFullRange` so the AV1 bitstream signals full-range
/// values (0-255 for Y, Cb, Cr), as produced by `turbojpeg::decompress_to_yuv`.
#[allow(clippy::too_many_arguments)]
fn encode_av1_raw_full_range(
    y_plane: &[u8],
    u_plane: &[u8],
    v_plane: &[u8],
    width: u32,
    height: u32,
    crf: u32,
    preset: u32,
    threads: u32,
) -> Result<Vec<u8>> {
    let encoder = suppress_stdio_for(|| {
        let mut cfg = SvtAv1EncoderConfig::new(width, height, Some(preset as i8));
        cfg.config.rate_control_mode = 0;
        cfg.config.qp = crf;
        cfg.config.pred_structure = 1;
        cfg.config.intra_period_length = -1;
        cfg.config.logical_processors = threads;
        cfg.config.color_range = svt_av1_enc::ffi::ColorRange::CrFullRange;
        cfg.into_encoder()
    })
    .map_err(|e| anyhow::anyhow!("SVT-AV1 init: {:?}", e))?;

    let frame = Frame::new(
        y_plane,
        u_plane,
        v_plane,
        width,
        width / 2,
        width / 2,
        width * height * 3 / 2,
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

/// Encode pre-decoded full-range YUV420 planes directly into an AVIF file.
///
/// Used by `process_image_v_d` to skip the `rgb_to_yuv420` conversion step
/// by feeding turbojpeg's native YUV output directly to SVT-AV1.
/// The resulting AVIF is tagged full-range in the AV1 bitstream.
#[allow(clippy::too_many_arguments)]
pub fn encode_avif_from_yuv_planes(
    y_plane: &[u8],
    u_plane: &[u8],
    v_plane: &[u8],
    width: u32,
    height: u32,
    dst: &Path,
    crf: u32,
    preset: u32,
    threads: u32,
) -> Result<(i32, i32)> {
    let av1 = encode_av1_raw_full_range(
        y_plane, u_plane, v_plane, width, height, crf, preset, threads,
    )?;
    let avif_bytes = wrap_avif_container(&av1, width, height)?;
    std::fs::write(dst, &avif_bytes).context("write AVIF file (yuv planes)")?;
    debug!(
        dst = %dst.display(),
        width,
        height,
        "AVIF encoded from YUV planes (full-range)"
    );
    Ok((width as i32, height as i32))
}

// ── Unit tests ────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use image::{DynamicImage, ImageBuffer, Rgb};
    use tempfile::TempDir;

    // ── Helpers ───────────────────────────────────────────────────────────────

    /// Build a solid-colour RgbImage of the given dimensions.
    fn solid_rgb(r: u8, g: u8, b: u8, w: u32, h: u32) -> image::RgbImage {
        ImageBuffer::from_pixel(w, h, Rgb([r, g, b]))
    }

    /// Build a DynamicImage filled with the given colour.
    fn solid_dyn(r: u8, g: u8, b: u8, w: u32, h: u32) -> DynamicImage {
        DynamicImage::ImageRgb8(solid_rgb(r, g, b, w, h))
    }

    // ── rgb_to_yuv420_full_range ──────────────────────────────────────────────

    #[test]
    fn test_yuv420_full_range_plane_dimensions() {
        let (w, h) = (64u32, 64u32);
        let rgb = solid_rgb(100, 150, 200, w, h);
        let (y, u, v) = rgb_to_yuv420_full_range(&rgb, w, h);
        assert_eq!(y.len(), (w * h) as usize, "Y plane size");
        assert_eq!(u.len(), (w / 2 * (h / 2)) as usize, "U plane size");
        assert_eq!(v.len(), (w / 2 * (h / 2)) as usize, "V plane size");
    }

    #[test]
    fn test_yuv420_full_range_black_gives_zero_luma() {
        // Full-range: black = Y=0, Cb=128, Cr=128
        let rgb = solid_rgb(0, 0, 0, 2, 2);
        let (y, u, v) = rgb_to_yuv420_full_range(&rgb, 2, 2);
        assert!(
            y.iter().all(|&p| p == 0),
            "Y must be 0 for black (full range)"
        );
        assert!(u.iter().all(|&p| p == 128), "Cb must be 128 for black");
        assert!(v.iter().all(|&p| p == 128), "Cr must be 128 for black");
    }

    #[test]
    fn test_yuv420_full_range_white_gives_max_luma() {
        // Full-range: white = Y=255, Cb=128, Cr=128
        let rgb = solid_rgb(255, 255, 255, 2, 2);
        let (y, u, v) = rgb_to_yuv420_full_range(&rgb, 2, 2);
        assert!(
            y.iter().all(|&p| p == 255),
            "Y must be 255 for white (full range)"
        );
        assert!(u.iter().all(|&p| p == 128), "Cb must be 128 for white");
        assert!(v.iter().all(|&p| p == 128), "Cr must be 128 for white");
    }

    #[test]
    fn test_yuv420_full_range_neutral_gray_has_neutral_chroma() {
        // Mid-gray (128,128,128): Cb and Cr should both be 128 (neutral).
        let rgb = solid_rgb(128, 128, 128, 2, 2);
        let (y, u, v) = rgb_to_yuv420_full_range(&rgb, 2, 2);
        // Y should be approximately 128 (±2 rounding)
        assert!(
            y.iter().all(|&p| (p as i32 - 128).abs() <= 2),
            "Y must be ~128 for mid-gray; got {:?}",
            &y[..4]
        );
        assert!(
            u.iter().all(|&p| p == 128),
            "Cb must be 128 for neutral gray"
        );
        assert!(
            v.iter().all(|&p| p == 128),
            "Cr must be 128 for neutral gray"
        );
    }

    #[test]
    fn test_yuv420_full_range_y_is_always_in_0_255() {
        // For any solid colour, Y must stay in 0..=255 (no clamping overflow).
        for &(r, g, b) in &[
            (255u8, 0, 0),
            (0, 255, 0),
            (0, 0, 255),
            (255, 255, 0),
            (0, 255, 255),
            (255, 0, 255),
        ] {
            let rgb = solid_rgb(r, g, b, 2, 2);
            let (y, u, v) = rgb_to_yuv420_full_range(&rgb, 2, 2);
            assert!(
                y.iter().all(|&p| p <= 255),
                "Y out of range for ({r},{g},{b})"
            );
            assert!(
                u.iter().all(|&p| p <= 255),
                "U out of range for ({r},{g},{b})"
            );
            assert!(
                v.iter().all(|&p| p <= 255),
                "V out of range for ({r},{g},{b})"
            );
        }
    }

    #[test]
    fn test_yuv420_full_range_vs_limited_range_luma_difference() {
        // Full-range black → Y=0; limited-range black → Y=16.
        // Full-range white → Y=255; limited-range white → Y=235.
        // They must differ: full range spans a wider value band.
        let black = solid_rgb(0, 0, 0, 2, 2);
        let (y_full, _, _) = rgb_to_yuv420_full_range(&black, 2, 2);
        let (y_ltd, _, _) = rgb_to_yuv420(&black, 2, 2);
        assert_ne!(
            y_full[0], y_ltd[0],
            "full-range and limited-range should differ for black"
        );
        assert_eq!(y_full[0], 0, "full-range black Y must be 0");
        assert_eq!(y_ltd[0], 16, "limited-range black Y must be 16");

        let white = solid_rgb(255, 255, 255, 2, 2);
        let (y_full, _, _) = rgb_to_yuv420_full_range(&white, 2, 2);
        let (y_ltd, _, _) = rgb_to_yuv420(&white, 2, 2);
        assert_eq!(y_full[0], 255, "full-range white Y must be 255");
        assert_eq!(y_ltd[0], 235, "limited-range white Y must be 235");
    }

    // ── rgb_to_yuv420 (limited-range, kept for reference tests) ──────────────

    #[test]
    fn test_yuv420_plane_dimensions() {
        let (w, h) = (64u32, 64u32);
        let rgb = solid_rgb(100, 150, 200, w, h);
        let (y, u, v) = rgb_to_yuv420(&rgb, w, h);
        assert_eq!(y.len(), (w * h) as usize, "Y plane size");
        assert_eq!(u.len(), (w / 2 * (h / 2)) as usize, "U plane size");
        assert_eq!(v.len(), (w / 2 * (h / 2)) as usize, "V plane size");
    }

    #[test]
    fn test_yuv420_pure_black_gives_limited_range_floor() {
        // BT.601: Y for black = 16 (limited-range floor)
        let rgb = solid_rgb(0, 0, 0, 2, 2);
        let (y, _u, _v) = rgb_to_yuv420(&rgb, 2, 2);
        assert!(
            y.iter().all(|&v| v == 16),
            "all Y values should be 16 for black"
        );
    }

    #[test]
    fn test_yuv420_pure_white_gives_limited_range_ceiling() {
        // BT.601: Y for white = 235 (limited-range ceiling)
        let rgb = solid_rgb(255, 255, 255, 2, 2);
        let (y, u, v) = rgb_to_yuv420(&rgb, 2, 2);
        assert!(
            y.iter().all(|&val| val == 235),
            "all Y values should be 235 for white"
        );
        // U and V for neutral grey/white should be near 128
        assert!(
            u.iter().all(|&val| (val as i32 - 128).abs() <= 2),
            "U near 128 for white"
        );
        assert!(
            v.iter().all(|&val| (val as i32 - 128).abs() <= 2),
            "V near 128 for white"
        );
    }

    #[test]
    fn test_yuv420_pure_red_has_high_cr() {
        // Pure red: Cr (V) should be significantly above 128
        let rgb = solid_rgb(255, 0, 0, 2, 2);
        let (y, _u, v) = rgb_to_yuv420(&rgb, 2, 2);
        assert!(y[0] > 16, "Y should be above floor for red");
        assert!(
            v[0] > 180,
            "Cr (V) should be high for pure red, got {}",
            v[0]
        );
    }

    #[test]
    fn test_yuv420_pure_blue_has_high_cb() {
        // Pure blue: Cb (U) should be significantly above 128
        let rgb = solid_rgb(0, 0, 255, 2, 2);
        let (_y, u, _v) = rgb_to_yuv420(&rgb, 2, 2);
        assert!(
            u[0] > 180,
            "Cb (U) should be high for pure blue, got {}",
            u[0]
        );
    }

    #[test]
    fn test_yuv420_odd_dimensions_handled_by_caller() {
        // encode_avif clips to even before calling; test with even dims only.
        let rgb = solid_rgb(128, 128, 128, 4, 4);
        let (y, u, v) = rgb_to_yuv420(&rgb, 4, 4);
        assert_eq!(y.len(), 16);
        assert_eq!(u.len(), 4);
        assert_eq!(v.len(), 4);
    }

    // ── wrap_avif_container ────────────────────────────────────────────────────

    #[test]
    fn test_wrap_avif_container_nonempty() {
        let dummy_av1 = vec![0x00u8; 64];
        let result = wrap_avif_container(&dummy_av1, 64, 64).unwrap();
        assert!(!result.is_empty(), "AVIF container must not be empty");
    }

    #[test]
    fn test_wrap_avif_container_larger_than_input() {
        let dummy_av1 = vec![0x00u8; 64];
        let result = wrap_avif_container(&dummy_av1, 64, 64).unwrap();
        assert!(
            result.len() > dummy_av1.len(),
            "AVIF container ({} bytes) should be larger than raw AV1 input ({} bytes)",
            result.len(),
            dummy_av1.len()
        );
    }

    #[test]
    fn test_wrap_avif_container_has_ftyp_box() {
        // ISOBMFF: first 4 bytes = box size, next 4 bytes = box type.
        // AVIF files always begin with an 'ftyp' box.
        let dummy_av1 = vec![0x00u8; 64];
        let result = wrap_avif_container(&dummy_av1, 64, 64).unwrap();
        assert!(
            result.len() >= 8,
            "container must have at least one box header (8 bytes)"
        );
        assert_eq!(
            &result[4..8],
            b"ftyp",
            "first ISOBMFF box type must be 'ftyp'"
        );
    }

    #[test]
    fn test_wrap_avif_container_different_sizes_produce_different_output() {
        let av1 = vec![0x01u8; 64];
        let small = wrap_avif_container(&av1, 64, 64).unwrap();
        let large = wrap_avif_container(&av1, 1280, 720).unwrap();
        // Different dimensions → different container metadata
        assert_ne!(small, large);
    }

    // ── encoder_thread_count ──────────────────────────────────────────────────

    #[test]
    fn test_encoder_thread_count_at_least_one() {
        // Regardless of environment, the result must be >= 1.
        assert!(encoder_thread_count() >= 1);
    }

    // ── encode_avif (encode_avif_ravif fallback for small images) ─────────────

    #[test]
    fn test_encode_avif_below_min_svt_size_uses_ravif() {
        // Images < 64×64 must fall back to ravif and still succeed.
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("out.avif");
        let img = solid_dyn(128, 64, 32, 32, 32);
        let (w, h) = encode_avif(&img, &dst, 18, 8, 0, 0).unwrap();
        assert_eq!(w, 32);
        assert_eq!(h, 32);
        assert!(dst.exists(), "AVIF file must exist");
        assert!(
            std::fs::metadata(&dst).unwrap().len() > 0,
            "AVIF file must not be empty"
        );
    }

    #[test]
    fn test_encode_avif_creates_file() {
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("out.avif");
        let img = solid_dyn(100, 150, 200, 128, 128);
        let result = encode_avif(&img, &dst, 18, 8, 0, 0);
        assert!(result.is_ok(), "encode_avif failed: {:?}", result.err());
        assert!(dst.exists(), "output AVIF file must exist");
        assert!(std::fs::metadata(&dst).unwrap().len() > 0);
    }

    #[test]
    fn test_encode_avif_returns_correct_dims() {
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("out.avif");
        let img = solid_dyn(80, 80, 80, 128, 96);
        let (w, h) = encode_avif(&img, &dst, 18, 8, 0, 0).unwrap();
        // Output dimensions may be even-clipped but should match the input
        assert!((w - 128).abs() <= 2, "width should be near 128, got {}", w);
        assert!((h - 96).abs() <= 2, "height should be near 96, got {}", h);
    }

    #[test]
    fn test_encode_avif_max_width_scaling() {
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("out.avif");
        // Input is 200×200; max_width=100 must scale it down.
        let img = solid_dyn(200, 100, 50, 200, 200);
        let (w, h) = encode_avif(&img, &dst, 18, 8, 100, 0).unwrap();
        assert!(w <= 100, "width {} should be ≤ max_width 100", w);
        assert!(h > 0, "height should be positive");
    }

    #[test]
    fn test_encode_avif_even_output_dimensions() {
        // Odd-dimension input should result in even-dimension output.
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("out.avif");
        let img = solid_dyn(128, 128, 128, 131, 97);
        let (w, h) = encode_avif(&img, &dst, 18, 8, 0, 0).unwrap();
        assert_eq!(w % 2, 0, "output width {} must be even", w);
        assert_eq!(h % 2, 0, "output height {} must be even", h);
    }

    // ── resize skip threshold ─────────────────────────────────────────────────

    #[test]
    fn test_encode_avif_skip_resize_within_threshold() {
        // 1050×700 with max_width=920: ratio = 1050/920 ≈ 1.14 < 1.15
        // Should skip resize → output width stays ≈ 1050
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("skip.avif");
        let img = solid_dyn(128, 128, 128, 1050, 700);
        let (w, _h) = encode_avif(&img, &dst, 18, 8, 920, 0).unwrap();
        assert!(w >= 1048, "width {} should be near 1050 (skip resize)", w);
    }

    #[test]
    fn test_encode_avif_does_resize_beyond_threshold() {
        // 1200×800 with max_width=920: ratio = 1200/920 ≈ 1.30 > 1.15
        // Should resize → output width ≤ 920
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("resize.avif");
        let img = solid_dyn(128, 128, 128, 1200, 800);
        let (w, _h) = encode_avif(&img, &dst, 18, 8, 920, 0).unwrap();
        assert!(w <= 920, "width {} should be ≤ 920 after resize", w);
    }

    #[test]
    fn test_encode_avif_explicit_threads() {
        // Verify that passing explicit thread count (1) works.
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("1thread.avif");
        let img = solid_dyn(100, 150, 200, 128, 128);
        let result = encode_avif(&img, &dst, 18, 8, 0, 1);
        assert!(
            result.is_ok(),
            "encode with 1 thread failed: {:?}",
            result.err()
        );
    }

    // ── encode_avif_ravif (direct) ────────────────────────────────────────────

    #[test]
    fn test_encode_avif_ravif_creates_file() {
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("ravif.avif");
        let img = solid_dyn(60, 120, 180, 64, 64);
        let (w, h) = encode_avif_ravif(&img, &dst).unwrap();
        assert_eq!(w, 64);
        assert_eq!(h, 64);
        assert!(dst.exists());
        assert!(std::fs::metadata(&dst).unwrap().len() > 0);
    }

    #[test]
    fn test_encode_avif_ravif_small_input() {
        // ravif should handle even very small images gracefully.
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("tiny.avif");
        let img = solid_dyn(255, 0, 0, 4, 4);
        let result = encode_avif_ravif(&img, &dst);
        assert!(
            result.is_ok(),
            "ravif should handle 4×4 images: {:?}",
            result.err()
        );
    }

    // ── suppress_stdio_for ────────────────────────────────────────────────────

    // ── encode_avif_from_yuv_planes ───────────────────────────────────────────

    #[test]
    fn test_encode_avif_from_yuv_planes_creates_file() {
        // Build YUV420 planes from a solid-green image and ensure the output
        // AVIF file is created and non-empty.
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("out.avif");
        let rgb = solid_rgb(0, 200, 0, 64, 64);
        let w = rgb.width();
        let h = rgb.height();
        let (y, u, v) = rgb_to_yuv420(&rgb, w, h);
        let result = encode_avif_from_yuv_planes(&y, &u, &v, w, h, &dst, 50, 10, 1);
        assert!(
            result.is_ok(),
            "encode_avif_from_yuv_planes failed: {:?}",
            result.err()
        );
        let (rw, rh) = result.unwrap();
        assert_eq!(rw as u32, w);
        assert_eq!(rh as u32, h);
        assert!(dst.exists(), "output AVIF file must exist");
        assert!(
            std::fs::metadata(&dst).unwrap().len() > 0,
            "output must be non-empty"
        );
    }

    #[test]
    fn test_encode_avif_from_yuv_planes_has_ftyp_box() {
        // The produced bytes must start with an AVIF ftyp box (magic bytes).
        let tmp = TempDir::new().unwrap();
        let dst = tmp.path().join("out.avif");
        let rgb = solid_rgb(100, 100, 100, 64, 64);
        let w = rgb.width();
        let h = rgb.height();
        let (y, u, v) = rgb_to_yuv420(&rgb, w, h);
        encode_avif_from_yuv_planes(&y, &u, &v, w, h, &dst, 50, 10, 1).unwrap();
        let bytes = std::fs::read(&dst).unwrap();
        // AVIF/ISOBMFF: offset 4-7 must be "ftyp"
        assert!(bytes.len() > 8, "file too short to contain ftyp box");
        assert_eq!(&bytes[4..8], b"ftyp", "AVIF must start with ftyp box");
    }

    // ── suppress_stdio_for ────────────────────────────────────────────────────

    // ── wrap_avif_container ─────────────────────────────────────────────────

    /// Parse all ISOBMFF boxes in `data` and return the byte slice of the first
    /// box whose 4CC matches `target`, or `None`.
    fn find_box<'a>(data: &'a [u8], target: &[u8; 4]) -> Option<&'a [u8]> {
        let mut pos = 0usize;
        while pos + 8 <= data.len() {
            let size = u32::from_be_bytes(data[pos..pos + 4].try_into().unwrap()) as usize;
            if size < 8 || pos + size > data.len() {
                break;
            }
            if &data[pos + 4..pos + 8] == target {
                return Some(&data[pos..pos + size]);
            }
            pos += size;
        }
        None
    }

    /// Recursively search all (potentially nested) ISOBMFF boxes for the first
    /// one whose 4CC matches `target`.  Returns the full box slice including
    /// the 8-byte header.
    fn find_box_deep<'a>(data: &'a [u8], target: &[u8; 4]) -> Option<&'a [u8]> {
        let mut pos = 0usize;
        while pos + 8 <= data.len() {
            let size = u32::from_be_bytes(data[pos..pos + 4].try_into().unwrap()) as usize;
            if size < 8 || pos + size > data.len() {
                break;
            }
            if &data[pos + 4..pos + 8] == target {
                return Some(&data[pos..pos + size]);
            }
            // Descend into known container boxes.
            let four_cc = &data[pos + 4..pos + 8];
            let is_container = matches!(
                four_cc,
                b"moov"
                    | b"trak"
                    | b"mdia"
                    | b"minf"
                    | b"stbl"
                    | b"meta"
                    | b"ipco"
                    | b"iprp"
                    | b"ilst"
                    | b"udta"
                    | b"iinf"
            );
            if is_container {
                let header = if four_cc == b"meta" { 12 } else { 8 };
                if let Some(found) = find_box_deep(&data[pos + header..pos + size], target) {
                    return Some(found);
                }
            }
            pos += size;
        }
        None
    }

    #[test]
    fn test_wrap_avif_container_av1c_signals_yuv420_main_profile() {
        // Regression: wrap_avif_container must emit an Av1C box with
        //   seq_profile = 0  (AV1 Main profile — supports YUV 4:2:0)
        //   chroma_subsampling_x = 1, chroma_subsampling_y = 1  (4:2:0)
        //
        // The previous bug: Aviffy::new() defaults to seq_profile=1 (High,
        // 4:4:4) with chroma_subsampling=(false,false), which mismatches the
        // YUV420 AV1 bitstream. Chrome validates these fields and renders a
        // completely white image when they differ.
        //
        // AV1CodecConfigurationRecord layout (4 bytes after the 4CC):
        //   [0]  0x81  marker (1) | version (7=1)
        //   [1]  seq_profile (3 MSB) | seq_level_idx_0 (5 LSB)
        //          profile=0, level=31 → 0b000_11111 = 0x1F
        //   [2]  seq_tier_0 | high_bitdepth | twelve_bit | monochrome |
        //        chroma_subsampling_x | chroma_subsampling_y | sample_pos(2)
        //          8-bit 4:2:0 → 0b0000_1100 = 0x0C
        //   [3]  0x00  (reserved + no initial_presentation_delay)
        let avif_bytes = wrap_avif_container(&[0u8; 16], 64, 64).unwrap();

        // Locate the Av1C box by scanning for the 4CC bytes.
        let av1c_pos = avif_bytes
            .windows(4)
            .position(|w| w == b"av1C")
            .expect("Av1C box not found in AVIF container output");

        // The Av1C box payload starts immediately after the 4CC.
        let config = &avif_bytes[av1c_pos + 4..av1c_pos + 8];

        assert_eq!(config[0], 0x81, "Av1C: marker+version byte must be 0x81");

        let seq_profile = config[1] >> 5;
        assert_eq!(
            seq_profile, 0,
            "Av1C: seq_profile must be 0 (Main, YUV420); got {seq_profile} — \
             this mismatch causes Chrome to render white images"
        );

        let chroma_subsampling_x = (config[2] >> 3) & 1;
        let chroma_subsampling_y = (config[2] >> 2) & 1;
        assert_eq!(
            chroma_subsampling_x, 1,
            "Av1C: chroma_subsampling_x must be 1 for YUV420"
        );
        assert_eq!(
            chroma_subsampling_y, 1,
            "Av1C: chroma_subsampling_y must be 1 for YUV420"
        );
    }

    #[test]
    fn test_suppress_stdio_for_returns_value() {
        // The closure's return value must pass through unchanged.
        let result = suppress_stdio_for(|| 42u32);
        assert_eq!(result, 42);
    }

    #[test]
    fn test_suppress_stdio_for_restores_fds() {
        // After the call, writing to stdout/stderr must still work
        // (no EBADF / broken pipe). We verify by checking that fd 1 and fd 2
        // are still valid open file descriptors using fcntl F_GETFD.
        suppress_stdio_for(|| ());
        let out_flags = unsafe { libc::fcntl(1, libc::F_GETFD) };
        let err_flags = unsafe { libc::fcntl(2, libc::F_GETFD) };
        assert!(
            out_flags >= 0,
            "stdout fd must still be valid after suppress"
        );
        assert!(
            err_flags >= 0,
            "stderr fd must still be valid after suppress"
        );
    }

    #[test]
    fn test_suppress_stdio_for_nested_calls_restore_correctly() {
        // Nested calls (sequential, since there's a mutex) must each restore fds.
        suppress_stdio_for(|| ());
        suppress_stdio_for(|| ());
        let out_flags = unsafe { libc::fcntl(1, libc::F_GETFD) };
        assert!(
            out_flags >= 0,
            "stdout fd must be valid after two suppress calls"
        );
    }
}
