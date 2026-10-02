use ginbar_worker_v2::source::{SourceRecord, SourceType, SourceVerifier};
use sha2::{Digest, Sha256};
use std::fs::{self, File};
use std::hint::black_box;
use std::io::Write;
use std::time::Instant;

const PAYLOAD_BYTES: usize = 8 << 20;
const STORAGE_KEY: &str = "sources/ab/ab23456789abcdef0123456789abcdef";

#[test]
#[ignore = "target-host measurement probe; set GINBAR_SOURCE_BENCH_ROOT"]
fn measure_source_open_hash_sniff_8mib() {
    let root = std::env::var("GINBAR_SOURCE_BENCH_ROOT")
        .expect("GINBAR_SOURCE_BENCH_ROOT must name a disposable target-local directory");
    let iterations = std::env::var("GINBAR_SOURCE_BENCH_ITERATIONS")
        .ok()
        .and_then(|value| value.parse::<u32>().ok())
        .unwrap_or(10);
    assert!(iterations > 0, "benchmark iterations must be positive");

    let path = std::path::Path::new(&root).join(STORAGE_KEY);
    fs::create_dir_all(path.parent().expect("benchmark source parent"))
        .expect("create benchmark source directories");
    let mut body = vec![0x5a_u8; PAYLOAD_BYTES];
    body[..3].copy_from_slice(&[0xff, 0xd8, 0xff]);
    let mut file = File::create(&path).expect("create benchmark source");
    file.write_all(&body).expect("write benchmark source");
    file.sync_all().expect("sync benchmark source");
    let sha256: [u8; 32] = Sha256::digest(&body).into();
    let record = SourceRecord {
        post_id: 1,
        source_type: SourceType::Upload,
        storage_key: STORAGE_KEY.to_owned(),
        source_url: None,
        original_name: None,
        declared_mime_type: "application/octet-stream".to_owned(),
        byte_size: PAYLOAD_BYTES as u64,
        sha256,
    };
    let verifier = SourceVerifier::new(&root, PAYLOAD_BYTES as u64).expect("create verifier");

    let start = Instant::now();
    for _ in 0..iterations {
        let verified = verifier
            .verify(record.clone(), || false)
            .expect("verify benchmark source");
        black_box(verified.media_type());
    }
    let elapsed = start.elapsed();
    let ns_per_op = elapsed.as_nanos() / u128::from(iterations);
    let mib_per_second = (PAYLOAD_BYTES as f64 * f64::from(iterations))
        / elapsed.as_secs_f64()
        / (1024.0 * 1024.0);
    println!(
        "source_verify_8mib iterations={iterations} total_ms={:.3} ns_per_op={ns_per_op} mib_per_s={mib_per_second:.2}",
        elapsed.as_secs_f64() * 1000.0
    );

    fs::remove_file(&path).expect("remove benchmark source");
}
