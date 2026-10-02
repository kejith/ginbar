use sha2::{Digest, Sha256};
use std::fmt;
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Seek, SeekFrom};
use std::path::Path;

#[cfg(unix)]
use std::ffi::CString;
#[cfg(unix)]
use std::os::fd::{AsRawFd, FromRawFd};
#[cfg(unix)]
use std::os::unix::fs::OpenOptionsExt;

const SNIFF_BYTES: usize = 4096;
const READ_BUFFER_BYTES: usize = 128 * 1024;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ErrorDisposition {
    Retryable,
    Terminal,
    Cancelled,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProcessingError {
    disposition: ErrorDisposition,
    message: String,
}

impl ProcessingError {
    pub fn retryable(message: impl Into<String>) -> Self {
        Self {
            disposition: ErrorDisposition::Retryable,
            message: message.into(),
        }
    }

    pub fn terminal(message: impl Into<String>) -> Self {
        Self {
            disposition: ErrorDisposition::Terminal,
            message: message.into(),
        }
    }

    pub fn cancelled() -> Self {
        Self {
            disposition: ErrorDisposition::Cancelled,
            message: "processing cancelled".to_owned(),
        }
    }

    pub fn disposition(&self) -> ErrorDisposition {
        self.disposition
    }
}

impl fmt::Display for ProcessingError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.message)
    }
}

impl std::error::Error for ProcessingError {}

#[repr(i16)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SourceType {
    Upload = 0,
    Url = 1,
}

impl TryFrom<i16> for SourceType {
    type Error = ();

    fn try_from(value: i16) -> Result<Self, Self::Error> {
        match value {
            0 => Ok(Self::Upload),
            1 => Ok(Self::Url),
            _ => Err(()),
        }
    }
}

#[repr(i16)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MediaKind {
    Image = 0,
    Video = 1,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SniffedMediaType {
    Jpeg,
    Png,
    Gif,
    WebP,
    Avif,
    Mp4,
    WebM,
}

impl SniffedMediaType {
    pub fn kind(self) -> MediaKind {
        match self {
            Self::Jpeg | Self::Png | Self::Gif | Self::WebP | Self::Avif => MediaKind::Image,
            Self::Mp4 | Self::WebM => MediaKind::Video,
        }
    }

    pub fn mime_type(self) -> &'static str {
        match self {
            Self::Jpeg => "image/jpeg",
            Self::Png => "image/png",
            Self::Gif => "image/gif",
            Self::WebP => "image/webp",
            Self::Avif => "image/avif",
            Self::Mp4 => "video/mp4",
            Self::WebM => "video/webm",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SourceRecord {
    pub post_id: i64,
    pub source_type: SourceType,
    pub storage_key: String,
    pub source_url: Option<String>,
    pub original_name: Option<String>,
    pub declared_mime_type: String,
    pub byte_size: u64,
    pub sha256: [u8; 32],
}

#[derive(Debug)]
pub struct VerifiedSource {
    source: SourceRecord,
    media_type: SniffedMediaType,
    file: File,
}

impl VerifiedSource {
    pub fn source(&self) -> &SourceRecord {
        &self.source
    }

    pub fn media_type(&self) -> SniffedMediaType {
        self.media_type
    }

    pub fn file_mut(&mut self) -> &mut File {
        &mut self.file
    }

    pub fn rewind(&mut self) -> Result<(), ProcessingError> {
        self.file
            .seek(SeekFrom::Start(0))
            .map(|_| ())
            .map_err(|error| ProcessingError::retryable(format!("rewind verified source: {error}")))
    }
}

pub struct SourceVerifier {
    max_bytes: u64,
    #[cfg(unix)]
    sources_dir: File,
    #[cfg(not(unix))]
    sources_path: std::path::PathBuf,
}

impl SourceVerifier {
    pub fn new(root: impl AsRef<Path>, max_bytes: u64) -> Result<Self, ProcessingError> {
        if max_bytes == 0 {
            return Err(ProcessingError::terminal(
                "source verification byte limit must be positive",
            ));
        }

        let root = fs::canonicalize(root.as_ref()).map_err(|error| {
            ProcessingError::retryable(format!("canonicalize media root: {error}"))
        })?;
        let root_metadata = fs::metadata(&root)
            .map_err(|error| ProcessingError::retryable(format!("stat media root: {error}")))?;
        if !root_metadata.is_dir() {
            return Err(ProcessingError::terminal("media root is not a directory"));
        }

        let sources_path = root.join("sources");
        let sources_metadata = fs::symlink_metadata(&sources_path).map_err(|error| {
            ProcessingError::retryable(format!("stat source root: {error}"))
        })?;
        if sources_metadata.file_type().is_symlink() || !sources_metadata.is_dir() {
            return Err(ProcessingError::terminal(
                "source root must be a real directory",
            ));
        }
        let sources_path = fs::canonicalize(&sources_path).map_err(|error| {
            ProcessingError::retryable(format!("canonicalize source root: {error}"))
        })?;
        if !sources_path.starts_with(&root) {
            return Err(ProcessingError::terminal("source root escapes media root"));
        }

        #[cfg(unix)]
        let sources_dir = OpenOptions::new()
            .read(true)
            .custom_flags(libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(&sources_path)
            .map_err(|error| {
                ProcessingError::retryable(format!("open source root directory: {error}"))
            })?;

        Ok(Self {
            max_bytes,
            #[cfg(unix)]
            sources_dir,
            #[cfg(not(unix))]
            sources_path,
        })
    }

    pub fn verify<F>(
        &self,
        source: SourceRecord,
        mut is_cancelled: F,
    ) -> Result<VerifiedSource, ProcessingError>
    where
        F: FnMut() -> bool,
    {
        if source.post_id <= 0 {
            return Err(ProcessingError::terminal("source post id must be positive"));
        }
        if source.byte_size == 0 || source.byte_size > self.max_bytes {
            return Err(ProcessingError::terminal(
                "source byte size is outside the configured verification bound",
            ));
        }
        if is_cancelled() {
            return Err(ProcessingError::cancelled());
        }

        let (shard, id) = validate_source_key(&source.storage_key)?;
        let mut file = self.open_source(shard, id)?;
        let metadata = file
            .metadata()
            .map_err(|error| ProcessingError::retryable(format!("stat open source file: {error}")))?;
        if !metadata.is_file() {
            return Err(ProcessingError::terminal("source is not a regular file"));
        }
        if metadata.len() != source.byte_size {
            return Err(ProcessingError::terminal(format!(
                "source size mismatch: metadata={} expected={}",
                metadata.len(),
                source.byte_size
            )));
        }

        let mut hasher = Sha256::new();
        let mut prefix = Vec::with_capacity(SNIFF_BYTES);
        let mut buffer = vec![0_u8; READ_BUFFER_BYTES];
        let mut total = 0_u64;
        loop {
            if is_cancelled() {
                return Err(ProcessingError::cancelled());
            }
            let read = file
                .read(&mut buffer)
                .map_err(|error| ProcessingError::retryable(format!("read source file: {error}")))?;
            if read == 0 {
                break;
            }
            total = total.saturating_add(read as u64);
            if total > self.max_bytes {
                return Err(ProcessingError::terminal(
                    "source exceeded the configured verification bound",
                ));
            }
            hasher.update(&buffer[..read]);
            if prefix.len() < SNIFF_BYTES {
                let copy = (SNIFF_BYTES - prefix.len()).min(read);
                prefix.extend_from_slice(&buffer[..copy]);
            }
        }

        if is_cancelled() {
            return Err(ProcessingError::cancelled());
        }
        if total != source.byte_size {
            return Err(ProcessingError::terminal(format!(
                "source size changed during verification: read={total} expected={}",
                source.byte_size
            )));
        }
        let digest: [u8; 32] = hasher.finalize().into();
        if digest != source.sha256 {
            return Err(ProcessingError::terminal("source SHA-256 mismatch"));
        }
        let media_type = sniff_media_type(&prefix)
            .ok_or_else(|| ProcessingError::terminal("unsupported or unrecognized media type"))?;
        file.seek(SeekFrom::Start(0))
            .map_err(|error| ProcessingError::retryable(format!("rewind source file: {error}")))?;

        Ok(VerifiedSource {
            source,
            media_type,
            file,
        })
    }

    #[cfg(unix)]
    fn open_source(&self, shard: &str, id: &str) -> Result<File, ProcessingError> {
        let shard = CString::new(shard)
            .map_err(|_| ProcessingError::terminal("invalid source shard component"))?;
        // SAFETY: shard is a validated NUL-free single path component and sources_dir is an open directory FD.
        let shard_fd = unsafe {
            libc::openat(
                self.sources_dir.as_raw_fd(),
                shard.as_ptr(),
                libc::O_RDONLY | libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC,
            )
        };
        if shard_fd < 0 {
            return Err(open_error("open source shard"));
        }
        // SAFETY: successful openat returned a new owned file descriptor.
        let shard_dir = unsafe { File::from_raw_fd(shard_fd) };

        let id = CString::new(id)
            .map_err(|_| ProcessingError::terminal("invalid source file component"))?;
        // SAFETY: id is a validated NUL-free single path component and shard_dir is an open directory FD.
        let source_fd = unsafe {
            libc::openat(
                shard_dir.as_raw_fd(),
                id.as_ptr(),
                libc::O_RDONLY | libc::O_NOFOLLOW | libc::O_CLOEXEC,
            )
        };
        if source_fd < 0 {
            return Err(open_error("open source file"));
        }
        // SAFETY: successful openat returned a new owned file descriptor.
        Ok(unsafe { File::from_raw_fd(source_fd) })
    }

    #[cfg(not(unix))]
    fn open_source(&self, shard: &str, id: &str) -> Result<File, ProcessingError> {
        let shard_path = self.sources_path.join(shard);
        let shard_metadata = fs::symlink_metadata(&shard_path).map_err(|error| {
            ProcessingError::retryable(format!("stat source shard directory: {error}"))
        })?;
        if shard_metadata.file_type().is_symlink() || !shard_metadata.is_dir() {
            return Err(ProcessingError::terminal(
                "source shard must be a real directory",
            ));
        }
        let path = shard_path.join(id);
        let path_metadata = fs::symlink_metadata(&path)
            .map_err(|error| ProcessingError::retryable(format!("stat source file: {error}")))?;
        if path_metadata.file_type().is_symlink() || !path_metadata.is_file() {
            return Err(ProcessingError::terminal(
                "source path must be a regular non-symlink file",
            ));
        }
        OpenOptions::new()
            .read(true)
            .open(path)
            .map_err(|error| ProcessingError::retryable(format!("open source file: {error}")))
    }
}

#[cfg(unix)]
fn open_error(action: &str) -> ProcessingError {
    let error = std::io::Error::last_os_error();
    match error.raw_os_error() {
        Some(libc::ELOOP) | Some(libc::ENOTDIR) => {
            ProcessingError::terminal(format!("{action}: unsafe source path"))
        }
        _ => ProcessingError::retryable(format!("{action}: {error}")),
    }
}

fn validate_source_key(storage_key: &str) -> Result<(&str, &str), ProcessingError> {
    let mut parts = storage_key.split('/');
    let Some(prefix) = parts.next() else {
        return Err(ProcessingError::terminal("invalid source storage key"));
    };
    let Some(shard) = parts.next() else {
        return Err(ProcessingError::terminal("invalid source storage key"));
    };
    let Some(id) = parts.next() else {
        return Err(ProcessingError::terminal("invalid source storage key"));
    };
    if parts.next().is_some()
        || prefix != "sources"
        || shard.len() != 2
        || id.len() != 32
        || !is_lower_hex(shard.as_bytes())
        || !is_lower_hex(id.as_bytes())
        || !id.starts_with(shard)
    {
        return Err(ProcessingError::terminal("invalid source storage key"));
    }
    Ok((shard, id))
}

fn is_lower_hex(value: &[u8]) -> bool {
    value
        .iter()
        .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
}

pub fn sniff_media_type(prefix: &[u8]) -> Option<SniffedMediaType> {
    if prefix.starts_with(&[0xff, 0xd8, 0xff]) {
        return Some(SniffedMediaType::Jpeg);
    }
    if prefix.starts_with(b"\x89PNG\r\n\x1a\n") {
        return Some(SniffedMediaType::Png);
    }
    if prefix.starts_with(b"GIF87a") || prefix.starts_with(b"GIF89a") {
        return Some(SniffedMediaType::Gif);
    }
    if prefix.len() >= 12 && &prefix[..4] == b"RIFF" && &prefix[8..12] == b"WEBP" {
        return Some(SniffedMediaType::WebP);
    }
    if is_ftyp_with_brand(prefix, &[b"avif", b"avis"]) {
        return Some(SniffedMediaType::Avif);
    }
    if is_ftyp_with_brand(
        prefix,
        &[b"heic", b"heix", b"hevc", b"hevx", b"mif1", b"msf1"],
    ) {
        return None;
    }
    if is_ftyp_with_brand(
        prefix,
        &[
            b"isom", b"iso2", b"iso4", b"iso5", b"iso6", b"mp41", b"mp42", b"avc1",
            b"M4V ", b"qt  ",
        ],
    ) {
        return Some(SniffedMediaType::Mp4);
    }
    if prefix.starts_with(&[0x1a, 0x45, 0xdf, 0xa3])
        && prefix.windows(4).any(|window| window == b"webm")
    {
        return Some(SniffedMediaType::WebM);
    }
    None
}

fn is_ftyp_with_brand(prefix: &[u8], brands: &[&[u8; 4]]) -> bool {
    if prefix.len() < 16 || &prefix[4..8] != b"ftyp" {
        return false;
    }
    let declared_size = u32::from_be_bytes([prefix[0], prefix[1], prefix[2], prefix[3]]) as usize;
    if declared_size < 16 {
        return false;
    }
    let end = declared_size.min(prefix.len());
    if brands
        .iter()
        .any(|brand| prefix.get(8..12) == Some(&brand[..]))
    {
        return true;
    }
    let mut offset = 16;
    while offset + 4 <= end {
        if brands
            .iter()
            .any(|brand| prefix.get(offset..offset + 4) == Some(&brand[..]))
        {
            return true;
        }
        offset += 4;
    }
    false
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    use std::path::PathBuf;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::time::{SystemTime, UNIX_EPOCH};

    #[test]
    fn sniff_supported_media_signatures() {
        assert_eq!(
            sniff_media_type(b"\xff\xd8\xffrest"),
            Some(SniffedMediaType::Jpeg)
        );
        assert_eq!(
            sniff_media_type(b"\x89PNG\r\n\x1a\nrest"),
            Some(SniffedMediaType::Png)
        );
        assert_eq!(sniff_media_type(b"GIF89arest"), Some(SniffedMediaType::Gif));
        assert_eq!(
            sniff_media_type(b"RIFF\x00\x00\x00\x00WEBPrest"),
            Some(SniffedMediaType::WebP)
        );
        assert_eq!(
            sniff_media_type(&ftyp(b"avif", b"mif1")),
            Some(SniffedMediaType::Avif)
        );
        assert_eq!(
            sniff_media_type(&ftyp(b"isom", b"mp42")),
            Some(SniffedMediaType::Mp4)
        );
        assert_eq!(sniff_media_type(&ftyp(b"heic", b"isom")), None);
        let mut webm = vec![0x1a, 0x45, 0xdf, 0xa3, 0x42, 0x82, 0x84];
        webm.extend_from_slice(b"webm");
        assert_eq!(sniff_media_type(&webm), Some(SniffedMediaType::WebM));
        assert_eq!(sniff_media_type(b"not media"), None);
    }

    #[test]
    fn verifier_checks_size_hash_and_rewinds() {
        let fixture = Fixture::new(b"\xff\xd8\xffverified-source");
        let verifier = SourceVerifier::new(&fixture.root, 1024).expect("create verifier");
        let mut verified = verifier
            .verify(fixture.record(), || false)
            .expect("verify source");
        assert_eq!(verified.media_type(), SniffedMediaType::Jpeg);
        assert_eq!(verified.source().declared_mime_type, "application/octet-stream");
        let mut bytes = Vec::new();
        verified
            .file_mut()
            .read_to_end(&mut bytes)
            .expect("read rewound source");
        assert_eq!(bytes, fixture.body);
    }

    #[test]
    fn verifier_rejects_integrity_mismatch_and_unknown_type() {
        let fixture = Fixture::new(b"\xff\xd8\xffverified-source");
        let verifier = SourceVerifier::new(&fixture.root, 1024).expect("create verifier");
        let mut bad_size = fixture.record();
        bad_size.byte_size += 1;
        let error = verifier.verify(bad_size, || false).expect_err("size must fail");
        assert_eq!(error.disposition(), ErrorDisposition::Terminal);

        let mut bad_hash = fixture.record();
        bad_hash.sha256[0] ^= 0xff;
        let error = verifier.verify(bad_hash, || false).expect_err("hash must fail");
        assert_eq!(error.disposition(), ErrorDisposition::Terminal);

        let unknown = Fixture::new(b"not a supported media type");
        let verifier = SourceVerifier::new(&unknown.root, 1024).expect("create verifier");
        let error = verifier
            .verify(unknown.record(), || false)
            .expect_err("unknown media must fail");
        assert_eq!(error.disposition(), ErrorDisposition::Terminal);
    }

    #[test]
    fn verifier_honors_cancellation() {
        let fixture = Fixture::new(b"\xff\xd8\xffverified-source");
        let verifier = SourceVerifier::new(&fixture.root, 1024).expect("create verifier");
        let cancelled = AtomicBool::new(true);
        let error = verifier
            .verify(fixture.record(), || cancelled.load(Ordering::Relaxed))
            .expect_err("cancelled verification must fail");
        assert_eq!(error.disposition(), ErrorDisposition::Cancelled);
    }

    #[test]
    fn verifier_rejects_invalid_storage_key_shape() {
        let fixture = Fixture::new(b"\xff\xd8\xffverified-source");
        let verifier = SourceVerifier::new(&fixture.root, 1024).expect("create verifier");
        for key in [
            "sources/a/not-hex",
            "sources/aa/not-32-hex",
            "sources/ab/0123456789abcdef0123456789abcdef",
            "sources/01/0123456789ABCDEF0123456789ABCDEF",
            "sources/01/0123456789abcdef0123456789abcdef/extra",
            "../sources/01/0123456789abcdef0123456789abcdef",
        ] {
            let mut record = fixture.record();
            record.storage_key = key.to_owned();
            let error = verifier
                .verify(record, || false)
                .expect_err("invalid key must fail");
            assert_eq!(error.disposition(), ErrorDisposition::Terminal, "key={key}");
        }
    }

    #[cfg(unix)]
    #[test]
    fn verifier_rejects_symlink_source() {
        use std::os::unix::fs::symlink;

        let fixture = Fixture::new(b"\xff\xd8\xffverified-source");
        let source_path = fixture.source_path();
        let target = fixture.root.join("target");
        fs::rename(&source_path, &target).expect("move source target");
        symlink(&target, &source_path).expect("create source symlink");
        let verifier = SourceVerifier::new(&fixture.root, 1024).expect("create verifier");
        let error = verifier
            .verify(fixture.record(), || false)
            .expect_err("symlink must fail");
        assert_eq!(error.disposition(), ErrorDisposition::Terminal);
    }

    fn ftyp(major: &[u8; 4], compatible: &[u8; 4]) -> Vec<u8> {
        let mut bytes = Vec::new();
        bytes.extend_from_slice(&20_u32.to_be_bytes());
        bytes.extend_from_slice(b"ftyp");
        bytes.extend_from_slice(major);
        bytes.extend_from_slice(&0_u32.to_be_bytes());
        bytes.extend_from_slice(compatible);
        bytes
    }

    struct Fixture {
        root: PathBuf,
        body: Vec<u8>,
        storage_key: String,
        sha256: [u8; 32],
    }

    impl Fixture {
        fn new(body: &[u8]) -> Self {
            let nonce = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .expect("clock before epoch")
                .as_nanos();
            let root = std::env::temp_dir().join(format!(
                "ginbar-worker-source-{}-{nonce}",
                std::process::id()
            ));
            let storage_key = "sources/01/0123456789abcdef0123456789abcdef".to_owned();
            let source_path = root.join(&storage_key);
            fs::create_dir_all(source_path.parent().expect("source parent"))
                .expect("create source dirs");
            let mut file = File::create(&source_path).expect("create source file");
            file.write_all(body).expect("write source");
            file.sync_all().expect("sync source");
            let sha256 = Sha256::digest(body).into();
            Self {
                root,
                body: body.to_vec(),
                storage_key,
                sha256,
            }
        }

        fn record(&self) -> SourceRecord {
            SourceRecord {
                post_id: 1,
                source_type: SourceType::Upload,
                storage_key: self.storage_key.clone(),
                source_url: None,
                original_name: Some("fixture.jpg".to_owned()),
                declared_mime_type: "application/octet-stream".to_owned(),
                byte_size: self.body.len() as u64,
                sha256: self.sha256,
            }
        }

        fn source_path(&self) -> std::path::PathBuf {
            self.root.join(&self.storage_key)
        }
    }

    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }
}
