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
use wallium_worker::{ffmpeg, processing};

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
    // AVIF encoding is slow — reduce sample count for practical bench times.
    group.sample_size(10);

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            eprintln!("SKIP {name}: {path} not found — run scripts/gen_bench_inputs.sh");
            continue;
        }

        group.bench_with_input(BenchmarkId::new("crf18_preset8", name), &path, |b, &p| {
            let rt = rt();
            b.iter(|| {
                let (dirs, _tmp) = bench_dirs();
                let dst = dirs.image.join("out.avif");
                rt.block_on(async {
                    ffmpeg::convert_to_avif(Path::new(p), &dst, 18, 8, 920).await.unwrap();
                });
            });
        });
    }

    group.finish();
}

// ── convert_to_avif (thumbnail settings) ──────────────────────────────────────

fn bench_convert_to_avif_thumbnail(c: &mut Criterion) {
    let mut group = c.benchmark_group("convert_to_avif_thumbnail");
    group.sample_size(10);

    for &(name, path) in TEST_INPUTS {
        if !input_available(path) {
            continue;
        }

        group.bench_with_input(BenchmarkId::new("crf30_preset6", name), &path, |b, &p| {
            let rt = rt();
            b.iter(|| {
                let (dirs, _tmp) = bench_dirs();
                let dst = dirs.thumbnail.join("out.avif");
                rt.block_on(async {
                    ffmpeg::convert_to_avif(Path::new(p), &dst, 30, 6, 0).await.unwrap();
                });
            });
        });
    }

    group.finish();
}

// ── normalize_to_jpeg ─────────────────────────────────────────────────────────

fn bench_normalize_to_jpeg(c: &mut Criterion) {
    let mut group = c.benchmark_group("normalize_to_jpeg");
    group.sample_size(10);

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
    // End-to-end is the slowest — minimal samples.
    group.sample_size(10);

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

    // Use the medium-sized input for the comparison.
    let (name, path) = TEST_INPUTS[1];
    if !input_available(path) {
        eprintln!("SKIP preset comparison: {path} not found");
        return;
    }

    for preset in [4, 6, 8, 10] {
        group.bench_with_input(
            BenchmarkId::new(format!("crf18_preset{preset}"), name),
            &preset,
            |b, &preset| {
                let rt = rt();
                b.iter(|| {
                    let (dirs, _tmp) = bench_dirs();
                    let dst = dirs.image.join("out.avif");
                    rt.block_on(async {
                        ffmpeg::convert_to_avif(Path::new(path), &dst, 18, preset, 0)
                            .await
                            .unwrap();
                    });
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
    bench_convert_to_avif_thumbnail,
    bench_normalize_to_jpeg,
    bench_compute_phash,
    bench_create_thumbnail,
    bench_process_image,
    bench_avif_preset_comparison,
);
criterion_main!(benches);
