use crate::image::encode_thumbnail_avif;
use crate::output::OutputStore;
use crate::processing::{
    processed_output_key, Cancellation, MediaKind, MediaType, NeverCancelled, OutputFormat,
    ProcessError, ProcessedMedia, VerifiedSource, VideoFormat, VideoProcessor,
};
use image::{GenericImageView, ImageFormat as CodecImageFormat};
use std::io::{self, Read};
use std::path::{Path, PathBuf};
use std::process::{Command, ExitStatus, Stdio};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

pub const VIDEO_PROCESSING_VERSION: u16 = 1;

const MAX_VIDEO_DIMENSION: i32 = 16_384;
const MAX_VIDEO_PIXELS: u64 = 80_000_000;
const PROBE_TIMEOUT: Duration = Duration::from_secs(10);
const THUMBNAIL_TIMEOUT: Duration = Duration::from_secs(20);
const COMMAND_POLL: Duration = Duration::from_millis(20);
const MAX_PROBE_STDOUT_BYTES: usize = 64 * 1024;
const MAX_PROBE_STDERR_BYTES: usize = 64 * 1024;
const MAX_THUMBNAIL_STDOUT_BYTES: usize = 4 * 1024 * 1024;
const THUMBNAIL_FRAME_BOUND: u32 = 512;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct VideoMetadata {
    pub output_format: OutputFormat,
    pub mime_type: &'static str,
    pub width: i32,
    pub height: i32,
    pub duration_ms: i64,
    pub rotation_degrees: i32,
    pub video_codec: String,
    pub pixel_format: String,
    pub audio_codecs: Vec<String>,
}

pub trait VideoProbe: Send + Sync {
    fn probe(
        &self,
        source: &VerifiedSource,
        cancellation: &dyn Cancellation,
    ) -> Result<VideoMetadata, ProcessError>;
}

pub trait VideoThumbnailer: Send + Sync {
    fn extract_png(
        &self,
        source_path: &Path,
        cancellation: &dyn Cancellation,
    ) -> Result<Vec<u8>, ProcessError>;
}

#[derive(Debug, Clone)]
pub struct BoundedFfprobe {
    program: PathBuf,
    timeout: Duration,
}

impl Default for BoundedFfprobe {
    fn default() -> Self {
        Self {
            program: PathBuf::from("ffprobe"),
            timeout: PROBE_TIMEOUT,
        }
    }
}

impl BoundedFfprobe {
    pub fn new(program: impl Into<PathBuf>) -> Self {
        Self {
            program: program.into(),
            timeout: PROBE_TIMEOUT,
        }
    }

    pub fn timeout(&self) -> Duration {
        self.timeout
    }
}

impl VideoProbe for BoundedFfprobe {
    fn probe(
        &self,
        source: &VerifiedSource,
        cancellation: &dyn Cancellation,
    ) -> Result<VideoMetadata, ProcessError> {
        if cancellation.is_cancelled() {
            return Err(ProcessError::retryable("video probe cancelled"));
        }
        if !matches!(source.media_type, MediaType::Video(_)) {
            return Err(ProcessError::terminal(
                "video probe requires a verified video source",
            ));
        }

        let mut command = Command::new(&self.program);
        command
            .arg("-v")
            .arg("error")
            .arg("-show_entries")
            .arg("stream=codec_type,codec_name,width,height,pix_fmt,sample_aspect_ratio:stream_tags=rotate:stream_side_data=rotation:format=format_name,duration")
            .arg("-of")
            .arg("compact=p=0:nk=0")
            .arg(&source.canonical_path);

        let output = run_bounded_command(
            &mut command,
            self.timeout,
            cancellation,
            "video probe",
            MAX_PROBE_STDOUT_BYTES,
            MAX_PROBE_STDERR_BYTES,
        )?;
        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr);
            return Err(ProcessError::terminal(format!(
                "ffprobe rejected verified video source: {}",
                stderr.trim()
            )));
        }

        let stdout = std::str::from_utf8(&output.stdout)
            .map_err(|_| ProcessError::terminal("ffprobe output is not valid UTF-8"))?;
        classify_probe(source.media_type, stdout)
    }
}

#[derive(Debug, Clone)]
pub struct BoundedFfmpegThumbnailer {
    program: PathBuf,
    timeout: Duration,
}

impl Default for BoundedFfmpegThumbnailer {
    fn default() -> Self {
        Self {
            program: PathBuf::from("ffmpeg"),
            timeout: THUMBNAIL_TIMEOUT,
        }
    }
}

impl BoundedFfmpegThumbnailer {
    pub fn new(program: impl Into<PathBuf>) -> Self {
        Self {
            program: program.into(),
            timeout: THUMBNAIL_TIMEOUT,
        }
    }
}

impl VideoThumbnailer for BoundedFfmpegThumbnailer {
    fn extract_png(
        &self,
        source_path: &Path,
        cancellation: &dyn Cancellation,
    ) -> Result<Vec<u8>, ProcessError> {
        if cancellation.is_cancelled() {
            return Err(ProcessError::retryable(
                "video thumbnail extraction cancelled",
            ));
        }
        let mut command = Command::new(&self.program);
        command
            .arg("-v")
            .arg("error")
            .arg("-nostdin")
            .arg("-filter_threads")
            .arg("1")
            .arg("-threads")
            .arg("1")
            .arg("-i")
            .arg(source_path)
            .arg("-map")
            .arg("0:v:0")
            .arg("-an")
            .arg("-sn")
            .arg("-dn")
            .arg("-frames:v")
            .arg("1")
            .arg("-vf")
            .arg(format!(
                "scale={THUMBNAIL_FRAME_BOUND}:{THUMBNAIL_FRAME_BOUND}:force_original_aspect_ratio=decrease"
            ))
            .arg("-threads")
            .arg("1")
            .arg("-f")
            .arg("image2pipe")
            .arg("-vcodec")
            .arg("png")
            .arg("pipe:1");

        let output = run_bounded_command(
            &mut command,
            self.timeout,
            cancellation,
            "video thumbnail extraction",
            MAX_THUMBNAIL_STDOUT_BYTES,
            MAX_PROBE_STDERR_BYTES,
        )?;
        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr);
            return Err(ProcessError::terminal(format!(
                "ffmpeg could not extract video thumbnail: {}",
                stderr.trim()
            )));
        }
        if output.stdout.is_empty() {
            return Err(ProcessError::terminal(
                "ffmpeg produced no video thumbnail frame",
            ));
        }
        Ok(output.stdout)
    }
}

pub struct BoundedVideoProcessor {
    outputs: OutputStore,
    probe: Box<dyn VideoProbe>,
    thumbnailer: Box<dyn VideoThumbnailer>,
}

impl BoundedVideoProcessor {
    pub fn new(outputs: OutputStore) -> Self {
        Self::with_components(
            outputs,
            Box::<BoundedFfprobe>::default(),
            Box::<BoundedFfmpegThumbnailer>::default(),
        )
    }

    pub fn with_components(
        outputs: OutputStore,
        probe: Box<dyn VideoProbe>,
        thumbnailer: Box<dyn VideoThumbnailer>,
    ) -> Self {
        Self {
            outputs,
            probe,
            thumbnailer,
        }
    }

    pub fn process_video_cancellable(
        &mut self,
        source: &mut VerifiedSource,
        post_id: i64,
        cancellation: &dyn Cancellation,
    ) -> Result<ProcessedMedia, ProcessError> {
        if cancellation.is_cancelled() {
            return Err(ProcessError::retryable("video processing cancelled"));
        }
        let metadata = self.probe.probe(source, cancellation)?;
        if metadata.output_format != OutputFormat::Mp4 || metadata.mime_type != "video/mp4" {
            return Err(ProcessError::terminal(
                "video processor received non-MP4 passthrough metadata",
            ));
        }

        let main_key = processed_output_key(post_id, &source.sha256, OutputFormat::Mp4)?;
        let thumbnail_key = video_thumbnail_output_key(&main_key)?;
        let published = self.outputs.publish_file(
            &main_key,
            &mut source.file,
            source.byte_size,
            &source.sha256,
            cancellation,
        )?;
        if cancellation.is_cancelled() {
            return Err(ProcessError::retryable("video processing cancelled"));
        }

        // Thumbnail from the durable canonical object, not the mutable ingestion path.
        // FFmpeg autorotation is left enabled, so display-matrix rotation is applied
        // before the bounded scale filter. The decoded Rust-side frame is <=512x512.
        let canonical_path = self.outputs.root().join(&main_key);
        let frame_png = self
            .thumbnailer
            .extract_png(&canonical_path, cancellation)?;
        if cancellation.is_cancelled() {
            return Err(ProcessError::retryable("video processing cancelled"));
        }
        let frame = image::load_from_memory_with_format(&frame_png, CodecImageFormat::Png)
            .map_err(|error| {
                ProcessError::terminal(format!("decode extracted video thumbnail PNG: {error}"))
            })?;
        let (frame_width, frame_height) = frame.dimensions();
        if frame_width == 0
            || frame_height == 0
            || frame_width > THUMBNAIL_FRAME_BOUND
            || frame_height > THUMBNAIL_FRAME_BOUND
        {
            return Err(ProcessError::terminal(format!(
                "video thumbnail frame dimensions {frame_width}x{frame_height} exceed bounded extraction contract"
            )));
        }
        let thumbnail_avif = encode_thumbnail_avif(&frame)?;
        if cancellation.is_cancelled() {
            return Err(ProcessError::retryable("video processing cancelled"));
        }
        self.outputs
            .publish_bytes(&thumbnail_key, &thumbnail_avif)?;

        let byte_size = i64::try_from(published.byte_size).map_err(|_| {
            ProcessError::terminal("canonical MP4 byte size does not fit database bigint")
        })?;
        Ok(ProcessedMedia {
            kind: MediaKind::Video,
            storage_key: main_key,
            mime_type: metadata.mime_type.to_owned(),
            width: metadata.width,
            height: metadata.height,
            duration_ms: metadata.duration_ms,
            byte_size,
            sha256: published.sha256,
            perceptual_hash: None,
        })
    }
}

impl VideoProcessor for BoundedVideoProcessor {
    fn process_video(
        &mut self,
        source: &mut VerifiedSource,
        post_id: i64,
    ) -> Result<ProcessedMedia, ProcessError> {
        self.process_video_cancellable(source, post_id, &NeverCancelled)
    }
}

pub fn video_thumbnail_output_key(main_storage_key: &str) -> Result<String, ProcessError> {
    let stem = main_storage_key
        .strip_suffix(".mp4")
        .ok_or_else(|| ProcessError::terminal("canonical video output key must end in .mp4"))?;
    Ok(format!("{stem}.thumb.avif"))
}

#[derive(Debug)]
struct CommandOutput {
    status: ExitStatus,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

#[derive(Debug)]
struct BoundedCapture {
    bytes: Vec<u8>,
    exceeded: bool,
}

fn spawn_bounded_capture<R>(
    mut stream: R,
    max_bytes: usize,
) -> JoinHandle<io::Result<BoundedCapture>>
where
    R: Read + Send + 'static,
{
    thread::spawn(move || {
        let mut captured = Vec::with_capacity(max_bytes.min(64 * 1024));
        let mut exceeded = false;
        let mut buffer = [0u8; 8192];
        loop {
            let read = stream.read(&mut buffer)?;
            if read == 0 {
                break;
            }
            let remaining = max_bytes.saturating_sub(captured.len());
            let keep = remaining.min(read);
            captured.extend_from_slice(&buffer[..keep]);
            if keep < read {
                exceeded = true;
            }
        }
        Ok(BoundedCapture {
            bytes: captured,
            exceeded,
        })
    })
}

fn join_capture(
    handle: JoinHandle<io::Result<BoundedCapture>>,
    context: &str,
) -> Result<BoundedCapture, ProcessError> {
    handle
        .join()
        .map_err(|_| ProcessError::retryable(format!("{context} capture thread panicked")))?
        .map_err(|error| ProcessError::retryable(format!("read {context}: {error}")))
}

fn run_bounded_command(
    command: &mut Command,
    timeout: Duration,
    cancellation: &dyn Cancellation,
    context: &str,
    stdout_limit: usize,
    stderr_limit: usize,
) -> Result<CommandOutput, ProcessError> {
    if timeout.is_zero() || stdout_limit == 0 || stderr_limit == 0 {
        return Err(ProcessError::terminal(
            "video command limits must be positive",
        ));
    }
    if cancellation.is_cancelled() {
        return Err(ProcessError::retryable(format!("{context} cancelled")));
    }

    command
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let mut child = command
        .spawn()
        .map_err(|error| ProcessError::retryable(format!("spawn {context}: {error}")))?;
    let stdout = child
        .stdout
        .take()
        .ok_or_else(|| ProcessError::retryable(format!("capture {context} stdout")))?;
    let stderr = child
        .stderr
        .take()
        .ok_or_else(|| ProcessError::retryable(format!("capture {context} stderr")))?;
    let stdout_capture = spawn_bounded_capture(stdout, stdout_limit);
    let stderr_capture = spawn_bounded_capture(stderr, stderr_limit);
    let started = Instant::now();

    let mut cancelled = false;
    let mut timed_out = false;
    let status = loop {
        match child.try_wait() {
            Ok(Some(status)) => break status,
            Ok(None) => {}
            Err(error) => {
                let _ = child.kill();
                let _ = child.wait();
                let _ = join_capture(stdout_capture, &format!("{context} stdout"));
                let _ = join_capture(stderr_capture, &format!("{context} stderr"));
                return Err(ProcessError::retryable(format!("poll {context}: {error}")));
            }
        }

        if cancellation.is_cancelled() {
            cancelled = true;
        } else if started.elapsed() >= timeout {
            timed_out = true;
        }
        if cancelled || timed_out {
            let kill_error = child.kill().err();
            let wait_result = child.wait();
            let stdout = join_capture(stdout_capture, &format!("{context} stdout"));
            let stderr = join_capture(stderr_capture, &format!("{context} stderr"));
            if let Some(error) = kill_error {
                if wait_result.is_err() {
                    return Err(ProcessError::retryable(format!(
                        "terminate {context}: {error}"
                    )));
                }
            }
            wait_result.map_err(|error| {
                ProcessError::retryable(format!("reap terminated {context}: {error}"))
            })?;
            stdout?;
            stderr?;
            if cancelled {
                return Err(ProcessError::retryable(format!("{context} cancelled")));
            }
            return Err(ProcessError::terminal(format!(
                "{context} timed out after {} ms",
                timeout.as_millis()
            )));
        }
        thread::sleep(COMMAND_POLL);
    };

    let stdout = join_capture(stdout_capture, &format!("{context} stdout"))?;
    let stderr = join_capture(stderr_capture, &format!("{context} stderr"))?;
    if stdout.exceeded {
        return Err(ProcessError::terminal(format!(
            "{context} stdout exceeded {stdout_limit} byte limit"
        )));
    }
    if stderr.exceeded {
        return Err(ProcessError::terminal(format!(
            "{context} stderr exceeded {stderr_limit} byte limit"
        )));
    }
    Ok(CommandOutput {
        status,
        stdout: stdout.bytes,
        stderr: stderr.bytes,
    })
}

#[derive(Debug, Default)]
struct ParsedProbe {
    video_streams: Vec<ParsedStream>,
    audio_codecs: Vec<Option<String>>,
    format_name: Option<String>,
    duration_seconds: Option<String>,
}

#[derive(Debug, Default)]
struct ParsedStream {
    codec_name: Option<String>,
    pixel_format: Option<String>,
    sample_aspect_ratio: Option<String>,
    width: Option<i32>,
    height: Option<i32>,
    rotation_values: Vec<String>,
}

fn classify_probe(media_type: MediaType, output: &str) -> Result<VideoMetadata, ProcessError> {
    match media_type {
        MediaType::Video(VideoFormat::Mp4) => classify_mp4(parse_probe_output(output)?),
        MediaType::Video(VideoFormat::Ebml) => Err(ProcessError::terminal(
            "EBML/WebM input is recognized but not supported by video processing version 1",
        )),
        MediaType::Image(_) => Err(ProcessError::terminal(
            "video probe cannot classify an image source",
        )),
    }
}

fn parse_probe_output(output: &str) -> Result<ParsedProbe, ProcessError> {
    let mut parsed = ParsedProbe::default();

    for line in output
        .lines()
        .map(str::trim)
        .filter(|line| !line.is_empty())
    {
        let mut codec_type = None;
        let mut codec_name = None;
        let mut pixel_format = None;
        let mut sample_aspect_ratio = None;
        let mut width = None;
        let mut height = None;
        let mut duration = None;
        let mut format_name = None;
        let mut rotation_values = Vec::new();

        for field in line.split('|') {
            let Some((key, value)) = field.split_once('=') else {
                return Err(ProcessError::terminal(format!(
                    "ffprobe field has invalid compact syntax: {field}"
                )));
            };
            let lower_key = key.to_ascii_lowercase();
            match key {
                "codec_type" => codec_type = Some(value),
                "codec_name" => codec_name = Some(value.to_owned()),
                "pix_fmt" => pixel_format = Some(value.to_owned()),
                "sample_aspect_ratio" => sample_aspect_ratio = Some(value.to_owned()),
                "width" => width = Some(parse_i32_field("width", value)?),
                "height" => height = Some(parse_i32_field("height", value)?),
                "duration" => duration = Some(value.to_owned()),
                "format_name" => format_name = Some(value.to_owned()),
                _ if lower_key == "rotation"
                    || lower_key.ends_with(":rotation")
                    || lower_key.ends_with(":rotate") =>
                {
                    rotation_values.push(value.to_owned())
                }
                _ => {}
            }
        }

        match codec_type {
            Some("video") => parsed.video_streams.push(ParsedStream {
                codec_name,
                pixel_format,
                sample_aspect_ratio,
                width,
                height,
                rotation_values,
            }),
            Some("audio") => parsed.audio_codecs.push(codec_name),
            Some(_) => {}
            None => {
                if let Some(format_name) = format_name {
                    if parsed.format_name.replace(format_name).is_some() {
                        return Err(ProcessError::terminal(
                            "ffprobe returned multiple format names",
                        ));
                    }
                }
                if let Some(duration) = duration {
                    if parsed.duration_seconds.replace(duration).is_some() {
                        return Err(ProcessError::terminal(
                            "ffprobe returned multiple format durations",
                        ));
                    }
                }
            }
        }
    }

    Ok(parsed)
}

fn parse_i32_field(name: &str, value: &str) -> Result<i32, ProcessError> {
    value
        .parse::<i32>()
        .map_err(|_| ProcessError::terminal(format!("ffprobe {name} is invalid: {value}")))
}

fn classify_mp4(mut parsed: ParsedProbe) -> Result<VideoMetadata, ProcessError> {
    let format_name = parsed
        .format_name
        .as_deref()
        .ok_or_else(|| ProcessError::terminal("ffprobe did not report container format"))?;
    if !format_name.split(',').any(|name| name == "mp4") {
        return Err(ProcessError::terminal(format!(
            "container format {format_name} is not accepted as MP4 passthrough"
        )));
    }
    if parsed.video_streams.len() != 1 {
        return Err(ProcessError::terminal(format!(
            "MP4 passthrough requires exactly one video stream, found {}",
            parsed.video_streams.len()
        )));
    }
    let video = parsed.video_streams.pop().expect("length checked above");
    let video_codec = video
        .codec_name
        .ok_or_else(|| ProcessError::terminal("ffprobe did not report video codec"))?;
    if video_codec != "h264" {
        return Err(ProcessError::terminal(format!(
            "MP4 video codec {video_codec} requires transcoding; video processing version 1 accepts H.264 passthrough only"
        )));
    }

    let pixel_format = video
        .pixel_format
        .ok_or_else(|| ProcessError::terminal("ffprobe did not report video pixel format"))?;
    if !matches!(pixel_format.as_str(), "yuv420p" | "yuvj420p") {
        return Err(ProcessError::terminal(format!(
            "H.264 pixel format {pixel_format} requires transcoding; video processing version 1 accepts 8-bit 4:2:0 passthrough only"
        )));
    }
    let sample_aspect_ratio = video
        .sample_aspect_ratio
        .ok_or_else(|| ProcessError::terminal("ffprobe did not report sample aspect ratio"))?;
    if sample_aspect_ratio != "1:1" {
        return Err(ProcessError::terminal(format!(
            "sample aspect ratio {sample_aspect_ratio} requires explicit display-geometry handling; video processing version 1 accepts square pixels only"
        )));
    }

    let coded_width = video
        .width
        .ok_or_else(|| ProcessError::terminal("ffprobe did not report video width"))?;
    let coded_height = video
        .height
        .ok_or_else(|| ProcessError::terminal("ffprobe did not report video height"))?;
    validate_dimensions(coded_width, coded_height)?;
    let rotation_degrees = parse_rotation_values(&video.rotation_values)?;
    let (width, height) = if matches!(rotation_degrees, 90 | 270) {
        (coded_height, coded_width)
    } else {
        (coded_width, coded_height)
    };
    validate_dimensions(width, height)?;

    let mut audio_codecs = Vec::with_capacity(parsed.audio_codecs.len());
    for codec in parsed.audio_codecs {
        let codec =
            codec.ok_or_else(|| ProcessError::terminal("ffprobe did not report audio codec"))?;
        if codec != "aac" {
            return Err(ProcessError::terminal(format!(
                "MP4 audio codec {codec} requires transcoding; video processing version 1 accepts AAC or no audio"
            )));
        }
        audio_codecs.push(codec);
    }

    let duration = parsed
        .duration_seconds
        .ok_or_else(|| ProcessError::terminal("ffprobe did not report video duration"))?;
    let duration_ms = parse_duration_ms(&duration)?;

    Ok(VideoMetadata {
        output_format: OutputFormat::Mp4,
        mime_type: "video/mp4",
        width,
        height,
        duration_ms,
        rotation_degrees,
        video_codec,
        pixel_format,
        audio_codecs,
    })
}

fn parse_rotation_values(values: &[String]) -> Result<i32, ProcessError> {
    let mut rotation = None;
    for value in values {
        let degrees = value
            .parse::<f64>()
            .map_err(|_| ProcessError::terminal(format!("ffprobe rotation is invalid: {value}")))?;
        if !degrees.is_finite() {
            return Err(ProcessError::terminal(format!(
                "ffprobe rotation is not finite: {value}"
            )));
        }
        let quarter_turns = (degrees / 90.0).round();
        let snapped = quarter_turns * 90.0;
        if (degrees - snapped).abs() > 0.01 {
            return Err(ProcessError::terminal(format!(
                "video rotation {value} is not an orthogonal display transform"
            )));
        }
        let normalized = ((snapped as i32 % 360) + 360) % 360;
        match rotation {
            Some(existing) if existing != normalized => {
                return Err(ProcessError::terminal(
                    "ffprobe returned conflicting video rotation metadata",
                ))
            }
            _ => rotation = Some(normalized),
        }
    }
    Ok(rotation.unwrap_or(0))
}

fn validate_dimensions(width: i32, height: i32) -> Result<(), ProcessError> {
    if width <= 0 || height <= 0 {
        return Err(ProcessError::terminal("video dimensions must be positive"));
    }
    if width > MAX_VIDEO_DIMENSION || height > MAX_VIDEO_DIMENSION {
        return Err(ProcessError::terminal(format!(
            "video dimensions {width}x{height} exceed {MAX_VIDEO_DIMENSION}px limit"
        )));
    }
    let pixels = u64::try_from(width)
        .expect("positive width validated above")
        .saturating_mul(u64::try_from(height).expect("positive height validated above"));
    if pixels > MAX_VIDEO_PIXELS {
        return Err(ProcessError::terminal(format!(
            "video pixel count {pixels} exceeds {MAX_VIDEO_PIXELS} limit"
        )));
    }
    Ok(())
}

fn parse_duration_ms(value: &str) -> Result<i64, ProcessError> {
    let seconds = value
        .parse::<f64>()
        .map_err(|_| ProcessError::terminal(format!("ffprobe duration is invalid: {value}")))?;
    if !seconds.is_finite() || seconds <= 0.0 {
        return Err(ProcessError::terminal(format!(
            "ffprobe duration must be finite and positive: {value}"
        )));
    }
    let millis = seconds * 1000.0;
    if millis > i64::MAX as f64 {
        return Err(ProcessError::terminal(
            "video duration exceeds database range",
        ));
    }
    Ok(millis.round() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::processing::{FailureClass, MediaRoot, SourceRecord};
    use sha2::{Digest, Sha256};
    use std::fs;
    use std::path::PathBuf;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::Arc;
    use std::time::{SystemTime, UNIX_EPOCH};

    const VALID_MP4: &str = "codec_name=h264|codec_type=video|width=1920|height=1080|pix_fmt=yuv420p|sample_aspect_ratio=1:1\ncodec_name=aac|codec_type=audio\nformat_name=mov,mp4,m4a,3gp,3g2,mj2|duration=12.345000\n";

    struct TempRoot(PathBuf);

    impl TempRoot {
        fn new() -> Self {
            let nonce = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .expect("clock")
                .as_nanos();
            let path = std::env::temp_dir().join(format!(
                "ginbar-video-processor-{}-{nonce}",
                std::process::id()
            ));
            fs::create_dir_all(path.join("sources/01")).unwrap();
            Self(path)
        }
    }

    impl Drop for TempRoot {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    fn verified_mp4(root: &TempRoot, body: &[u8]) -> VerifiedSource {
        let key = "sources/01/0123456789abcdef0123456789abcdef";
        fs::write(root.0.join(key), body).unwrap();
        let sha256: [u8; 32] = Sha256::digest(body).into();
        let source = SourceRecord {
            post_id: 513,
            source_type: 0,
            storage_key: key.to_owned(),
            declared_mime_type: "video/mp4".to_owned(),
            byte_size: body.len() as i64,
            sha256,
        };
        MediaRoot::new(&root.0)
            .unwrap()
            .open_verified(&source, 1024 * 1024, &NeverCancelled)
            .unwrap()
    }

    #[test]
    fn accepts_browser_compatible_mp4_passthrough() {
        let metadata = classify_probe(MediaType::Video(VideoFormat::Mp4), VALID_MP4).unwrap();
        assert_eq!(metadata.output_format, OutputFormat::Mp4);
        assert_eq!(metadata.mime_type, "video/mp4");
        assert_eq!((metadata.width, metadata.height), (1920, 1080));
        assert_eq!(metadata.duration_ms, 12_345);
        assert_eq!(metadata.rotation_degrees, 0);
        assert_eq!(metadata.video_codec, "h264");
        assert_eq!(metadata.pixel_format, "yuv420p");
        assert_eq!(metadata.audio_codecs, vec!["aac"]);
    }

    #[test]
    fn accepts_rotated_square_pixel_mp4_and_reports_display_dimensions() {
        let probe = "codec_name=h264|codec_type=video|width=1920|height=1080|pix_fmt=yuv420p|sample_aspect_ratio=1:1|side_datum/display_matrix:rotation=-90\nformat_name=mov,mp4,m4a,3gp,3g2,mj2|duration=1.0\n";
        let metadata = classify_probe(MediaType::Video(VideoFormat::Mp4), probe).unwrap();
        assert_eq!((metadata.width, metadata.height), (1080, 1920));
        assert_eq!(metadata.rotation_degrees, 270);
    }

    #[test]
    fn rejects_non_square_pixels_and_non_orthogonal_rotation() {
        for probe in [
            "codec_name=h264|codec_type=video|width=720|height=576|pix_fmt=yuv420p|sample_aspect_ratio=16:15\nformat_name=mov,mp4|duration=1.0\n",
            "codec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p|sample_aspect_ratio=1:1|rotation=45\nformat_name=mov,mp4|duration=1.0\n",
        ] {
            assert_eq!(
                classify_probe(MediaType::Video(VideoFormat::Mp4), probe)
                    .unwrap_err()
                    .class(),
                FailureClass::Terminal
            );
        }
    }

    #[test]
    fn accepts_mp4_without_audio() {
        let metadata = classify_probe(
            MediaType::Video(VideoFormat::Mp4),
            "codec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p|sample_aspect_ratio=1:1\nformat_name=mov,mp4|duration=0.250000\n",
        )
        .unwrap();
        assert!(metadata.audio_codecs.is_empty());
        assert_eq!(metadata.duration_ms, 250);
    }

    #[test]
    fn rejects_codecs_that_need_transcoding() {
        for probe in [
            "codec_name=hevc|codec_type=video|width=1920|height=1080|pix_fmt=yuv420p|sample_aspect_ratio=1:1\nformat_name=mov,mp4|duration=1.0\n",
            "codec_name=h264|codec_type=video|width=1920|height=1080|pix_fmt=yuv420p10le|sample_aspect_ratio=1:1\nformat_name=mov,mp4|duration=1.0\n",
            "codec_name=h264|codec_type=video|width=1920|height=1080|pix_fmt=yuv420p|sample_aspect_ratio=1:1\ncodec_name=ac3|codec_type=audio\nformat_name=mov,mp4|duration=1.0\n",
        ] {
            let error = classify_probe(MediaType::Video(VideoFormat::Mp4), probe).unwrap_err();
            assert_eq!(error.class(), FailureClass::Terminal);
            assert!(error.to_string().contains("requires transcoding"));
        }
    }

    #[test]
    fn rejects_multiple_or_missing_video_streams() {
        let multiple = "codec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p|sample_aspect_ratio=1:1\ncodec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p|sample_aspect_ratio=1:1\nformat_name=mov,mp4|duration=1.0\n";
        let missing = "codec_name=aac|codec_type=audio\nformat_name=mov,mp4|duration=1.0\n";
        for probe in [multiple, missing] {
            let error = classify_probe(MediaType::Video(VideoFormat::Mp4), probe).unwrap_err();
            assert_eq!(error.class(), FailureClass::Terminal);
            assert!(error.to_string().contains("exactly one video stream"));
        }
    }

    #[test]
    fn rejects_invalid_dimensions_or_duration() {
        for probe in [
            "codec_name=h264|codec_type=video|width=0|height=360|pix_fmt=yuv420p|sample_aspect_ratio=1:1\nformat_name=mov,mp4|duration=1.0\n",
            "codec_name=h264|codec_type=video|width=16385|height=1080|pix_fmt=yuv420p|sample_aspect_ratio=1:1\nformat_name=mov,mp4|duration=1.0\n",
            "codec_name=h264|codec_type=video|width=12000|height=12000|pix_fmt=yuv420p|sample_aspect_ratio=1:1\nformat_name=mov,mp4|duration=1.0\n",
            "codec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p|sample_aspect_ratio=1:1\nformat_name=mov,mp4|duration=0\n",
            "codec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p|sample_aspect_ratio=1:1\nformat_name=mov,mp4|duration=N/A\n",
        ] {
            assert_eq!(
                classify_probe(MediaType::Video(VideoFormat::Mp4), probe)
                    .unwrap_err()
                    .class(),
                FailureClass::Terminal
            );
        }
    }

    #[test]
    fn rejects_non_mp4_container_name_and_ebml() {
        let wrong = VALID_MP4.replace(
            "format_name=mov,mp4,m4a,3gp,3g2,mj2",
            "format_name=matroska,webm",
        );
        assert!(classify_probe(MediaType::Video(VideoFormat::Mp4), &wrong).is_err());
        assert!(classify_probe(MediaType::Video(VideoFormat::Ebml), VALID_MP4).is_err());
    }

    #[cfg(unix)]
    fn executable_script(body: &str) -> (TempRoot, PathBuf) {
        use std::os::unix::fs::PermissionsExt;
        let root = TempRoot::new();
        let path = root.0.join("tool.sh");
        fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
        let mut permissions = fs::metadata(&path).unwrap().permissions();
        permissions.set_mode(0o700);
        fs::set_permissions(&path, permissions).unwrap();
        (root, path)
    }

    #[cfg(unix)]
    #[test]
    fn bounded_probe_times_out_and_reaps_child() {
        let (_root, program) = executable_script("while :; do :; done");
        let probe = BoundedFfprobe {
            program,
            timeout: Duration::from_millis(50),
        };
        let temp = TempRoot::new();
        let mut source = verified_mp4(&temp, b"\0\0\0\x18ftypisom0000mp42body");
        source.media_type = MediaType::Video(VideoFormat::Mp4);
        let error = probe.probe(&source, &NeverCancelled).unwrap_err();
        assert_eq!(error.class(), FailureClass::Terminal);
        assert!(error.to_string().contains("timed out"));
    }

    #[cfg(unix)]
    #[test]
    fn bounded_probe_observes_cancellation_and_reaps_child() {
        let (_root, program) = executable_script("while :; do :; done");
        let probe = BoundedFfprobe {
            program,
            timeout: Duration::from_secs(2),
        };
        let temp = TempRoot::new();
        let mut source = verified_mp4(&temp, b"\0\0\0\x18ftypisom0000mp42body");
        source.media_type = MediaType::Video(VideoFormat::Mp4);
        let cancelled = Arc::new(AtomicBool::new(false));
        let trigger = Arc::clone(&cancelled);
        let handle = thread::spawn(move || {
            thread::sleep(Duration::from_millis(50));
            trigger.store(true, Ordering::Release);
        });
        let cancellation = || cancelled.load(Ordering::Acquire);
        let error = probe.probe(&source, &cancellation).unwrap_err();
        handle.join().unwrap();
        assert_eq!(error.class(), FailureClass::Retryable);
        assert!(error.to_string().contains("cancelled"));
    }

    #[test]
    fn video_thumbnail_key_uses_canonical_identity() {
        let main =
            "media/01/513/v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.mp4";
        assert_eq!(
            video_thumbnail_output_key(main).unwrap(),
            "media/01/513/v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.thumb.avif"
        );
    }

    struct StaticProbe(VideoMetadata);

    impl VideoProbe for StaticProbe {
        fn probe(
            &self,
            _source: &VerifiedSource,
            _cancellation: &dyn Cancellation,
        ) -> Result<VideoMetadata, ProcessError> {
            Ok(self.0.clone())
        }
    }

    struct StaticThumbnailer(Vec<u8>);

    impl VideoThumbnailer for StaticThumbnailer {
        fn extract_png(
            &self,
            _source_path: &Path,
            _cancellation: &dyn Cancellation,
        ) -> Result<Vec<u8>, ProcessError> {
            Ok(self.0.clone())
        }
    }

    fn png_frame() -> Vec<u8> {
        use image::{ExtendedColorType, ImageEncoder};
        let pixels = vec![128u8; 32 * 24 * 4];
        let mut encoded = Vec::new();
        image::codecs::png::PngEncoder::new(&mut encoded)
            .write_image(&pixels, 32, 24, ExtendedColorType::Rgba8)
            .unwrap();
        encoded
    }

    #[test]
    fn zero_transcode_processor_publishes_exact_mp4_and_thumbnail_idempotently() {
        let root = TempRoot::new();
        let body = b"\0\0\0\x18ftypisom0000mp42canonical-video-bytes";
        let mut source = verified_mp4(&root, body);
        source.media_type = MediaType::Video(VideoFormat::Mp4);
        let metadata = classify_probe(MediaType::Video(VideoFormat::Mp4), VALID_MP4).unwrap();
        let outputs = OutputStore::new(&root.0).unwrap();
        let mut processor = BoundedVideoProcessor::with_components(
            outputs,
            Box::new(StaticProbe(metadata)),
            Box::new(StaticThumbnailer(png_frame())),
        );

        let first = processor
            .process_video_cancellable(&mut source, 513, &NeverCancelled)
            .unwrap();
        let second = processor
            .process_video_cancellable(&mut source, 513, &NeverCancelled)
            .unwrap();
        assert_eq!(first, second);
        assert_eq!(first.kind, MediaKind::Video);
        assert_eq!(first.mime_type, "video/mp4");
        assert_eq!(first.byte_size, body.len() as i64);
        let expected_sha: [u8; 32] = Sha256::digest(body).into();
        assert_eq!(first.sha256, expected_sha);
        assert_eq!(fs::read(root.0.join(&first.storage_key)).unwrap(), body);
        let thumb = fs::read(
            root.0
                .join(video_thumbnail_output_key(&first.storage_key).unwrap()),
        )
        .unwrap();
        assert_eq!(&thumb[4..8], b"ftyp");
        assert_eq!(&thumb[8..12], b"avif");
    }

    #[test]
    fn video_processor_collision_is_terminal_and_preserves_existing_bytes() {
        let root = TempRoot::new();
        let body = b"\0\0\0\x18ftypisom0000mp42canonical-video-bytes";
        let mut source = verified_mp4(&root, body);
        source.media_type = MediaType::Video(VideoFormat::Mp4);
        let key = processed_output_key(513, &source.sha256, OutputFormat::Mp4).unwrap();
        let path = root.0.join(&key);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(&path, vec![0x44; body.len()]).unwrap();
        let metadata = classify_probe(MediaType::Video(VideoFormat::Mp4), VALID_MP4).unwrap();
        let mut processor = BoundedVideoProcessor::with_components(
            OutputStore::new(&root.0).unwrap(),
            Box::new(StaticProbe(metadata)),
            Box::new(StaticThumbnailer(png_frame())),
        );
        let error = processor
            .process_video_cancellable(&mut source, 513, &NeverCancelled)
            .unwrap_err();
        assert_eq!(error.class(), FailureClass::Terminal);
        assert_eq!(fs::read(path).unwrap(), vec![0x44; body.len()]);
    }

    #[test]
    fn processing_version_is_explicit() {
        assert_eq!(VIDEO_PROCESSING_VERSION, 1);
    }
}
