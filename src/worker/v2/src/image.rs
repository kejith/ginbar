use crate::output::OutputStore;
use crate::processing::{
    processed_output_key, ImageFormat, ImageProcessor, MediaKind, MediaType, OutputFormat,
    ProcessError, ProcessedMedia, VerifiedSource,
};
use image::codecs::png::PngDecoder;
use image::codecs::webp::WebPDecoder;
use image::imageops::FilterType;
use image::{
    DynamicImage, GenericImageView, ImageDecoder, ImageFormat as CodecFormat, ImageReader,
};
use rgb::FromSlice;
use std::io::{BufReader, Seek, SeekFrom};

// These constants are part of processing contract v1. Changing an output-affecting
// value after integration requires incrementing PROCESSING_VERSION.
const MAX_INPUT_DIMENSION: u32 = 16_384;
const MAX_INPUT_PIXELS: u64 = 80_000_000;
const MAX_DECODE_ALLOC_BYTES: u64 = 384 * 1024 * 1024;
const MAX_OUTPUT_DIMENSION: u32 = 1_280;
const THUMBNAIL_DIMENSION: u32 = 256;
const MAIN_QUALITY: f32 = 75.0;
const THUMBNAIL_QUALITY: f32 = 60.0;
const ENCODE_SPEED: u8 = 10;
const ENCODE_THREADS: usize = 1;

#[derive(Debug, Clone)]
pub struct BoundedImageProcessor {
    outputs: OutputStore,
}

impl BoundedImageProcessor {
    pub fn new(outputs: OutputStore) -> Self {
        Self { outputs }
    }

    pub fn output_store(&self) -> &OutputStore {
        &self.outputs
    }
}

impl ImageProcessor for BoundedImageProcessor {
    fn process_image(
        &mut self,
        source: &mut VerifiedSource,
        post_id: i64,
    ) -> Result<ProcessedMedia, ProcessError> {
        ensure_supported_still(source)?;
        let image = decode_verified_still(source)?;
        let (decoded_width, decoded_height) = image.dimensions();
        validate_dimensions(decoded_width, decoded_height)?;

        let thumbnail = make_square_thumbnail(&image);
        let main = if decoded_width > MAX_OUTPUT_DIMENSION || decoded_height > MAX_OUTPUT_DIMENSION
        {
            let resized = image.resize(
                MAX_OUTPUT_DIMENSION,
                MAX_OUTPUT_DIMENSION,
                FilterType::Triangle,
            );
            drop(image);
            resized
        } else {
            image
        };
        let (width, height) = main.dimensions();

        // Encode both objects before creating any durable output. This avoids leaving
        // a canonical object behind merely because the second encode failed.
        let thumbnail_bytes = encode_avif(&thumbnail, THUMBNAIL_QUALITY)?;
        let main_bytes = encode_avif(&main, MAIN_QUALITY)?;

        let main_key = processed_output_key(post_id, &source.sha256, OutputFormat::Avif)?;
        let thumbnail_key = thumbnail_output_key(&main_key)?;

        // Publish the auxiliary object first. Database publication happens only after
        // this processor returns, so a ready media row always implies both files were
        // durably and collision-safely published.
        self.outputs
            .publish_bytes(&thumbnail_key, &thumbnail_bytes)?;
        let published = self.outputs.publish_bytes(&main_key, &main_bytes)?;

        let byte_size = i64::try_from(published.byte_size).map_err(|_| {
            ProcessError::terminal("processed AVIF byte size does not fit database bigint")
        })?;
        let width = i32::try_from(width)
            .map_err(|_| ProcessError::terminal("processed AVIF width does not fit database"))?;
        let height = i32::try_from(height)
            .map_err(|_| ProcessError::terminal("processed AVIF height does not fit database"))?;

        Ok(ProcessedMedia {
            kind: MediaKind::Image,
            storage_key: main_key,
            mime_type: "image/avif".to_owned(),
            width,
            height,
            duration_ms: 0,
            byte_size,
            sha256: published.sha256,
            perceptual_hash: None,
        })
    }
}

pub fn thumbnail_output_key(main_storage_key: &str) -> Result<String, ProcessError> {
    let stem = main_storage_key
        .strip_suffix(".avif")
        .ok_or_else(|| ProcessError::terminal("main image output key must end in .avif"))?;
    Ok(format!("{stem}.thumb.avif"))
}

fn ensure_supported_still(source: &mut VerifiedSource) -> Result<(), ProcessError> {
    match source.media_type {
        MediaType::Image(ImageFormat::Jpeg) => Ok(()),
        MediaType::Image(ImageFormat::Png) => {
            source
                .file
                .seek(SeekFrom::Start(0))
                .map_err(|error| ProcessError::retryable(format!("rewind PNG source: {error}")))?;
            let decoder =
                PngDecoder::with_limits(BufReader::new(&mut source.file), decode_limits())
                    .map_err(|error| classify_decode_error("inspect PNG", error))?;
            if decoder
                .is_apng()
                .map_err(|error| classify_decode_error("inspect PNG animation", error))?
            {
                return Err(ProcessError::terminal(
                    "animated PNG is not supported by the still-image processor",
                ));
            }
            Ok(())
        }
        MediaType::Image(ImageFormat::Webp) => {
            source
                .file
                .seek(SeekFrom::Start(0))
                .map_err(|error| ProcessError::retryable(format!("rewind WebP source: {error}")))?;
            let mut decoder = WebPDecoder::new(BufReader::new(&mut source.file))
                .map_err(|error| classify_decode_error("inspect WebP", error))?;
            decoder
                .set_limits(decode_limits())
                .map_err(|error| classify_decode_error("limit WebP decode", error))?;
            if decoder.has_animation() {
                return Err(ProcessError::terminal(
                    "animated WebP is not supported by the still-image processor",
                ));
            }
            Ok(())
        }
        MediaType::Image(ImageFormat::Gif) => Err(ProcessError::terminal(
            "GIF input is not supported by the first still-image processor",
        )),
        MediaType::Image(ImageFormat::Avif | ImageFormat::Heif) => Err(ProcessError::terminal(
            "AVIF/HEIF input decode is not supported by this processing version",
        )),
        MediaType::Video(_) => Err(ProcessError::terminal(
            "video input cannot be processed by the image processor",
        )),
    }
}

fn decode_verified_still(source: &mut VerifiedSource) -> Result<DynamicImage, ProcessError> {
    let format = match source.media_type {
        MediaType::Image(ImageFormat::Jpeg) => CodecFormat::Jpeg,
        MediaType::Image(ImageFormat::Png) => CodecFormat::Png,
        MediaType::Image(ImageFormat::Webp) => CodecFormat::WebP,
        _ => {
            return Err(ProcessError::terminal(
                "image format reached decode without still-image support",
            ))
        }
    };

    source
        .file
        .seek(SeekFrom::Start(0))
        .map_err(|error| ProcessError::retryable(format!("rewind image source: {error}")))?;
    let mut reader = ImageReader::with_format(BufReader::new(&mut source.file), format);
    reader.limits(decode_limits());
    let mut decoder = reader
        .into_decoder()
        .map_err(|error| classify_decode_error("open image decoder", error))?;

    let (width, height) = decoder.dimensions();
    validate_dimensions(width, height)?;
    let orientation = decoder
        .orientation()
        .map_err(|error| classify_decode_error("read image orientation", error))?;
    let mut image = DynamicImage::from_decoder(decoder)
        .map_err(|error| classify_decode_error("fully decode image", error))?;
    image.apply_orientation(orientation);

    let (width, height) = image.dimensions();
    validate_dimensions(width, height)?;
    Ok(image)
}

fn decode_limits() -> image::Limits {
    let mut limits = image::Limits::default();
    limits.max_image_width = Some(MAX_INPUT_DIMENSION);
    limits.max_image_height = Some(MAX_INPUT_DIMENSION);
    limits.max_alloc = Some(MAX_DECODE_ALLOC_BYTES);
    limits
}

fn validate_dimensions(width: u32, height: u32) -> Result<(), ProcessError> {
    if width == 0 || height == 0 {
        return Err(ProcessError::terminal(
            "decoded image dimensions must be positive",
        ));
    }
    if width > MAX_INPUT_DIMENSION || height > MAX_INPUT_DIMENSION {
        return Err(ProcessError::terminal(format!(
            "decoded image dimensions {width}x{height} exceed {MAX_INPUT_DIMENSION}px limit"
        )));
    }
    let pixels = u64::from(width) * u64::from(height);
    if pixels > MAX_INPUT_PIXELS {
        return Err(ProcessError::terminal(format!(
            "decoded image pixel count {pixels} exceeds {MAX_INPUT_PIXELS} limit"
        )));
    }
    Ok(())
}

fn make_square_thumbnail(image: &DynamicImage) -> DynamicImage {
    let (width, height) = image.dimensions();
    let side = width.min(height);
    let x = (width - side) / 2;
    let y = (height - side) / 2;
    let view = image.view(x, y, side, side);
    DynamicImage::ImageRgba8(image::imageops::resize(
        &*view,
        THUMBNAIL_DIMENSION,
        THUMBNAIL_DIMENSION,
        FilterType::Triangle,
    ))
}

fn encode_avif(image: &DynamicImage, quality: f32) -> Result<Vec<u8>, ProcessError> {
    let rgba = image.to_rgba8();
    let pixels = rgba.as_raw().as_slice().as_rgba();
    let encoded = ravif::Encoder::new()
        .with_quality(quality)
        .with_alpha_quality(quality)
        .with_speed(ENCODE_SPEED)
        .with_num_threads(Some(ENCODE_THREADS))
        .encode_rgba(ravif::Img::new(
            pixels,
            rgba.width() as usize,
            rgba.height() as usize,
        ))
        .map_err(|error| ProcessError::retryable(format!("encode AVIF: {error}")))?;
    Ok(encoded.avif_file)
}

fn classify_decode_error(context: &str, error: image::ImageError) -> ProcessError {
    match error {
        image::ImageError::IoError(error) => ProcessError::retryable(format!("{context}: {error}")),
        other => ProcessError::terminal(format!("{context}: {other}")),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::processing::{MediaRoot, NeverCancelled, SourceRecord};
    use image::{ExtendedColorType, ImageEncoder};
    use sha2::{Digest, Sha256};
    use std::fs;
    use std::path::PathBuf;
    use std::time::{SystemTime, UNIX_EPOCH};

    struct TempRoot(PathBuf);

    impl TempRoot {
        fn new() -> Self {
            let nonce = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .expect("clock")
                .as_nanos();
            let path = std::env::temp_dir().join(format!(
                "ginbar-image-processor-{}-{nonce}",
                std::process::id()
            ));
            fs::create_dir_all(path.join("sources/01")).expect("create source root");
            Self(path)
        }
    }

    impl Drop for TempRoot {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    fn png_bytes(width: u32, height: u32) -> Vec<u8> {
        let mut pixels = vec![0u8; width as usize * height as usize * 4];
        for chunk in pixels.as_chunks_mut::<4>().0 {
            chunk.copy_from_slice(&[32, 96, 192, 255]);
        }
        let mut encoded = Vec::new();
        image::codecs::png::PngEncoder::new(&mut encoded)
            .write_image(&pixels, width, height, ExtendedColorType::Rgba8)
            .expect("encode test PNG");
        encoded
    }

    fn verified_png(root: &TempRoot, bytes: &[u8]) -> VerifiedSource {
        let key = "sources/01/0123456789abcdef0123456789abcdef";
        fs::write(root.0.join(key), bytes).expect("write source");
        let sha256: [u8; 32] = Sha256::digest(bytes).into();
        let source = SourceRecord {
            post_id: 513,
            source_type: 0,
            storage_key: key.to_owned(),
            declared_mime_type: "image/png".to_owned(),
            byte_size: bytes.len() as i64,
            sha256,
        };
        MediaRoot::new(&root.0)
            .unwrap()
            .open_verified(&source, 1024 * 1024, &NeverCancelled)
            .unwrap()
    }

    #[test]
    fn thumbnail_key_is_derived_from_canonical_identity() {
        let main =
            "media/01/513/v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.avif";
        assert_eq!(
            thumbnail_output_key(main).unwrap(),
            "media/01/513/v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.thumb.avif"
        );
    }

    #[test]
    fn processor_fully_decodes_and_durably_publishes_avif_pair() {
        let root = TempRoot::new();
        let bytes = png_bytes(48, 32);
        let mut source = verified_png(&root, &bytes);
        let output_store = OutputStore::new(&root.0).unwrap();
        let mut processor = BoundedImageProcessor::new(output_store);

        let first = processor.process_image(&mut source, 513).unwrap();
        let second = processor.process_image(&mut source, 513).unwrap();
        assert_eq!(first, second);
        assert_eq!(first.kind, MediaKind::Image);
        assert_eq!(first.mime_type, "image/avif");
        assert_eq!((first.width, first.height), (48, 32));

        let main_bytes = fs::read(root.0.join(&first.storage_key)).unwrap();
        assert!(main_bytes.len() >= 12);
        assert_eq!(&main_bytes[4..8], b"ftyp");
        assert_eq!(&main_bytes[8..12], b"avif");
        let thumb_key = thumbnail_output_key(&first.storage_key).unwrap();
        let thumb_bytes = fs::read(root.0.join(thumb_key)).unwrap();
        assert!(thumb_bytes.len() >= 12);
        assert_eq!(&thumb_bytes[4..8], b"ftyp");
    }

    #[test]
    fn corrupt_sniffable_png_is_terminal_after_full_decode() {
        let root = TempRoot::new();
        let bytes = b"\x89PNG\r\n\x1a\nnot-a-complete-png";
        let mut source = verified_png(&root, bytes);
        let output_store = OutputStore::new(&root.0).unwrap();
        let mut processor = BoundedImageProcessor::new(output_store);
        let error = processor.process_image(&mut source, 513).unwrap_err();
        assert_eq!(error.class(), crate::processing::FailureClass::Terminal);
    }

    #[test]
    fn deterministic_collision_does_not_replace_existing_main() {
        let root = TempRoot::new();
        let bytes = png_bytes(32, 32);
        let mut source = verified_png(&root, &bytes);
        let main_key = processed_output_key(513, &source.sha256, OutputFormat::Avif).unwrap();
        let main_path = root.0.join(&main_key);
        fs::create_dir_all(main_path.parent().unwrap()).unwrap();
        fs::write(&main_path, b"unrelated").unwrap();

        let output_store = OutputStore::new(&root.0).unwrap();
        let mut processor = BoundedImageProcessor::new(output_store);
        let error = processor.process_image(&mut source, 513).unwrap_err();
        assert_eq!(error.class(), crate::processing::FailureClass::Terminal);
        assert_eq!(fs::read(main_path).unwrap(), b"unrelated");
    }

    #[test]
    fn explicit_dimension_and_pixel_limits_are_terminal() {
        assert!(validate_dimensions(MAX_INPUT_DIMENSION + 1, 1).is_err());
        assert!(validate_dimensions(10_000, 10_000).is_err());
        assert!(validate_dimensions(4000, 4000).is_ok());
    }
}
