use crate::processing::{Cancellation, NeverCancelled, ProcessError};
use sha2::{Digest, Sha256};
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Seek, SeekFrom, Write};
use std::path::{Component, Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

const VERIFY_BUFFER_SIZE: usize = 128 * 1024;
static STAGING_SEQUENCE: AtomicU64 = AtomicU64::new(0);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OutputPublishOutcome {
    Published,
    Reused,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PublishedOutput {
    pub outcome: OutputPublishOutcome,
    pub byte_size: u64,
    pub sha256: [u8; 32],
}

#[derive(Debug, Clone)]
pub struct OutputStore {
    root: PathBuf,
}

impl OutputStore {
    pub fn new(root: impl AsRef<Path>) -> Result<Self, ProcessError> {
        let root = fs::canonicalize(root.as_ref()).map_err(|error| {
            ProcessError::retryable(format!("canonicalize output root: {error}"))
        })?;
        let metadata = fs::symlink_metadata(&root)
            .map_err(|error| ProcessError::retryable(format!("inspect output root: {error}")))?;
        if metadata.file_type().is_symlink() || !metadata.is_dir() {
            return Err(ProcessError::terminal(
                "output root must be a real directory",
            ));
        }
        Ok(Self { root })
    }

    pub fn root(&self) -> &Path {
        &self.root
    }

    pub fn publish_bytes(
        &self,
        storage_key: &str,
        bytes: &[u8],
    ) -> Result<PublishedOutput, ProcessError> {
        self.publish_bytes_inner(storage_key, bytes, PublishFault::None)
    }

    pub fn publish_file(
        &self,
        storage_key: &str,
        source: &mut File,
        expected_size: u64,
        expected_sha256: &[u8; 32],
        cancellation: &dyn Cancellation,
    ) -> Result<PublishedOutput, ProcessError> {
        self.publish_file_inner(
            storage_key,
            source,
            expected_size,
            expected_sha256,
            cancellation,
            PublishFault::None,
        )
    }

    fn publish_bytes_inner(
        &self,
        storage_key: &str,
        bytes: &[u8],
        fault: PublishFault,
    ) -> Result<PublishedOutput, ProcessError> {
        let digest: [u8; 32] = Sha256::digest(bytes).into();
        let byte_size = bytes.len() as u64;
        self.publish_known(
            storage_key,
            byte_size,
            &digest,
            &NeverCancelled,
            fault,
            |staging| {
                staging.write_all(bytes).map_err(|error| {
                    ProcessError::retryable(format!("write staged processed output: {error}"))
                })
            },
        )
    }

    fn publish_file_inner(
        &self,
        storage_key: &str,
        source: &mut File,
        expected_size: u64,
        expected_sha256: &[u8; 32],
        cancellation: &dyn Cancellation,
        fault: PublishFault,
    ) -> Result<PublishedOutput, ProcessError> {
        if expected_size == 0 {
            return Err(ProcessError::terminal(
                "processed file publication requires positive expected size",
            ));
        }
        self.publish_known(
            storage_key,
            expected_size,
            expected_sha256,
            cancellation,
            fault,
            |staging| {
                source.seek(SeekFrom::Start(0)).map_err(|error| {
                    ProcessError::retryable(format!("rewind processed file source: {error}"))
                })?;
                let mut buffer = vec![0u8; VERIFY_BUFFER_SIZE];
                let mut hasher = Sha256::new();
                let mut total = 0u64;
                loop {
                    if cancellation.is_cancelled() {
                        return Err(ProcessError::retryable(
                            "processed file publication cancelled",
                        ));
                    }
                    let read = source.read(&mut buffer).map_err(|error| {
                        ProcessError::retryable(format!("read processed file source: {error}"))
                    })?;
                    if read == 0 {
                        break;
                    }
                    total = total.saturating_add(read as u64);
                    if total > expected_size {
                        return Err(ProcessError::terminal(
                            "processed file source grew beyond verified size",
                        ));
                    }
                    hasher.update(&buffer[..read]);
                    staging.write_all(&buffer[..read]).map_err(|error| {
                        ProcessError::retryable(format!(
                            "write staged processed file output: {error}"
                        ))
                    })?;
                }
                let actual: [u8; 32] = hasher.finalize().into();
                if total != expected_size || &actual != expected_sha256 {
                    return Err(ProcessError::terminal(
                        "processed file source changed after verification",
                    ));
                }
                Ok(())
            },
        )
    }

    fn publish_known<F>(
        &self,
        storage_key: &str,
        byte_size: u64,
        digest: &[u8; 32],
        cancellation: &dyn Cancellation,
        fault: PublishFault,
        write_staging: F,
    ) -> Result<PublishedOutput, ProcessError>
    where
        F: FnOnce(&mut File) -> Result<(), ProcessError>,
    {
        let relative = validate_output_key(storage_key)?;
        let final_path = self.root.join(relative);
        let parent_relative = relative
            .parent()
            .ok_or_else(|| ProcessError::terminal("output key has no parent directory"))?;
        let parent = self.ensure_directory_chain(parent_relative)?;

        match self.verify_existing(&final_path, byte_size, digest, cancellation)? {
            ExistingOutput::Same => {
                return Ok(PublishedOutput {
                    outcome: OutputPublishOutcome::Reused,
                    byte_size,
                    sha256: *digest,
                });
            }
            ExistingOutput::Different => {
                return Err(ProcessError::terminal(format!(
                    "processed output collision at {storage_key}"
                )));
            }
            ExistingOutput::Missing => {}
        }
        if cancellation.is_cancelled() {
            return Err(ProcessError::retryable(
                "processed output publication cancelled",
            ));
        }

        let (staging_path, mut staging) = self.create_staging_file(&parent, &final_path)?;
        if let Err(error) = write_staging(&mut staging) {
            let _ = fs::remove_file(&staging_path);
            return Err(error);
        }
        if let Err(error) = staging.sync_all() {
            let _ = fs::remove_file(&staging_path);
            return Err(ProcessError::retryable(format!(
                "fsync staged processed output: {error}"
            )));
        }
        drop(staging);

        if fault == PublishFault::AfterStagingSync {
            return Err(ProcessError::retryable(
                "injected crash after staged output fsync",
            ));
        }
        if cancellation.is_cancelled() {
            let _ = fs::remove_file(&staging_path);
            return Err(ProcessError::retryable(
                "processed output publication cancelled",
            ));
        }

        match fs::hard_link(&staging_path, &final_path) {
            Ok(()) => {
                sync_directory(&parent, "fsync output directory after publish")?;

                if fault == PublishFault::AfterFinalDirectorySync {
                    return Err(ProcessError::retryable(
                        "injected crash after final output directory fsync",
                    ));
                }

                fs::remove_file(&staging_path).map_err(|error| {
                    ProcessError::retryable(format!("remove staged processed output: {error}"))
                })?;
                sync_directory(&parent, "fsync output directory after staging cleanup")?;
                Ok(PublishedOutput {
                    outcome: OutputPublishOutcome::Published,
                    byte_size,
                    sha256: *digest,
                })
            }
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => {
                let existing =
                    self.verify_existing(&final_path, byte_size, digest, cancellation)?;
                let _ = fs::remove_file(&staging_path);
                let _ = sync_directory(&parent, "fsync output directory after staging cleanup");
                match existing {
                    ExistingOutput::Same => Ok(PublishedOutput {
                        outcome: OutputPublishOutcome::Reused,
                        byte_size,
                        sha256: *digest,
                    }),
                    ExistingOutput::Different => Err(ProcessError::terminal(format!(
                        "processed output collision at {storage_key}"
                    ))),
                    ExistingOutput::Missing => Err(ProcessError::retryable(
                        "processed output disappeared during no-overwrite publication",
                    )),
                }
            }
            Err(error) => {
                let _ = fs::remove_file(&staging_path);
                Err(ProcessError::retryable(format!(
                    "publish processed output without overwrite: {error}"
                )))
            }
        }
    }

    fn ensure_directory_chain(&self, relative: &Path) -> Result<PathBuf, ProcessError> {
        let mut current = self.root.clone();
        for component in relative.components() {
            let Component::Normal(name) = component else {
                return Err(ProcessError::terminal(
                    "output directory contains an invalid path component",
                ));
            };
            let next = current.join(name);
            match fs::symlink_metadata(&next) {
                Ok(metadata) => {
                    if metadata.file_type().is_symlink() || !metadata.is_dir() {
                        return Err(ProcessError::terminal(format!(
                            "output directory component is not a real directory: {}",
                            next.display()
                        )));
                    }
                }
                Err(error) if error.kind() == io::ErrorKind::NotFound => {
                    match fs::create_dir(&next) {
                        Ok(()) => {
                            sync_directory(
                                &current,
                                "fsync parent after output directory creation",
                            )?;
                        }
                        Err(create_error)
                            if create_error.kind() == io::ErrorKind::AlreadyExists =>
                        {
                            let metadata =
                                fs::symlink_metadata(&next).map_err(|inspect_error| {
                                    ProcessError::retryable(format!(
                                        "inspect raced output directory: {inspect_error}"
                                    ))
                                })?;
                            if metadata.file_type().is_symlink() || !metadata.is_dir() {
                                return Err(ProcessError::terminal(format!(
                                    "output directory component is not a real directory: {}",
                                    next.display()
                                )));
                            }
                        }
                        Err(create_error) => {
                            return Err(ProcessError::retryable(format!(
                                "create output directory {}: {create_error}",
                                next.display()
                            )));
                        }
                    }
                }
                Err(error) => {
                    return Err(ProcessError::retryable(format!(
                        "inspect output directory {}: {error}",
                        next.display()
                    )));
                }
            }
            current = next;
        }
        Ok(current)
    }

    fn create_staging_file(
        &self,
        parent: &Path,
        final_path: &Path,
    ) -> Result<(PathBuf, File), ProcessError> {
        let final_name = final_path
            .file_name()
            .and_then(|name| name.to_str())
            .ok_or_else(|| ProcessError::terminal("output filename is not valid UTF-8"))?;

        for _ in 0..1024 {
            let sequence = STAGING_SEQUENCE.fetch_add(1, Ordering::Relaxed);
            let staging_path = parent.join(format!(
                ".{final_name}.stage-{}-{sequence}",
                std::process::id()
            ));
            match OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&staging_path)
            {
                Ok(file) => return Ok((staging_path, file)),
                Err(error) if error.kind() == io::ErrorKind::AlreadyExists => continue,
                Err(error) => {
                    return Err(ProcessError::retryable(format!(
                        "create staged processed output: {error}"
                    )));
                }
            }
        }

        Err(ProcessError::retryable(
            "could not allocate a unique staged processed-output filename",
        ))
    }

    fn verify_existing(
        &self,
        path: &Path,
        expected_size: u64,
        expected_sha256: &[u8; 32],
        cancellation: &dyn Cancellation,
    ) -> Result<ExistingOutput, ProcessError> {
        let metadata = match fs::symlink_metadata(path) {
            Ok(metadata) => metadata,
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                return Ok(ExistingOutput::Missing)
            }
            Err(error) => {
                return Err(ProcessError::retryable(format!(
                    "inspect existing processed output: {error}"
                )))
            }
        };
        if metadata.file_type().is_symlink() || !metadata.is_file() {
            return Ok(ExistingOutput::Different);
        }
        if metadata.len() != expected_size {
            return Ok(ExistingOutput::Different);
        }

        let mut file = File::open(path).map_err(|error| {
            ProcessError::retryable(format!("open existing processed output: {error}"))
        })?;
        let mut buffer = vec![0u8; VERIFY_BUFFER_SIZE];
        let mut hasher = Sha256::new();
        let mut total = 0u64;
        loop {
            if cancellation.is_cancelled() {
                return Err(ProcessError::retryable(
                    "processed output verification cancelled",
                ));
            }
            let read = file.read(&mut buffer).map_err(|error| {
                ProcessError::retryable(format!("read existing processed output: {error}"))
            })?;
            if read == 0 {
                break;
            }
            total = total.saturating_add(read as u64);
            if total > expected_size {
                return Ok(ExistingOutput::Different);
            }
            hasher.update(&buffer[..read]);
        }
        let actual: [u8; 32] = hasher.finalize().into();
        Ok(if total == expected_size && &actual == expected_sha256 {
            ExistingOutput::Same
        } else {
            ExistingOutput::Different
        })
    }
}

fn validate_output_key(storage_key: &str) -> Result<&Path, ProcessError> {
    if storage_key.is_empty() || storage_key.len() > 512 {
        return Err(ProcessError::terminal(
            "processed output key length is invalid",
        ));
    }
    let path = Path::new(storage_key);
    if path.is_absolute() {
        return Err(ProcessError::terminal(
            "processed output key must be relative",
        ));
    }
    let mut components = path.components();
    match components.next() {
        Some(Component::Normal(root)) if root == "media" => {}
        _ => {
            return Err(ProcessError::terminal(
                "processed output key must be under media/",
            ))
        }
    }
    let mut count = 1usize;
    for component in components {
        if !matches!(component, Component::Normal(_)) {
            return Err(ProcessError::terminal(
                "processed output key contains an invalid path component",
            ));
        }
        count += 1;
    }
    if count < 2 || path.file_name().is_none() {
        return Err(ProcessError::terminal(
            "processed output key has no filename",
        ));
    }
    Ok(path)
}

fn sync_directory(path: &Path, context: &str) -> Result<(), ProcessError> {
    let directory =
        File::open(path).map_err(|error| ProcessError::retryable(format!("{context}: {error}")))?;
    directory
        .sync_all()
        .map_err(|error| ProcessError::retryable(format!("{context}: {error}")))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ExistingOutput {
    Missing,
    Same,
    Different,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum PublishFault {
    None,
    AfterStagingSync,
    AfterFinalDirectorySync,
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::processing::FailureClass;
    use std::time::{SystemTime, UNIX_EPOCH};

    struct TempRoot(PathBuf);

    impl TempRoot {
        fn new() -> Self {
            let nonce = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .expect("clock")
                .as_nanos();
            let path = std::env::temp_dir().join(format!(
                "ginbar-output-store-{}-{nonce}",
                std::process::id()
            ));
            fs::create_dir_all(&path).expect("create temp root");
            Self(path)
        }
    }

    impl Drop for TempRoot {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    #[test]
    fn publishes_and_reuses_identical_bytes() {
        let root = TempRoot::new();
        let store = OutputStore::new(&root.0).unwrap();
        let key = "media/01/1/v1-test.avif";
        let first = store.publish_bytes(key, b"same bytes").unwrap();
        let second = store.publish_bytes(key, b"same bytes").unwrap();
        assert_eq!(first.outcome, OutputPublishOutcome::Published);
        assert_eq!(second.outcome, OutputPublishOutcome::Reused);
        assert_eq!(fs::read(root.0.join(key)).unwrap(), b"same bytes");
    }

    #[test]
    fn streams_verified_file_and_reuses_without_buffering() {
        let root = TempRoot::new();
        let store = OutputStore::new(&root.0).unwrap();
        let source_path = root.0.join("source.bin");
        let bytes = vec![0x5au8; VERIFY_BUFFER_SIZE * 3 + 17];
        fs::write(&source_path, &bytes).unwrap();
        let digest: [u8; 32] = Sha256::digest(&bytes).into();
        let mut source = File::open(&source_path).unwrap();
        let key = "media/05/5/v1-test.mp4";
        let first = store
            .publish_file(
                key,
                &mut source,
                bytes.len() as u64,
                &digest,
                &NeverCancelled,
            )
            .unwrap();
        let second = store
            .publish_file(
                key,
                &mut source,
                bytes.len() as u64,
                &digest,
                &NeverCancelled,
            )
            .unwrap();
        assert_eq!(first.outcome, OutputPublishOutcome::Published);
        assert_eq!(second.outcome, OutputPublishOutcome::Reused);
        assert_eq!(first.sha256, digest);
        assert_eq!(fs::read(root.0.join(key)).unwrap(), bytes);
    }

    #[test]
    fn streamed_source_change_is_rejected_before_final_publication() {
        let root = TempRoot::new();
        let store = OutputStore::new(&root.0).unwrap();
        let source_path = root.0.join("source.bin");
        fs::write(&source_path, b"changed bytes").unwrap();
        let expected: [u8; 32] = Sha256::digest(b"verified bytes").into();
        let mut source = File::open(&source_path).unwrap();
        let key = "media/06/6/v1-test.mp4";
        let error = store
            .publish_file(key, &mut source, 14, &expected, &NeverCancelled)
            .unwrap_err();
        assert_eq!(error.class(), FailureClass::Terminal);
        assert!(!root.0.join(key).exists());
    }

    #[test]
    fn streamed_collision_never_overwrites_existing_bytes() {
        let root = TempRoot::new();
        let store = OutputStore::new(&root.0).unwrap();
        let source_path = root.0.join("source.bin");
        fs::write(&source_path, b"source").unwrap();
        let digest: [u8; 32] = Sha256::digest(b"source").into();
        let key = "media/07/7/v1-test.mp4";
        fs::create_dir_all(root.0.join("media/07/7")).unwrap();
        fs::write(root.0.join(key), b"other!").unwrap();
        let mut source = File::open(&source_path).unwrap();
        let error = store
            .publish_file(key, &mut source, 6, &digest, &NeverCancelled)
            .unwrap_err();
        assert_eq!(error.class(), FailureClass::Terminal);
        assert_eq!(fs::read(root.0.join(key)).unwrap(), b"other!");
    }

    #[test]
    fn collision_never_overwrites_existing_bytes() {
        let root = TempRoot::new();
        let store = OutputStore::new(&root.0).unwrap();
        let key = "media/02/2/v1-test.avif";
        store.publish_bytes(key, b"first").unwrap();
        let error = store.publish_bytes(key, b"second").unwrap_err();
        assert_eq!(error.class(), FailureClass::Terminal);
        assert_eq!(fs::read(root.0.join(key)).unwrap(), b"first");
    }

    #[test]
    fn retry_after_crash_before_link_publishes_once() {
        let root = TempRoot::new();
        let store = OutputStore::new(&root.0).unwrap();
        let key = "media/03/3/v1-test.avif";
        let error = store
            .publish_bytes_inner(key, b"payload", PublishFault::AfterStagingSync)
            .unwrap_err();
        assert_eq!(error.class(), FailureClass::Retryable);
        assert!(!root.0.join(key).exists());
        let retried = store.publish_bytes(key, b"payload").unwrap();
        assert_eq!(retried.outcome, OutputPublishOutcome::Published);
        assert_eq!(fs::read(root.0.join(key)).unwrap(), b"payload");
    }

    #[test]
    fn retry_after_crash_after_directory_fsync_reuses_final() {
        let root = TempRoot::new();
        let store = OutputStore::new(&root.0).unwrap();
        let key = "media/04/4/v1-test.avif";
        let error = store
            .publish_bytes_inner(key, b"payload", PublishFault::AfterFinalDirectorySync)
            .unwrap_err();
        assert_eq!(error.class(), FailureClass::Retryable);
        assert_eq!(fs::read(root.0.join(key)).unwrap(), b"payload");
        let retried = store.publish_bytes(key, b"payload").unwrap();
        assert_eq!(retried.outcome, OutputPublishOutcome::Reused);
        assert_eq!(fs::read(root.0.join(key)).unwrap(), b"payload");
    }
}
