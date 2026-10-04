use crate::processing::{
    Cancellation, MediaType, OutputFormat, ProcessError, VerifiedSource, VideoFormat,
};
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Seek, SeekFrom};
use std::path::PathBuf;
use std::process::{Command, ExitStatus, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::thread;
use std::time::{Duration, Instant};

pub const VIDEO_PROCESSING_VERSION: u16 = 1;

const MAX_VIDEO_DIMENSION: i32 = 16_384;
const MAX_VIDEO_PIXELS: u64 = 80_000_000;
const PROBE_TIMEOUT: Duration = Duration::from_secs(10);
const COMMAND_POLL: Duration = Duration::from_millis(20);
const MAX_PROBE_STDOUT_BYTES: u64 = 64 * 1024;
const MAX_PROBE_STDERR_BYTES: u64 = 64 * 1024;
static CAPTURE_SEQUENCE: AtomicU64 = AtomicU64::new(0);

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct VideoMetadata {
    pub output_format: OutputFormat,
    pub mime_type: &'static str,
    pub width: i32,
    pub height: i32,
    pub duration_ms: i64,
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
            .arg("stream=codec_type,codec_name,width,height,pix_fmt:format=duration")
            .arg("-of")
            .arg("compact=p=0:nk=0")
            .arg(&source.canonical_path);

        let output = run_bounded_command(&mut command, self.timeout, cancellation)?;
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

#[derive(Debug)]
struct CommandOutput {
    status: ExitStatus,
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

fn run_bounded_command(
    command: &mut Command,
    timeout: Duration,
    cancellation: &dyn Cancellation,
) -> Result<CommandOutput, ProcessError> {
    if timeout.is_zero() {
        return Err(ProcessError::terminal(
            "video command timeout must be positive",
        ));
    }
    if cancellation.is_cancelled() {
        return Err(ProcessError::retryable("video command cancelled"));
    }

    let mut stdout = CaptureFile::new("ginbar-video-stdout")?;
    let mut stderr = CaptureFile::new("ginbar-video-stderr")?;
    let stdout_child = stdout
        .file
        .try_clone()
        .map_err(|error| ProcessError::retryable(format!("clone video stdout capture: {error}")))?;
    let stderr_child = stderr
        .file
        .try_clone()
        .map_err(|error| ProcessError::retryable(format!("clone video stderr capture: {error}")))?;

    command
        .stdin(Stdio::null())
        .stdout(Stdio::from(stdout_child))
        .stderr(Stdio::from(stderr_child));
    let mut child = command
        .spawn()
        .map_err(|error| ProcessError::retryable(format!("spawn video probe tool: {error}")))?;
    let started = Instant::now();

    let status = loop {
        match child.try_wait() {
            Ok(Some(status)) => break status,
            Ok(None) => {}
            Err(error) => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(ProcessError::retryable(format!(
                    "poll video probe tool: {error}"
                )));
            }
        }

        if cancellation.is_cancelled() {
            child.kill().map_err(|error| {
                ProcessError::retryable(format!("cancel video probe tool: {error}"))
            })?;
            child.wait().map_err(|error| {
                ProcessError::retryable(format!("reap cancelled video probe tool: {error}"))
            })?;
            return Err(ProcessError::retryable("video probe cancelled"));
        }
        if started.elapsed() >= timeout {
            child.kill().map_err(|error| {
                ProcessError::retryable(format!("kill timed-out video probe tool: {error}"))
            })?;
            child.wait().map_err(|error| {
                ProcessError::retryable(format!("reap timed-out video probe tool: {error}"))
            })?;
            return Err(ProcessError::terminal(format!(
                "video probe timed out after {} ms",
                timeout.as_millis()
            )));
        }
        thread::sleep(COMMAND_POLL);
    };

    Ok(CommandOutput {
        status,
        stdout: stdout.read_bounded(MAX_PROBE_STDOUT_BYTES, "video probe stdout")?,
        stderr: stderr.read_bounded(MAX_PROBE_STDERR_BYTES, "video probe stderr")?,
    })
}

struct CaptureFile {
    path: PathBuf,
    file: File,
}

impl CaptureFile {
    fn new(prefix: &str) -> Result<Self, ProcessError> {
        let temp_root = std::env::temp_dir();
        for _ in 0..256 {
            let sequence = CAPTURE_SEQUENCE.fetch_add(1, Ordering::Relaxed);
            let path = temp_root.join(format!("{prefix}-{}-{sequence}.tmp", std::process::id()));
            match OpenOptions::new()
                .read(true)
                .write(true)
                .create_new(true)
                .open(&path)
            {
                Ok(file) => return Ok(Self { path, file }),
                Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => continue,
                Err(error) => {
                    return Err(ProcessError::retryable(format!(
                        "create video command capture: {error}"
                    )))
                }
            }
        }
        Err(ProcessError::retryable(
            "could not allocate video command capture file",
        ))
    }

    fn read_bounded(&mut self, max_bytes: u64, context: &str) -> Result<Vec<u8>, ProcessError> {
        let length = self
            .file
            .metadata()
            .map_err(|error| ProcessError::retryable(format!("stat {context}: {error}")))?
            .len();
        if length > max_bytes {
            return Err(ProcessError::terminal(format!(
                "{context} exceeded {max_bytes} byte limit"
            )));
        }
        self.file
            .seek(SeekFrom::Start(0))
            .map_err(|error| ProcessError::retryable(format!("rewind {context}: {error}")))?;
        let mut bytes = Vec::with_capacity(length as usize);
        self.file
            .read_to_end(&mut bytes)
            .map_err(|error| ProcessError::retryable(format!("read {context}: {error}")))?;
        Ok(bytes)
    }
}

impl Drop for CaptureFile {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.path);
    }
}

#[derive(Debug, Default)]
struct ParsedProbe {
    video_streams: Vec<ParsedStream>,
    audio_codecs: Vec<Option<String>>,
    duration_seconds: Option<String>,
}

#[derive(Debug, Default)]
struct ParsedStream {
    codec_name: Option<String>,
    pixel_format: Option<String>,
    width: Option<i32>,
    height: Option<i32>,
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
        let mut width = None;
        let mut height = None;
        let mut duration = None;

        for field in line.split('|') {
            let Some((key, value)) = field.split_once('=') else {
                return Err(ProcessError::terminal(format!(
                    "ffprobe field has invalid compact syntax: {field}"
                )));
            };
            match key {
                "codec_type" => codec_type = Some(value),
                "codec_name" => codec_name = Some(value.to_owned()),
                "pix_fmt" => pixel_format = Some(value.to_owned()),
                "width" => width = Some(parse_i32_field("width", value)?),
                "height" => height = Some(parse_i32_field("height", value)?),
                "duration" => duration = Some(value.to_owned()),
                _ => {}
            }
        }

        match codec_type {
            Some("video") => parsed.video_streams.push(ParsedStream {
                codec_name,
                pixel_format,
                width,
                height,
            }),
            Some("audio") => parsed.audio_codecs.push(codec_name),
            Some(_) => {}
            None => {
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

    let width = video
        .width
        .ok_or_else(|| ProcessError::terminal("ffprobe did not report video width"))?;
    let height = video
        .height
        .ok_or_else(|| ProcessError::terminal("ffprobe did not report video height"))?;
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
        video_codec,
        pixel_format,
        audio_codecs,
    })
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
    use crate::processing::FailureClass;

    const VALID_MP4: &str = "codec_name=h264|codec_type=video|width=1920|height=1080|pix_fmt=yuv420p\ncodec_name=aac|codec_type=audio\nduration=12.345000\n";

    #[test]
    fn accepts_browser_compatible_mp4_passthrough() {
        let metadata = classify_probe(MediaType::Video(VideoFormat::Mp4), VALID_MP4).unwrap();
        assert_eq!(metadata.output_format, OutputFormat::Mp4);
        assert_eq!(metadata.mime_type, "video/mp4");
        assert_eq!((metadata.width, metadata.height), (1920, 1080));
        assert_eq!(metadata.duration_ms, 12_345);
        assert_eq!(metadata.video_codec, "h264");
        assert_eq!(metadata.pixel_format, "yuv420p");
        assert_eq!(metadata.audio_codecs, vec!["aac"]);
    }

    #[test]
    fn accepts_mp4_without_audio() {
        let metadata = classify_probe(
            MediaType::Video(VideoFormat::Mp4),
            "codec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p\nduration=0.250000\n",
        )
        .unwrap();
        assert!(metadata.audio_codecs.is_empty());
        assert_eq!(metadata.duration_ms, 250);
    }

    #[test]
    fn rejects_codecs_that_need_transcoding() {
        for probe in [
            "codec_name=hevc|codec_type=video|width=1920|height=1080|pix_fmt=yuv420p\nduration=1.0\n",
            "codec_name=h264|codec_type=video|width=1920|height=1080|pix_fmt=yuv420p10le\nduration=1.0\n",
            "codec_name=h264|codec_type=video|width=1920|height=1080|pix_fmt=yuv420p\ncodec_name=ac3|codec_type=audio\nduration=1.0\n",
        ] {
            let error = classify_probe(MediaType::Video(VideoFormat::Mp4), probe).unwrap_err();
            assert_eq!(error.class(), FailureClass::Terminal);
            assert!(error.to_string().contains("requires transcoding"));
        }
    }

    #[test]
    fn rejects_ambiguous_or_unbounded_video_metadata() {
        let multiple = format!(
            "{}{}",
            "codec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p\n", VALID_MP4
        );
        let error = classify_probe(MediaType::Video(VideoFormat::Mp4), &multiple).unwrap_err();
        assert_eq!(error.class(), FailureClass::Terminal);

        let too_large = "codec_name=h264|codec_type=video|width=16385|height=1080|pix_fmt=yuv420p\nduration=1.0\n";
        let error = classify_probe(MediaType::Video(VideoFormat::Mp4), too_large).unwrap_err();
        assert_eq!(error.class(), FailureClass::Terminal);

        let too_many_pixels = "codec_name=h264|codec_type=video|width=12000|height=12000|pix_fmt=yuv420p\nduration=1.0\n";
        let error =
            classify_probe(MediaType::Video(VideoFormat::Mp4), too_many_pixels).unwrap_err();
        assert_eq!(error.class(), FailureClass::Terminal);
    }

    #[test]
    fn rejects_missing_or_invalid_duration() {
        for probe in [
            "codec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p\n",
            "codec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p\nduration=N/A\n",
            "codec_name=h264|codec_type=video|width=640|height=360|pix_fmt=yuv420p\nduration=0\n",
        ] {
            let error = classify_probe(MediaType::Video(VideoFormat::Mp4), probe).unwrap_err();
            assert_eq!(error.class(), FailureClass::Terminal);
        }
    }

    #[test]
    fn ebml_is_recognized_but_not_silently_relabelled_as_webm() {
        let error = classify_probe(MediaType::Video(VideoFormat::Ebml), VALID_MP4).unwrap_err();
        assert_eq!(error.class(), FailureClass::Terminal);
        assert!(error.to_string().contains("EBML/WebM"));
    }

    #[test]
    fn processing_version_is_explicit() {
        assert_eq!(VIDEO_PROCESSING_VERSION, 1);
    }
}
