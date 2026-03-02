package utils

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/corona10/goimagehash"
	"github.com/muesli/smartcrop"
	"github.com/muesli/smartcrop/nfnt"
)

// ImageProcessResult is returned by ProcessImage.
type ImageProcessResult struct {
	Filename          string
	ThumbnailFilename string
	UploadedFilename  string
	PerceptionHash    *goimagehash.ExtImageHash
	Width             int
	Height            int
}

// ProcessImage converts inputFilePath → avif (high quality), computes
// perceptual hash, and creates a square AVIF thumbnail.
func ProcessImage(inputFilePath string, dirs Directories) (*ImageProcessResult, error) {
	fileName := filepath.Base(inputFilePath)
	outputFilePath := filepath.Join(dirs.Image, fileName+".avif")

	// Run the AVIF encode and JPEG normalisation concurrently — both only
	// read inputFilePath and write to independent outputs.
	type avifRes struct{ err error }
	type jpegRes struct {
		path string
		err  error
	}
	avifCh := make(chan avifRes, 1)
	jpegCh := make(chan jpegRes, 1)

	go func() {
		// CRF 18 / preset 8 — visually lossless at ~2× the speed of preset 4.
		// Scale down to at most 920 px wide; images narrower than 920 px are
		// stored at their original width.
		avifCh <- avifRes{ConvertImageToAvif(inputFilePath, outputFilePath, 18, 8, 920)}
	}()
	go func() {
		p, err := NormalizeImageToJPEG(inputFilePath, filepath.Join(dirs.Tmp, "thumbnails"))
		jpegCh <- jpegRes{p, err}
	}()

	if r := <-avifCh; r.err != nil {
		return nil, fmt.Errorf("convert to avif: %w", r.err)
	}
	jr := <-jpegCh
	if jr.err != nil {
		return nil, fmt.Errorf("normalize to jpeg: %w", jr.err)
	}
	jpegPath := jr.path
	defer func() { _ = os.Remove(jpegPath) }()

	img, err := LoadImageFile(jpegPath)
	if err != nil {
		return nil, fmt.Errorf("load image: %w", err)
	}

	hash, err := goimagehash.ExtPerceptionHash(*img, 16, 16)
	if err != nil {
		return nil, fmt.Errorf("perceptual hash: %w", err)
	}

	outputThumbnailFilePath := filepath.Join(dirs.Thumbnail, fileName+".avif")
	if err := CreateThumbnailFromImage(img, outputThumbnailFilePath, dirs); err != nil {
		return nil, fmt.Errorf("thumbnail: %w", err)
	}

	// Read actual dimensions from the encoded AVIF (may differ from the source
	// if it was wider than 920 px and was scaled down).
	outW, outH, _ := GetVideoDimensions(outputFilePath)
	if outW == 0 || outH == 0 {
		// Fallback to source bounds if ffprobe fails for any reason.
		b := (*img).Bounds()
		outW, outH = b.Dx(), b.Dy()
	}
	return &ImageProcessResult{
		Filename:          filepath.Base(outputFilePath),
		ThumbnailFilename: filepath.Base(outputThumbnailFilePath),
		UploadedFilename:  filepath.Base(inputFilePath),
		PerceptionHash:    hash,
		Width:             outW,
		Height:            outH,
	}, nil
}

// LoadImageFile opens and decodes an image from disk.
func LoadImageFile(inputFilePath string) (*image.Image, error) {
	f, err := os.Open(inputFilePath)
	if err != nil {
		return nil, fmt.Errorf("open image: %w", err)
	}
	defer func() { _ = f.Close() }()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return &img, nil
}

// SaveImageJPEG saves img to disk as a JPEG at quality 100.
func SaveImageJPEG(img *image.Image, directory, name string) (string, error) {
	filePath := filepath.Join(directory, name)
	f, err := os.Create(filePath)
	if err != nil {
		return filePath, fmt.Errorf("create jpeg: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err = jpeg.Encode(f, *img, &jpeg.Options{Quality: 100}); err != nil {
		_ = os.Remove(filePath)
		return filePath, fmt.Errorf("encode jpeg: %w", err)
	}
	return filePath, nil
}

// CreateThumbnailFromFile reads from disk and produces a thumbnail.
func CreateThumbnailFromFile(inputFilePath, dstFilePath string, dirs Directories) error {
	img, err := LoadImageFile(inputFilePath)
	if err != nil {
		return fmt.Errorf("load for thumbnail: %w", err)
	}
	return CreateThumbnailFromImage(img, dstFilePath, dirs)
}

// CreateThumbnailFromImage crops to 150×150 via smartcrop then saves as AVIF.
func CreateThumbnailFromImage(img *image.Image, dstFilePath string, dirs Directories) error {
	cropped, err := CropImage(img, 150, 150)
	if err != nil {
		return fmt.Errorf("crop: %w", err)
	}

	tmpPath, err := SaveImageJPEG(cropped, filepath.Join(dirs.Tmp, "thumbnails"), GenerateFilename("jpeg"))
	if err != nil {
		return fmt.Errorf("save tmp jpeg: %w", err)
	}
	defer func() { _ = os.Remove(tmpPath) }()

	// CRF 30 / preset 6: great quality for 150 px, fast encode.
	// maxWidth=0: smartcrop already cropped to 150×150, no further resize needed.
	if err = ConvertImageToAvif(tmpPath, dstFilePath, 30, 6, 0); err != nil {
		return fmt.Errorf("convert thumbnail to avif: %w", err)
	}
	return nil
}

// svtMaxWidth and svtMaxHeight are the hard per-frame limits of libsvtav1.
// Images that exceed either dimension fall back to libaom-av1.
const (
	svtMaxWidth  = 8192
	svtMaxHeight = 8704
)

// ConvertImageToAvif encodes inputFilePath as an AVIF still image using
// ffmpeg.  SVT-AV1 (libsvtav1) is used when the source fits within its
// 8192×8704 px limit; oversized images fall back to libaom-av1.
//
//   crf      quality: 0 = lossless, 63 = worst; 18 ≈ visually lossless,
//                     30 = excellent for small thumbnails.
//   preset   SVT-AV1 speed knob (0 = slowest, 13 = fastest).  Ignored when
//            falling back to libaom-av1 (a fixed cpu-used=4 is used instead).
//   maxWidth if > 0, the output is scaled down so that its width does not
//            exceed maxWidth pixels (height is adjusted proportionally).
//            Pass 0 to disable scaling.
//
// The crop/scale filter ensures even dimensions required by YUV 4:2:0.
func ConvertImageToAvif(inputFilePath, outputFilePath string, crf, preset, maxWidth int) error {
	// Probe dimensions first — needed both to select the encoder and to decide
	// whether to apply the scale-down filter.
	w, h, _ := GetVideoDimensions(inputFilePath)
	useSVT := (w == 0 && h == 0) || (w <= svtMaxWidth && h <= svtMaxHeight)

	// Build the -vf filter chain:
	//   • scale-down to maxWidth only when the image is actually wider
	//     (images narrower than maxWidth keep their original dimensions)
	//   • crop to even dimensions required by YUV 4:2:0
	vf := "crop=trunc(iw/2)*2:trunc(ih/2)*2"
	if maxWidth > 0 && (w == 0 || w > maxWidth) {
		// scale='min(MW,iw)':-2  →  shrink to MW, keep AR, even height.
		// The trailing crop handles any residual odd pixel from the scale.
		vf = fmt.Sprintf("scale='min(%d,iw)':-2,crop=trunc(iw/2)*2:trunc(ih/2)*2", maxWidth)
	}

	var args []string
	if useSVT {
		args = []string{
			"-y",
			"-i", inputFilePath,
			"-frames:v", "1",
			"-vf", vf,
			"-c:v", "libsvtav1",
			"-crf", strconv.Itoa(crf),
			"-preset", strconv.Itoa(preset),
			"-g", "1", // single keyframe = still picture
			"-pix_fmt", "yuv420p",
			outputFilePath,
		}
	} else {
		// libaom-av1 fallback for images taller/wider than SVT-AV1 supports.
		// -b:v 0 enables constant-quality mode; cpu-used 4 is a good balance.
		args = []string{
			"-y",
			"-i", inputFilePath,
			"-frames:v", "1",
			"-vf", vf,
			"-c:v", "libaom-av1",
			"-crf", strconv.Itoa(crf),
			"-b:v", "0",
			"-cpu-used", "4",
			"-g", "1",
			"-pix_fmt", "yuv420p",
			outputFilePath,
		}
	}

	cmd := exec.Command("ffmpeg", args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg avif: %w — stderr: %s", err, errb.String())
	}
	return nil
}

// NormalizeImageToJPEG uses ffmpeg to decode any image format (avif, jxl,
// webp, gif, …) to a temporary JPEG in dir that Go's image decoder can read.
// The caller is responsible for removing the returned path when done.
func NormalizeImageToJPEG(inputFilePath, dir string) (string, error) {
	dst := filepath.Join(dir, GenerateFilename(".jpg"))
	args := []string{
		"-y",
		"-i", inputFilePath,
		"-frames:v", "1",
		"-q:v", "2",
		dst,
	}
	cmd := exec.Command("ffmpeg", args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg normalize: %w — stderr: %s", err, errb.String())
	}
	return dst, nil
}

// CropImage uses smartcrop to find the best square crop of w×h.
func CropImage(imgIn *image.Image, w, h int) (*image.Image, error) {
	width, height := GetCropDimensions(imgIn, w, h)
	resizer := nfnt.NewDefaultResizer()
	analyzer := smartcrop.NewAnalyzer(resizer)
	bestCrop, err := analyzer.FindBestCrop(*imgIn, width, height)
	if err != nil {
		return nil, fmt.Errorf("find best crop: %w", err)
	}

	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	simg, ok := (*imgIn).(subImager)
	if !ok {
		return nil, fmt.Errorf("image does not support SubImage")
	}

	img := simg.SubImage(bestCrop)
	if img.Bounds().Dx() != width || img.Bounds().Dy() != height {
		img = resizer.Resize(img, uint(width), uint(height))
	}
	return &img, nil
}

// GetCropDimensions returns dimensions for cropping; if both are 0 uses the
// smaller axis of the image.
func GetCropDimensions(img *image.Image, width, height int) (int, int) {
	if width == 0 && height == 0 {
		b := (*img).Bounds()
		x, y := b.Dx(), b.Dy()
		if x < y {
			return x, x
		}
		return y, y
	}
	return width, height
}

// GenerateFilename generates a nanosecond-timestamped filename with the given
// extension (include dot, e.g. ".webp", or just "jpeg").
func GenerateFilename(ext string) string {
	return fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
}
