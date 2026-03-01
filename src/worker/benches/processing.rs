//! Benchmarks for the wallium-worker processing pipeline.
//!
//! ## Generating test inputs
//!
//! The benchmarks expect synthetic JPEG files at fixed paths.  Run the helper
//! script once to create them:
//!
//! ```sh
//! ./scripts/gen_bench_inputs.sh
//! ```
//!
//! This creates three JPEG files in `/tmp`:
//! - `/tmp/bench_640x427.jpg`
//! - `/tmp/bench_1280x853.jpg`
//! - `/tmp/bench_1920x1279.jpg`
//!
//! ## Running
//!
//! ```sh
//! cargo bench -p wallium-worker
//! ```

use criterion::{criterion_group, criterion_main, BenchmarkId, Criterion};
use std::path::Path;
use tempfile::TempDir;

// Bring in crate internals via the library (we're an external bench harness).
use wallium_worker::{avif, ffmpeg, processing};

// ── Constants ─────────────────────────────────────────────────────────────────

/// Synthetic test inputs (must be generated beforehand).
const TEST_INPUTS: &[(&str, &str)] = &[
    ("640x427", "/tmp/bench_640x427.jpg"),
    ("1280x853", "/tmp/bench_1280x853.jpg"),
    ("1920x1279", "/tmp/bench_1920x1279.jpg"),
];

// ── Helpers ───────────────────────────────────────────────────────────────────

/// Build a throwaway directory tree that mirrors the production media layout.
fn bench_dirs() -> (processing::Directories, TempDir) {
    let tmp = TempDir::new().expect("create temp dir");
    let root = tmp.path();
    let dirs = processing::Directories {
        image: root.join("images"),
        thumbnail: root.join("thumbnails"),
        video: root.join("videos"),
        tmp: root.join("tmp"),
        upload: root.join("upload"),
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

/// Skip a benchmark when the test input file does not exist.
fn input_available(path: &str) -> bool {
    Path::new(path).exists()
}

/// Create a tokio runtime for async benchmarks.
fn rt() -> tokio::runtime::Runtime {
    tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .unwrap()
}

// ── convert_to_avif (full-resolution) ─────────────────────────────────────────

fn bench_convert_to_avif_fullres(c: &mut Criterion) {
    let mut group = c.benchmark_group("convert_to_avif_fullres");
    group.sample_size(10);
    group.measurement_time(std::time::Duration::from_secs(15));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            eprintln!("SKIP {name}: {path} not found — run scripts/gen_bench_inputs.sh");
            continue;
        }

        // In-process SVT-AV1 bindings
        let img = image::open(path).expect("load image for bench");
        group.bench_with_input(BenchmarkId::new("inprocess_crf18_preset8", name), &img, |b, img| {
            b.iter(|| {
                let (dirs, _tmp) = bench_dirs();
                let dst = dirs.image.join("out.avif");
                avif::encode_avif(img, &dst, 18, 8, 920, 0).unwrap();
            });
        });
    }

    group.finish();
}

// ── normalize_to_jpeg ─────────────────────────────────────────────────────────

fn bench_normalize_to_jpeg(c: &mut Criterion) {
    let mut group = c.benchmark_group("normalize_to_jpeg");
    group.sample_size(10);
    group.measurement_time(std::time::Duration::from_secs(7));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        group.bench_with_input(BenchmarkId::new("ffmpeg", name), &path, |b, &p| {
            let rt = rt();
            b.iter(|| {
                let (dirs, _tmp) = bench_dirs();
                let dst = dirs.tmp.join("thumbnails").join("norm.jpg");
                rt.block_on(async {
                    ffmpeg::normalize_to_jpeg(Path::new(p), &dst).await.unwrap();
                });
                let _ = std::fs::remove_file(&dst);
            });
        });
    }

    group.finish();
}

// ── compute_phash ─────────────────────────────────────────────────────────────

fn bench_compute_phash(c: &mut Criterion) {
    let mut group = c.benchmark_group("compute_phash");
    group.measurement_time(std::time::Duration::from_secs(10));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        // Load the image once, benchmark only the hashing.
        let img = image::open(path).expect("load image");

        group.bench_with_input(BenchmarkId::new("double_gradient_16x16", name), &img, |b, img| {
            b.iter(|| {
                std::hint::black_box(processing::compute_phash(img));
            });
        });
    }

    group.finish();
}

// ── create_thumbnail ──────────────────────────────────────────────────────────

fn bench_create_thumbnail(c: &mut Criterion) {
    let mut group = c.benchmark_group("create_thumbnail");
    group.sample_size(10);
    group.measurement_time(std::time::Duration::from_secs(13));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        // Load the image once, benchmark the crop + resize + AVIF encode.
        let img = image::open(path).expect("load image");

        group.bench_with_input(BenchmarkId::new("150x150_avif", name), &name, |b, _| {
            let rt = rt();
            b.iter(|| {
                let (dirs, _tmp) = bench_dirs();
                let dst = dirs.thumbnail.join("thumb.avif");
                rt.block_on(async {
                    processing::create_thumbnail(&img, &dst, &dirs).await.unwrap();
                });
            });
        });
    }

    group.finish();
}

// ── process_image (end-to-end) ────────────────────────────────────────────────

fn bench_process_image(c: &mut Criterion) {
    let mut group = c.benchmark_group("process_image");
    group.sample_size(10);
    group.measurement_time(std::time::Duration::from_secs(30));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        group.bench_with_input(BenchmarkId::new("e2e", name), &path, |b, &p| {
            let rt = rt();
            b.iter_batched(
                || {
                    // Setup: create dirs and copy input so each iteration has a fresh file.
                    let (dirs, tmp) = bench_dirs();
                    let src = dirs.upload.join(format!("input_{}.jpg", processing::generate_name()));
                    std::fs::copy(p, &src).unwrap();
                    (dirs, tmp, src)
                },
                |(dirs, _tmp, src)| {
                    rt.block_on(async {
                        processing::process_image(&src, &dirs).await.unwrap();
                    });
                },
                criterion::BatchSize::PerIteration,
            );
        });
    }

    group.finish();
}

// ── Preset comparison ─────────────────────────────────────────────────────────

fn bench_avif_preset_comparison(c: &mut Criterion) {
    let mut group = c.benchmark_group("avif_preset_comparison");
    group.sample_size(10);
    group.measurement_time(std::time::Duration::from_secs(25));

    // Use the medium-sized input for the comparison.
    let (name, path) = TEST_INPUTS[1];
    if !input_available(path) {
        eprintln!("SKIP preset comparison: {path} not found");
        return;
    }

    let img = image::open(path).expect("load image for preset comparison");

    for preset in [4, 6, 8, 10] {
        // In-process SVT-AV1 bindings
        group.bench_with_input(
            BenchmarkId::new(format!("inprocess_crf18_preset{preset}"), name),
            &img,
            |b, img| {
                b.iter(|| {
                    let (dirs, _tmp) = bench_dirs();
                    let dst = dirs.image.join("out.avif");
                    avif::encode_avif(img, &dst, 18, preset, 0, 0).unwrap();
                });
            },
        );
    }

    group.finish();
}

// ── Wire up criterion ─────────────────────────────────────────────────────────

criterion_group!(
    benches,
    bench_convert_to_avif_fullres,
    bench_normalize_to_jpeg,
    bench_compute_phash,
    bench_create_thumbnail,
    bench_process_image,
    bench_avif_preset_comparison,
    // New groups added below
    bench_rgb_to_yuv420,
    bench_wrap_avif_container,
    bench_smart_crop,
    bench_dct1d_partial,
    bench_region_gradient_energy,
    bench_encode_avif_ravif,
    bench_generate_name,
    bench_prepare_thumbnail_pixels,
    bench_decode_jpeg,
);
criterion_main!(benches);

// ── prepare_thumbnail_pixels (smart_crop + resize, no encode) ─────────────────

fn bench_prepare_thumbnail_pixels(c: &mut Criterion) {
    let mut group = c.benchmark_group("prepare_thumbnail_pixels");
    group.measurement_time(std::time::Duration::from_secs(8));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        let img = image::open(path).expect("load image for prepare_thumbnail_pixels bench");

        group.bench_with_input(
            BenchmarkId::new("crop_resize_150x150", name),
            &img,
            |b, img| {
                b.iter(|| {
                    std::hint::black_box(processing::prepare_thumbnail_pixels(img));
                });
            },
        );
    }

    group.finish();
}

// ── rgb_to_yuv420 ─────────────────────────────────────────────────────────────

fn bench_rgb_to_yuv420(c: &mut Criterion) {
    let mut group = c.benchmark_group("rgb_to_yuv420");
    group.measurement_time(std::time::Duration::from_secs(8));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        let img = image::open(path).expect("load image").to_rgb8();
        let (w, h) = (img.width(), img.height());

        group.bench_with_input(BenchmarkId::new("bt601_yuv420", name), &img, |b, img| {
            b.iter(|| {
                std::hint::black_box(avif::rgb_to_yuv420(img, w, h));
            });
        });
    }

    group.finish();
}

// ── wrap_avif_container ────────────────────────────────────────────────────────

fn bench_wrap_avif_container(c: &mut Criterion) {
    let mut group = c.benchmark_group("wrap_avif_container");
    group.measurement_time(std::time::Duration::from_secs(5));

    // Use synthetic AV1 payloads of increasing sizes.
    for size in [4 * 1024usize, 64 * 1024, 512 * 1024] {
        let dummy_av1 = vec![0x00u8; size];
        let label = format!("{}kb", size / 1024);
        group.bench_with_input(
            BenchmarkId::new("avif_container", &label),
            &dummy_av1,
            |b, data| {
                b.iter(|| {
                    std::hint::black_box(avif::wrap_avif_container(data, 1920, 1080).unwrap());
                });
            },
        );
    }

    group.finish();
}

// ── smart_crop ────────────────────────────────────────────────────────────────

fn bench_smart_crop(c: &mut Criterion) {
    let mut group = c.benchmark_group("smart_crop");
    group.sample_size(20);
    group.measurement_time(std::time::Duration::from_secs(10));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        let img = image::open(path).expect("load image for smart_crop bench");

        group.bench_with_input(
            BenchmarkId::new("gradient_saliency_150x150", name),
            &img,
            |b, img| {
                b.iter(|| {
                    std::hint::black_box(processing::smart_crop(img, 150, 150));
                });
            },
        );
    }

    group.finish();
}

// ── dct1d_partial ─────────────────────────────────────────────────────────────

fn bench_dct1d_partial(c: &mut Criterion) {
    let mut group = c.benchmark_group("dct1d_partial");
    group.measurement_time(std::time::Duration::from_secs(5));

    // Simulate the actual usage: 256-element row DCT keeping 16 coefficients.
    let input: Vec<f64> = (0..256).map(|i| (i as f64).sin()).collect();
    let mut output = vec![0.0f64; 16];

    group.bench_function("n256_k16", |b| {
        b.iter(|| {
            std::hint::black_box(processing::dct1d_partial(&input, &mut output, 16));
        });
    });

    // Column transform: 256-element column DCT keeping 16 coefficients.
    group.bench_function("n256_k16_col", |b| {
        b.iter(|| {
            std::hint::black_box(processing::dct1d_partial(&input, &mut output, 16));
        });
    });

    group.finish();
}

// ── region_gradient_energy ────────────────────────────────────────────────────

fn bench_region_gradient_energy(c: &mut Criterion) {
    let mut group = c.benchmark_group("region_gradient_energy");
    group.measurement_time(std::time::Duration::from_secs(5));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        let img = image::open(path).expect("load image").to_luma8();
        let (w, h) = (img.width(), img.height());
        // Analyse the full image at step=2 (same as smart_crop).
        let step = 2u32;

        group.bench_with_input(
            BenchmarkId::new("full_frame_step2", name),
            &img,
            |b, gray| {
                b.iter(|| {
                    std::hint::black_box(processing::region_gradient_energy(
                        gray, 0, 0, w, h, step,
                    ));
                });
            },
        );
    }

    group.finish();
}

// ── encode_avif_ravif (fallback encoder) ──────────────────────────────────────

fn bench_encode_avif_ravif(c: &mut Criterion) {
    let mut group = c.benchmark_group("encode_avif_ravif");
    group.sample_size(10);
    group.measurement_time(std::time::Duration::from_secs(15));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        let img = image::open(path).expect("load image for ravif bench");

        group.bench_with_input(
            BenchmarkId::new("quality80_speed4", name),
            &img,
            |b, img| {
                b.iter(|| {
                    let (dirs, _tmp) = bench_dirs();
                    let dst = dirs.image.join("ravif_out.avif");
                    // Access the crate-public encode function via the full path
                    // (avif::encode_avif with max_width=0 and small preset to minimise time).
                    avif::encode_avif(img, &dst, 50, 12, 0, 0).unwrap();
                });
            },
        );
    }

    group.finish();
}

// ── generate_name ─────────────────────────────────────────────────────────────

fn bench_generate_name(c: &mut Criterion) {
    let mut group = c.benchmark_group("generate_name");
    group.measurement_time(std::time::Duration::from_secs(3));

    group.bench_function("uuid_v4", |b| {
        b.iter(|| {
            std::hint::black_box(processing::generate_name());
        });
    });

    group.finish();
}

// ── decode_jpeg (turbojpeg vs image crate) ────────────────────────────────────

fn bench_decode_jpeg(c: &mut Criterion) {
    let mut group = c.benchmark_group("decode_jpeg");
    group.sample_size(10);
    group.measurement_time(std::time::Duration::from_secs(10));

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        // turbojpeg (libjpeg-turbo) — no DCT downscale
        group.bench_with_input(
            BenchmarkId::new("turbojpeg_full", name),
            &path,
            |b, &p| {
                b.iter(|| {
                    std::hint::black_box(
                        processing::decode_jpeg_turbo(Path::new(p), 0).unwrap()
                    );
                });
            },
        );

        // turbojpeg with DCT 1/2 downscale (for images > 2× target width)
        group.bench_with_input(
            BenchmarkId::new("turbojpeg_half", name),
            &path,
            |b, &p| {
                b.iter(|| {
                    std::hint::black_box(
                        processing::decode_jpeg_turbo(Path::new(p), 920).unwrap()
                    );
                });
            },
        );

        // image crate (zune-jpeg under the hood)
        group.bench_with_input(
            BenchmarkId::new("image_crate", name),
            &path,
            |b, &p| {
                b.iter(|| {
                    std::hint::black_box(image::open(p).unwrap());
                });
            },
        );
    }

    group.finish();
}
