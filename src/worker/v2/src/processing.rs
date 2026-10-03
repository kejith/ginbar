use crate::jobs::JobLease;
use postgres::{Client, Error as PgError};
use sha2::{Digest, Sha256};
use std::fmt;
use std::fs::{self, File};
use std::io::{Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};

const READ_BUFFER_SIZE: usize = 128 * 1024;
const SNIFF_BYTES: usize = 64;
pub const PROCESSING_VERSION: u16 = 1;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FailureClass {
    Retryable,
    Terminal,
}

#[derive(Debug)]
pub struct ProcessError {
    class: FailureClass,
    message: String,
}

impl ProcessError {
    pub fn retryable(message: impl Into<String>) -> Self {
        Self {
            class: FailureClass::Retryable,
            message: message.into(),
        }
    }

    pub fn terminal(message: impl Into<String>) -> Self {
        Self {
            class: FailureClass::Terminal,
            message: message.into(),
        }
    }

    pub fn class(&self) -> FailureClass {
        self.class
    }
}

impl fmt::Display for ProcessError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.message)
    }
}

impl std::error::Error for ProcessError {}

#[derive(Debug)]
pub enum SourceLoadError {
    Database(PgError),
    InvalidDigestLength(usize),
}

impl fmt::Display for SourceLoadError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Database(error) => write!(formatter, "load media source: {error}"),
            Self::InvalidDigestLength(length) => {
                write!(
                    formatter,
                    "media source SHA-256 has invalid length {length}"
                )
            }
        }
    }
}

impl std::error::Error for SourceLoadError {}

impl From<PgError> for SourceLoadError {
    fn from(error: PgError) -> Self {
        Self::Database(error)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SourceRecord {
    pub post_id: i64,
    pub source_type: i16,
    pub storage_key: String,
    pub declared_mime_type: String,
    pub byte_size: i64,
    pub sha256: [u8; 32],
}

pub fn load_source(
    client: &mut Client,
    post_id: i64,
) -> Result<Option<SourceRecord>, SourceLoadError> {
    let row = client.query_opt(
        r#"
SELECT post_id, source_type, storage_key, declared_mime_type, byte_size, sha256
FROM media_sources
WHERE post_id = $1
"#,
        &[&post_id],
    )?;
    let Some(row) = row else {
        return Ok(None);
    };

    let digest: Vec<u8> = row.get("sha256");
    let digest_length = digest.len();
    let sha256: [u8; 32] = digest
        .try_into()
        .map_err(|_| SourceLoadError::InvalidDigestLength(digest_length))?;

    Ok(Some(SourceRecord {
        post_id: row.get("post_id"),
        source_type: row.get("source_type"),
        storage_key: row.get("storage_key"),
        declared_mime_type: row.get("declared_mime_type"),
        byte_size: row.get("byte_size"),
        sha256,
    }))
}

pub trait Cancellation {
    fn is_cancelled(&self) -> bool;
}

impl<F> Cancellation for F
where
    F: Fn() -> bool,
{
    fn is_cancelled(&self) -> bool {
        self()
    }
}

#[derive(Debug, Default, Clone, Copy)]
pub struct NeverCancelled;

impl Cancellation for NeverCancelled {
    fn is_cancelled(&self) -> bool {
        false
    }
}

#[derive(Debug)]
pub enum PrepareError {
    Source(SourceLoadError),
    MissingSource(i64),
    InvalidJobKind(i16),
    Processing(ProcessError),
}

impl PrepareError {
    pub fn class(&self) -> FailureClass {
        match self {
            Self::Source(SourceLoadError::Database(_)) => FailureClass::Retryable,
            Self::Source(SourceLoadError::InvalidDigestLength(_))
            | Self::MissingSource(_)
            | Self::InvalidJobKind(_) => FailureClass::Terminal,
            Self::Processing(error) => error.class(),
        }
    }
}

impl fmt::Display for PrepareError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Source(error) => error.fmt(formatter),
            Self::MissingSource(post_id) => {
                write!(formatter, "media source missing for post {post_id}")
            }
            Self::InvalidJobKind(kind) => write!(formatter, "unsupported media job kind {kind}"),
            Self::Processing(error) => error.fmt(formatter),
        }
    }
}

impl std::error::Error for PrepareError {}

impl From<SourceLoadError> for PrepareError {
    fn from(error: SourceLoadError) -> Self {
        Self::Source(error)
    }
}

impl From<ProcessError> for PrepareError {
    fn from(error: ProcessError) -> Self {
        Self::Processing(error)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ImageFormat {
    Jpeg,
    Png,
    Gif,
    Webp,
    Avif,
    Heif,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum VideoFormat {
    Mp4,
    Ebml,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MediaType {
    Image(ImageFormat),
    Video(VideoFormat),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MediaKind {
    Image,
    Video,
}

impl MediaKind {
    pub fn as_i16(self) -> i16 {
        match self {
            Self::Image => 0,
            Self::Video => 1,
        }
    }
}

impl MediaType {
    pub fn kind(self) -> MediaKind {
        match self {
            Self::Image(_) => MediaKind::Image,
            Self::Video(_) => MediaKind::Video,
        }
    }
}

#[derive(Debug)]
pub struct VerifiedSource {
    pub file: File,
    pub canonical_path: PathBuf,
    pub media_type: MediaType,
    pub byte_size: u64,
    pub sha256: [u8; 32],
}

#[derive(Debug, Clone)]
pub struct MediaRoot {
    root: PathBuf,
    sources: PathBuf,
}

impl MediaRoot {
    pub fn new(root: impl AsRef<Path>) -> Result<Self, ProcessError> {
        let root = fs::canonicalize(root.as_ref()).map_err(|error| {
            ProcessError::retryable(format!("canonicalize media root: {error}"))
        })?;
        let sources = fs::canonicalize(root.join("sources")).map_err(|error| {
            ProcessError::retryable(format!("canonicalize media source root: {error}"))
        })?;
        if !sources.starts_with(&root) || !sources.is_dir() {
            return Err(ProcessError::terminal(
                "media source root must be a directory inside media root",
            ));
        }
        Ok(Self { root, sources })
    }

    pub fn root(&self) -> &Path {
        &self.root
    }

    pub fn resolve_source_key(&self, storage_key: &str) -> Result<PathBuf, ProcessError> {
        validate_source_storage_key(storage_key)?;
        let relative = Path::new(storage_key);
        if relative.is_absolute() {
            return Err(ProcessError::terminal("media source key must be relative"));
        }

        let candidate = self.root.join(relative);
        let shard = candidate
            .parent()
            .ok_or_else(|| ProcessError::terminal("media source key has no shard directory"))?;

        let shard_metadata = fs::symlink_metadata(shard).map_err(|error| {
            ProcessError::retryable(format!("inspect media source shard: {error}"))
        })?;
        if shard_metadata.file_type().is_symlink() || !shard_metadata.is_dir() {
            return Err(ProcessError::terminal(
                "media source shard must be a real directory",
            ));
        }

        let file_metadata = fs::symlink_metadata(&candidate).map_err(|error| {
            ProcessError::retryable(format!("inspect media source file: {error}"))
        })?;
        if file_metadata.file_type().is_symlink() || !file_metadata.is_file() {
            return Err(ProcessError::terminal(
                "media source must be a regular non-symlink file",
            ));
        }

        let canonical = fs::canonicalize(&candidate).map_err(|error| {
            ProcessError::retryable(format!("canonicalize media source file: {error}"))
        })?;
        if !canonical.starts_with(&self.sources) {
            return Err(ProcessError::terminal(
                "media source path escapes configured source root",
            ));
        }
        Ok(canonical)
    }

    pub fn open_verified(
        &self,
        source: &SourceRecord,
        max_source_bytes: u64,
        cancellation: &impl Cancellation,
    ) -> Result<VerifiedSource, ProcessError> {
        if max_source_bytes == 0 {
            return Err(ProcessError::terminal(
                "maximum source byte limit must be positive",
            ));
        }
        if source.byte_size <= 0 {
            return Err(ProcessError::terminal(
                "authoritative source byte size must be positive",
            ));
        }
        let expected_size = source.byte_size as u64;
        if expected_size > max_source_bytes {
            return Err(ProcessError::terminal(format!(
                "source byte size {expected_size} exceeds worker limit {max_source_bytes}"
            )));
        }
        if cancellation.is_cancelled() {
            return Err(ProcessError::retryable("source verification cancelled"));
        }

        let canonical_path = self.resolve_source_key(&source.storage_key)?;
        let mut file = File::open(&canonical_path)
            .map_err(|error| ProcessError::retryable(format!("open media source: {error}")))?;
        let metadata = file
            .metadata()
            .map_err(|error| ProcessError::retryable(format!("stat media source: {error}")))?;
        if !metadata.is_file() {
            return Err(ProcessError::terminal("media source is not a regular file"));
        }
        if metadata.len() != expected_size {
            return Err(ProcessError::terminal(format!(
                "media source size mismatch: database={expected_size} file={}",
                metadata.len()
            )));
        }

        let mut hasher = Sha256::new();
        let mut buffer = vec![0u8; READ_BUFFER_SIZE];
        let mut sniff = [0u8; SNIFF_BYTES];
        let mut sniff_len = 0usize;
        let mut total = 0u64;

        loop {
            if cancellation.is_cancelled() {
                return Err(ProcessError::retryable("source verification cancelled"));
            }
            let read = file
                .read(&mut buffer)
                .map_err(|error| ProcessError::retryable(format!("read media source: {error}")))?;
            if read == 0 {
                break;
            }

            total = total.saturating_add(read as u64);
            if total > expected_size || total > max_source_bytes {
                return Err(ProcessError::terminal(
                    "media source grew beyond authoritative size while reading",
                ));
            }

            if sniff_len < SNIFF_BYTES {
                let copy_len = (SNIFF_BYTES - sniff_len).min(read);
                sniff[sniff_len..sniff_len + copy_len].copy_from_slice(&buffer[..copy_len]);
                sniff_len += copy_len;
            }
            hasher.update(&buffer[..read]);
        }

        if total != expected_size {
            return Err(ProcessError::terminal(format!(
                "media source size changed while reading: database={expected_size} read={total}"
            )));
        }

        let actual_sha256: [u8; 32] = hasher.finalize().into();
        if actual_sha256 != source.sha256 {
            return Err(ProcessError::terminal("media source SHA-256 mismatch"));
        }

        let media_type = sniff_media_type(&sniff[..sniff_len])
            .ok_or_else(|| ProcessError::terminal("unsupported or unrecognized media type"))?;

        file.seek(SeekFrom::Start(0))
            .map_err(|error| ProcessError::retryable(format!("rewind media source: {error}")))?;

        Ok(VerifiedSource {
            file,
            canonical_path,
            media_type,
            byte_size: total,
            sha256: actual_sha256,
        })
    }
}

pub fn prepare_claimed_source(
    client: &mut Client,
    lease: &JobLease,
    media_root: &MediaRoot,
    max_source_bytes: u64,
    cancellation: &impl Cancellation,
) -> Result<VerifiedSource, PrepareError> {
    if lease.kind != 0 {
        return Err(PrepareError::InvalidJobKind(lease.kind));
    }
    let source =
        load_source(client, lease.post_id)?.ok_or(PrepareError::MissingSource(lease.post_id))?;
    Ok(media_root.open_verified(&source, max_source_bytes, cancellation)?)
}

fn is_lower_hex(value: &str) -> bool {
    value
        .bytes()
        .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

pub fn validate_source_storage_key(storage_key: &str) -> Result<(), ProcessError> {
    let mut parts = storage_key.split('/');
    let root = parts.next();
    let shard = parts.next();
    let id = parts.next();
    if root != Some("sources") || parts.next().is_some() {
        return Err(ProcessError::terminal("invalid media source storage key"));
    }
    let (Some(shard), Some(id)) = (shard, id) else {
        return Err(ProcessError::terminal("invalid media source storage key"));
    };
    if shard.len() != 2
        || id.len() != 32
        || !is_lower_hex(shard)
        || !is_lower_hex(id)
        || !id.starts_with(shard)
    {
        return Err(ProcessError::terminal("invalid media source storage key"));
    }
    Ok(())
}

pub fn sniff_media_type(header: &[u8]) -> Option<MediaType> {
    if header.starts_with(b"\x89PNG\r\n\x1a\n") {
        return Some(MediaType::Image(ImageFormat::Png));
    }
    if header.len() >= 3 && header[..3] == [0xff, 0xd8, 0xff] {
        return Some(MediaType::Image(ImageFormat::Jpeg));
    }
    if header.starts_with(b"GIF87a") || header.starts_with(b"GIF89a") {
        return Some(MediaType::Image(ImageFormat::Gif));
    }
    if header.len() >= 12 && &header[..4] == b"RIFF" && &header[8..12] == b"WEBP" {
        return Some(MediaType::Image(ImageFormat::Webp));
    }
    if header.starts_with(&[0x1a, 0x45, 0xdf, 0xa3]) {
        return Some(MediaType::Video(VideoFormat::Ebml));
    }
    if header.len() >= 12 && &header[4..8] == b"ftyp" {
        let brands = &header[8..];
        if contains_brand(brands, &[b"avif", b"avis"]) {
            return Some(MediaType::Image(ImageFormat::Avif));
        }
        if contains_brand(
            brands,
            &[b"heic", b"heix", b"hevc", b"hevx", b"mif1", b"msf1"],
        ) {
            return Some(MediaType::Image(ImageFormat::Heif));
        }
        if contains_brand(
            brands,
            &[
                b"isom", b"iso2", b"iso5", b"iso6", b"mp41", b"mp42", b"avc1", b"dash",
            ],
        ) {
            return Some(MediaType::Video(VideoFormat::Mp4));
        }
    }
    None
}

fn contains_brand(bytes: &[u8], wanted: &[&[u8; 4]]) -> bool {
    bytes
        .as_chunks::<4>()
        .0
        .iter()
        .any(|brand| wanted.iter().any(|candidate| brand == candidate.as_slice()))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OutputFormat {
    Avif,
    Webm,
    Jpeg,
    Png,
    Webp,
    Mp4,
}

impl OutputFormat {
    fn extension(self) -> &'static str {
        match self {
            Self::Avif => "avif",
            Self::Webm => "webm",
            Self::Jpeg => "jpg",
            Self::Png => "png",
            Self::Webp => "webp",
            Self::Mp4 => "mp4",
        }
    }
}

pub fn processed_output_key(
    post_id: i64,
    source_sha256: &[u8; 32],
    output_format: OutputFormat,
) -> Result<String, ProcessError> {
    if post_id <= 0 {
        return Err(ProcessError::terminal("post ID must be positive"));
    }
    let shard = (post_id as u64) & 0xff;
    let mut digest = String::with_capacity(64);
    for byte in source_sha256 {
        use fmt::Write as _;
        write!(&mut digest, "{byte:02x}").expect("writing to String cannot fail");
    }
    Ok(format!(
        "media/{shard:02x}/{post_id}/v{PROCESSING_VERSION}-{digest}.{}",
        output_format.extension()
    ))
}

pub fn validate_processed_storage_key(storage_key: &str, post_id: i64) -> Result<(), ProcessError> {
    if post_id <= 0 {
        return Err(ProcessError::terminal("post ID must be positive"));
    }
    let mut parts = storage_key.split('/');
    let root = parts.next();
    let shard = parts.next();
    let key_post = parts.next();
    let file = parts.next();
    if root != Some("media") || parts.next().is_some() {
        return Err(ProcessError::terminal(
            "invalid processed media storage key",
        ));
    }
    let (Some(shard), Some(key_post), Some(file)) = (shard, key_post, file) else {
        return Err(ProcessError::terminal(
            "invalid processed media storage key",
        ));
    };
    let expected_shard = format!("{:02x}", (post_id as u64) & 0xff);
    if shard != expected_shard || key_post != post_id.to_string() {
        return Err(ProcessError::terminal(
            "processed media key does not match post",
        ));
    }
    let prefix = format!("v{PROCESSING_VERSION}-");
    let Some(rest) = file.strip_prefix(&prefix) else {
        return Err(ProcessError::terminal(
            "processed media key has wrong version",
        ));
    };
    let Some((digest, extension)) = rest.rsplit_once('.') else {
        return Err(ProcessError::terminal(
            "processed media key has no extension",
        ));
    };
    if digest.len() != 64 || !is_lower_hex(digest) {
        return Err(ProcessError::terminal(
            "processed media key has invalid source digest",
        ));
    }
    if !matches!(extension, "avif" | "webm" | "jpg" | "png" | "webp" | "mp4") {
        return Err(ProcessError::terminal(
            "processed media key has unsupported output extension",
        ));
    }
    Ok(())
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProcessedMedia {
    pub kind: MediaKind,
    pub storage_key: String,
    pub mime_type: String,
    pub width: i32,
    pub height: i32,
    pub duration_ms: i64,
    pub byte_size: i64,
    pub sha256: [u8; 32],
    pub perceptual_hash: Option<i64>,
}

pub trait ImageProcessor {
    fn process_image(
        &mut self,
        source: &mut VerifiedSource,
        post_id: i64,
    ) -> Result<ProcessedMedia, ProcessError>;
}

pub trait VideoProcessor {
    fn process_video(
        &mut self,
        source: &mut VerifiedSource,
        post_id: i64,
    ) -> Result<ProcessedMedia, ProcessError>;
}

pub fn dispatch<I, V>(
    source: &mut VerifiedSource,
    post_id: i64,
    image_processor: &mut I,
    video_processor: &mut V,
) -> Result<ProcessedMedia, ProcessError>
where
    I: ImageProcessor,
    V: VideoProcessor,
{
    match source.media_type {
        MediaType::Image(_) => image_processor.process_image(source, post_id),
        MediaType::Video(_) => video_processor.process_video(source, post_id),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::time::{SystemTime, UNIX_EPOCH};

    struct TempMediaRoot {
        path: PathBuf,
    }

    impl TempMediaRoot {
        fn new() -> Self {
            let nonce = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .expect("clock before unix epoch")
                .as_nanos();
            let path = std::env::temp_dir().join(format!(
                "ginbar-worker-processing-{}-{nonce}",
                std::process::id()
            ));
            fs::create_dir_all(path.join("sources/01")).expect("create temp media root");
            Self { path }
        }
    }

    impl Drop for TempMediaRoot {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.path);
        }
    }

    fn source_for(key: &str, body: &[u8]) -> SourceRecord {
        let sha256: [u8; 32] = Sha256::digest(body).into();
        SourceRecord {
            post_id: 7,
            source_type: 0,
            storage_key: key.to_owned(),
            declared_mime_type: "application/octet-stream".to_owned(),
            byte_size: body.len() as i64,
            sha256,
        }
    }

    #[test]
    fn source_key_requires_exact_ingestion_grammar() {
        assert!(validate_source_storage_key("sources/01/0123456789abcdef0123456789abcdef").is_ok());
        for invalid in [
            "",
            "sources/a/not-hex",
            "sources/aa/not-32-hex",
            "sources/ab/0123456789abcdef0123456789abcdef",
            "sources/01/0123456789ABCDEF0123456789ABCDEF",
            "sources/01/0123456789abcdef0123456789abcdeg",
            "sources/01/0123456789abcdef0123456789abcdef/extra",
            "../sources/01/0123456789abcdef0123456789abcdef",
            "/sources/01/0123456789abcdef0123456789abcdef",
        ] {
            assert!(validate_source_storage_key(invalid).is_err(), "{invalid}");
        }
    }

    #[test]
    fn sniff_recognizes_supported_media_families() {
        assert_eq!(
            sniff_media_type(b"\x89PNG\r\n\x1a\nrest"),
            Some(MediaType::Image(ImageFormat::Png))
        );
        assert_eq!(
            sniff_media_type(b"\xff\xd8\xffrest"),
            Some(MediaType::Image(ImageFormat::Jpeg))
        );
        assert_eq!(
            sniff_media_type(b"RIFF0000WEBPrest"),
            Some(MediaType::Image(ImageFormat::Webp))
        );
        assert_eq!(
            sniff_media_type(b"\x1a\x45\xdf\xa3rest"),
            Some(MediaType::Video(VideoFormat::Ebml))
        );
        let mut avif = vec![0, 0, 0, 24];
        avif.extend_from_slice(b"ftypavif");
        avif.extend_from_slice(&[0, 0, 0, 0]);
        avif.extend_from_slice(b"avif");
        assert_eq!(
            sniff_media_type(&avif),
            Some(MediaType::Image(ImageFormat::Avif))
        );
        let mut mp4 = vec![0, 0, 0, 24];
        mp4.extend_from_slice(b"ftypisom");
        mp4.extend_from_slice(&[0, 0, 0, 0]);
        mp4.extend_from_slice(b"mp42");
        assert_eq!(
            sniff_media_type(&mp4),
            Some(MediaType::Video(VideoFormat::Mp4))
        );
        assert_eq!(sniff_media_type(b"plain text"), None);
    }

    #[test]
    fn verifies_size_hash_type_and_rewinds() {
        let temp = TempMediaRoot::new();
        let body = b"\x89PNG\r\n\x1a\nverified body";
        let key = "sources/01/0123456789abcdef0123456789abcdef";
        fs::write(temp.path.join(key), body).expect("write source");
        let media_root = MediaRoot::new(&temp.path).expect("media root");
        let source = source_for(key, body);

        let mut verified = media_root
            .open_verified(&source, 1024, &NeverCancelled)
            .expect("verify source");
        assert_eq!(verified.byte_size, body.len() as u64);
        assert_eq!(verified.sha256, source.sha256);
        assert_eq!(verified.media_type, MediaType::Image(ImageFormat::Png));

        let mut reread = Vec::new();
        verified.file.read_to_end(&mut reread).expect("reread");
        assert_eq!(reread, body);
    }

    #[test]
    fn integrity_failures_are_terminal_and_cancellation_is_retryable() {
        let temp = TempMediaRoot::new();
        let body = b"\x89PNG\r\n\x1a\nverified body";
        let key = "sources/01/0123456789abcdef0123456789abcdef";
        fs::write(temp.path.join(key), body).expect("write source");
        let media_root = MediaRoot::new(&temp.path).expect("media root");

        let mut wrong_hash = source_for(key, body);
        wrong_hash.sha256[0] ^= 0xff;
        let error = media_root
            .open_verified(&wrong_hash, 1024, &NeverCancelled)
            .expect_err("hash mismatch");
        assert_eq!(error.class(), FailureClass::Terminal);

        let cancelled = || true;
        let error = media_root
            .open_verified(&source_for(key, body), 1024, &cancelled)
            .expect_err("cancelled");
        assert_eq!(error.class(), FailureClass::Retryable);
    }

    #[test]
    fn processed_output_key_is_deterministic_and_versioned() {
        let digest = [0x5a; 32];
        let first = processed_output_key(513, &digest, OutputFormat::Avif).unwrap();
        let second = processed_output_key(513, &digest, OutputFormat::Avif).unwrap();
        assert_eq!(first, second);
        assert!(first.starts_with("media/01/513/v1-"));
        assert!(first.ends_with(".avif"));
        assert_ne!(
            first,
            processed_output_key(513, &digest, OutputFormat::Webm).unwrap()
        );
        assert!(validate_processed_storage_key(&first, 513).is_ok());
        assert!(validate_processed_storage_key(&first, 514).is_err());
        assert!(validate_processed_storage_key("media/01/513/v1-zzzz.avif", 513).is_err());
    }

    struct FakeImage<'a>(&'a AtomicUsize);
    struct FakeVideo<'a>(&'a AtomicUsize);

    fn fake_media(kind: MediaKind) -> ProcessedMedia {
        ProcessedMedia {
            kind,
            storage_key: "media/00/1/v1-test.bin".to_owned(),
            mime_type: "application/octet-stream".to_owned(),
            width: 1,
            height: 1,
            duration_ms: 0,
            byte_size: 1,
            sha256: [0; 32],
            perceptual_hash: None,
        }
    }

    impl ImageProcessor for FakeImage<'_> {
        fn process_image(
            &mut self,
            _source: &mut VerifiedSource,
            _post_id: i64,
        ) -> Result<ProcessedMedia, ProcessError> {
            self.0.fetch_add(1, Ordering::SeqCst);
            Ok(fake_media(MediaKind::Image))
        }
    }

    impl VideoProcessor for FakeVideo<'_> {
        fn process_video(
            &mut self,
            _source: &mut VerifiedSource,
            _post_id: i64,
        ) -> Result<ProcessedMedia, ProcessError> {
            self.0.fetch_add(1, Ordering::SeqCst);
            Ok(fake_media(MediaKind::Video))
        }
    }

    #[test]
    fn dispatch_uses_media_family_boundary() {
        let temp = TempMediaRoot::new();
        let body = b"\x89PNG\r\n\x1a\nverified body";
        let key = "sources/01/0123456789abcdef0123456789abcdef";
        fs::write(temp.path.join(key), body).unwrap();
        let root = MediaRoot::new(&temp.path).unwrap();
        let mut verified = root
            .open_verified(&source_for(key, body), 1024, &NeverCancelled)
            .unwrap();

        let image_calls = AtomicUsize::new(0);
        let video_calls = AtomicUsize::new(0);
        let mut image = FakeImage(&image_calls);
        let mut video = FakeVideo(&video_calls);
        let result = dispatch(&mut verified, 7, &mut image, &mut video).unwrap();
        assert_eq!(result.kind, MediaKind::Image);
        assert_eq!(image_calls.load(Ordering::SeqCst), 1);
        assert_eq!(video_calls.load(Ordering::SeqCst), 0);
    }
}
