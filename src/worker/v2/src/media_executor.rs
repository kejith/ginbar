use crate::image::BoundedImageProcessor;
use crate::jobs::JobLease;
use crate::output::OutputStore;
use crate::processing::{
    prepare_claimed_source, FailureClass, ImageProcessor, MediaRoot, MediaType,
};
use crate::publication::{publish_processed, PublicationError, PublishOutcome};
use crate::runner::{ActiveJobCancellation, JobExecution, JobExecutor};
use crate::video::{BoundedVideoProcessor, VideoProbe, VideoThumbnailer};
use postgres::Client;

pub struct MediaJobExecutor {
    media_root: MediaRoot,
    max_source_bytes: u64,
    image_processor: BoundedImageProcessor,
    video_processor: BoundedVideoProcessor,
}

impl MediaJobExecutor {
    pub fn new(media_root: MediaRoot, max_source_bytes: u64) -> Result<Self, String> {
        let output_store = OutputStore::new(media_root.root())
            .map_err(|error| format!("open output store: {error}"))?;
        Ok(Self {
            media_root,
            max_source_bytes,
            image_processor: BoundedImageProcessor::new(output_store.clone()),
            video_processor: BoundedVideoProcessor::new(output_store),
        })
    }

    pub fn with_video_components(
        media_root: MediaRoot,
        max_source_bytes: u64,
        probe: Box<dyn VideoProbe>,
        thumbnailer: Box<dyn VideoThumbnailer>,
    ) -> Result<Self, String> {
        let output_store = OutputStore::new(media_root.root())
            .map_err(|error| format!("open output store: {error}"))?;
        Ok(Self {
            media_root,
            max_source_bytes,
            image_processor: BoundedImageProcessor::new(output_store.clone()),
            video_processor: BoundedVideoProcessor::with_components(
                output_store,
                probe,
                thumbnailer,
            ),
        })
    }
}

impl JobExecutor for MediaJobExecutor {
    fn execute(
        &mut self,
        client: &mut Client,
        lease: &JobLease,
        worker_id: &str,
        cancellation: &ActiveJobCancellation,
    ) -> JobExecution {
        if let Some(reason) = cancellation.reason() {
            return JobExecution::Cancelled(reason);
        }

        let mut source = match prepare_claimed_source(
            client,
            lease,
            &self.media_root,
            self.max_source_bytes,
            cancellation,
        ) {
            Ok(source) => source,
            Err(error) => {
                if let Some(reason) = cancellation.reason() {
                    return JobExecution::Cancelled(reason);
                }
                return JobExecution::Failure {
                    class: error.class(),
                    message: error.to_string(),
                };
            }
        };

        if let Some(reason) = cancellation.reason() {
            return JobExecution::Cancelled(reason);
        }

        let processed = match source.media_type {
            MediaType::Image(_) => self
                .image_processor
                .process_image(&mut source, lease.post_id),
            MediaType::Video(_) => self.video_processor.process_video_cancellable(
                &mut source,
                lease.post_id,
                cancellation,
            ),
        };
        let processed = match processed {
            Ok(processed) => processed,
            Err(error) => {
                if let Some(reason) = cancellation.reason() {
                    return JobExecution::Cancelled(reason);
                }
                return JobExecution::Failure {
                    class: error.class(),
                    message: error.to_string(),
                };
            }
        };

        // Codec/filesystem work can finish after ownership changes. Deterministic
        // no-overwrite outputs remain safe for retry, but stale work must never reach
        // the authoritative generation-fenced database publication.
        if let Some(reason) = cancellation.reason() {
            return JobExecution::Cancelled(reason);
        }

        match publish_processed(client, lease, worker_id, &source.sha256, &processed) {
            Ok(PublishOutcome::Published) => JobExecution::Published,
            Ok(PublishOutcome::LeaseLostOrConflict) => JobExecution::OwnershipLost,
            Err(PublicationError::Database(error)) => JobExecution::Failure {
                class: FailureClass::Retryable,
                message: format!("publish processed media: {error}"),
            },
            Err(PublicationError::Invalid(message)) => JobExecution::Failure {
                class: FailureClass::Terminal,
                message,
            },
        }
    }
}
