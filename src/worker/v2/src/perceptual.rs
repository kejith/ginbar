use image::{DynamicImage, imageops::FilterType};

/// Version of the perceptual-hash contract persisted in `media.perceptual_hash`.
///
/// Version 1 is a 64-bit horizontal difference hash (dHash): the already-decoded
/// representative image is resized directly to 9x8 with the Triangle filter,
/// converted to luma at that bounded size, then each pixel is compared with its
/// right-hand neighbor in row-major order. The first comparison is bit 0.
///
/// Changing any output-affecting detail requires a new processing/hash contract;
/// hashes from different contracts must not be compared as exact duplicates.
pub const PERCEPTUAL_HASH_VERSION: u16 = 1;

const HASH_WIDTH: u32 = 8;
const HASH_HEIGHT: u32 = 8;
const SAMPLE_WIDTH: u32 = HASH_WIDTH + 1;

pub fn gradient_hash(image: &DynamicImage) -> i64 {
    let sample = image
        .resize_exact(SAMPLE_WIDTH, HASH_HEIGHT, FilterType::Triangle)
        .to_luma8();

    let mut hash = 0_u64;
    let mut bit = 0_u32;
    for y in 0..HASH_HEIGHT {
        for x in 0..HASH_WIDTH {
            if sample.get_pixel(x, y).0[0] > sample.get_pixel(x + 1, y).0[0] {
                hash |= 1_u64 << bit;
            }
            bit += 1;
        }
    }

    hash as i64
}

#[cfg(test)]
mod tests {
    use super::*;
    use image::{GrayImage, Luma};

    fn horizontal_gradient(reverse: bool) -> DynamicImage {
        let image = GrayImage::from_fn(90, 80, |x, _| {
            let value = if reverse { 89 - x } else { x } as u8;
            Luma([value.saturating_mul(2)])
        });
        DynamicImage::ImageLuma8(image)
    }

    #[test]
    fn identical_decoded_media_hashes_deterministically() {
        let image = horizontal_gradient(false);
        let first = gradient_hash(&image);
        assert_eq!(first, gradient_hash(&image));
        assert_eq!(PERCEPTUAL_HASH_VERSION, 1);
    }

    #[test]
    fn clear_opposite_gradients_do_not_exactly_match() {
        assert_ne!(
            gradient_hash(&horizontal_gradient(false)),
            gradient_hash(&horizontal_gradient(true))
        );
    }

    #[test]
    fn equivalent_representative_pixels_use_the_same_hash_path() {
        let image = horizontal_gradient(false);
        let resized = image.resize_exact(180, 160, FilterType::Nearest);
        assert_eq!(gradient_hash(&image), gradient_hash(&resized));
    }
}
