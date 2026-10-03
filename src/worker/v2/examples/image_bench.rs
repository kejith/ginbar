use ginbar_worker_v2::image::BoundedImageProcessor;
use ginbar_worker_v2::output::OutputStore;
use ginbar_worker_v2::processing::{
    ImageProcessor, MediaRoot, NeverCancelled, SourceRecord,
};
use sha2::{Digest, Sha256};
use std::env;
use std::fs;
use std::path::PathBuf;
use std::process::ExitCode;
use std::time::{Instant, SystemTime, UNIX_EPOCH};

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("{error}");
            ExitCode::FAILURE
        }
    }
}

fn run() -> Result<(), String> {
    let mut args = env::args();
    let program = args.next().unwrap_or_else(|| "image_bench".to_owned());
    let input = args.next().ok_or_else(|| usage(&program))?;
    let iterations = args
        .next()
        .map(|value| {
            value
                .parse::<u32>()
                .map_err(|_| "iterations must be a positive integer".to_owned())
        })
        .transpose()?
        .unwrap_or(5);
    if iterations == 0 || args.next().is_some() {
        return Err(usage(&program));
    }

    let body = fs::read(&input).map_err(|error| format!("read {input}: {error}"))?;
    if body.is_empty() {
        return Err("benchmark input is empty".to_owned());
    }
    let digest: [u8; 32] = Sha256::digest(&body).into();

    let temp = TempRoot::new()?;
    let source_key = "sources/01/0123456789abcdef0123456789abcdef";
    let source_path = temp.path.join(source_key);
    fs::create_dir_all(
        source_path
            .parent()
            .ok_or_else(|| "benchmark source path has no parent".to_owned())?,
    )
    .map_err(|error| format!("create benchmark source directory: {error}"))?;
    fs::write(&source_path, &body)
        .map_err(|error| format!("write benchmark source: {error}"))?;

    let source_record = SourceRecord {
        post_id: 1,
        source_type: 0,
        storage_key: source_key.to_owned(),
        declared_mime_type: String::new(),
        byte_size: body.len() as i64,
        sha256: digest,
    };
    let media_root = MediaRoot::new(&temp.path).map_err(|error| error.to_string())?;
    let outputs = OutputStore::new(&temp.path).map_err(|error| error.to_string())?;
    let mut processor = BoundedImageProcessor::new(outputs);

    println!("iteration\tverify_ms\tprocess_ms\toutput_bytes\twidth\theight");
    for iteration in 0..iterations {
        let verify_started = Instant::now();
        let mut source = media_root
            .open_verified(&source_record, body.len() as u64, &NeverCancelled)
            .map_err(|error| format!("verify benchmark source: {error}"))?;
        let verify_ms = verify_started.elapsed().as_secs_f64() * 1000.0;

        let post_id = 1_000_000i64 + i64::from(iteration);
        let process_started = Instant::now();
        let processed = processor
            .process_image(&mut source, post_id)
            .map_err(|error| format!("process iteration {iteration}: {error}"))?;
        let process_ms = process_started.elapsed().as_secs_f64() * 1000.0;

        println!(
            "{}\t{verify_ms:.3}\t{process_ms:.3}\t{}\t{}\t{}",
            iteration + 1,
            processed.byte_size,
            processed.width,
            processed.height
        );
    }
    Ok(())
}

fn usage(program: &str) -> String {
    format!("usage: {program} <JPEG|PNG|WebP input path> [iterations]")
}

struct TempRoot {
    path: PathBuf,
}

impl TempRoot {
    fn new() -> Result<Self, String> {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|error| format!("read clock: {error}"))?
            .as_nanos();
        let path = env::temp_dir().join(format!(
            "ginbar-image-bench-{}-{nonce}",
            std::process::id()
        ));
        fs::create_dir_all(&path)
            .map_err(|error| format!("create benchmark temp root: {error}"))?;
        Ok(Self { path })
    }
}

impl Drop for TempRoot {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.path);
    }
}
