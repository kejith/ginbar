package utils

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// ProcessImage converts inputFilePath → webp, computes perceptual hash, and
// creates a square thumbnail.
func ProcessImage(inputFilePath string, dirs Directories) (*ImageProcessResult, error) {
	fileName := filepath.Base(inputFilePath)
	outputFilePath := filepath.Join(dirs.Image, fileName+".webp")

	if err := ConvertImageToWebp(inputFilePath, outputFilePath, 75); err != nil {
		return nil, fmt.Errorf("convert to webp: %w", err)
	}

	img, err := LoadImageFile(inputFilePath)
	if err != nil {
		return nil, fmt.Errorf("load image: %w", err)
	}

	hash, err := goimagehash.ExtPerceptionHash(*img, 16, 16)
	if err != nil {
		return nil, fmt.Errorf("perceptual hash: %w", err)
	}

	outputThumbnailFilePath := filepath.Join(dirs.Thumbnail, fileName)
	if err := CreateThumbnailFromImage(img, outputThumbnailFilePath, dirs); err != nil {
		return nil, fmt.Errorf("thumbnail: %w", err)
	}

	bounds := (*img).Bounds()
	return &ImageProcessResult{
		Filename:          filepath.Base(outputFilePath),
		ThumbnailFilename: filepath.Base(outputThumbnailFilePath),
		UploadedFilename:  filepath.Base(inputFilePath),
		PerceptionHash:    hash,
		Width:             bounds.Dx(),
		Height:            bounds.Dy(),
	}, nil
}

// LoadImageFile opens and decodes an image from disk.
func LoadImageFile(inputFilePath string) (*image.Image, error) {
	f, err := os.Open(inputFilePath)
	if err != nil {
		return nil, fmt.Errorf("open image: %w", err)
	}
	defer f.Close()

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
	defer f.Close()

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

// CreateThumbnailFromImage crops to 150×150 via smartcrop then saves as webp.
func CreateThumbnailFromImage(img *image.Image, dstFilePath string, dirs Directories) error {
	cropped, err := CropImage(img, 150, 150)
	if err != nil {
		return fmt.Errorf("crop: %w", err)
	}

	tmpPath, err := SaveImageJPEG(cropped, filepath.Join(dirs.Tmp, "thumbnails"), GenerateFilename("jpeg"))
	if err != nil {
		return fmt.Errorf("save tmp jpeg: %w", err)
	}

	if err = ConvertImageToWebp(tmpPath, dstFilePath, 75); err != nil {
		return fmt.Errorf("convert thumbnail to webp: %w", err)
	}
	return nil
}

// ConvertImageToWebp calls cwebp to convert inputFilePath → outputFilePath.
func ConvertImageToWebp(inputFilePath, outputFilePath string, quality uint) error {
	args := fmt.Sprintf("%s -q %d -preset picture -m 6 -mt -o %s",
		inputFilePath, quality, outputFilePath)
	cmd := exec.Command("cwebp", strings.Split(args, " ")...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cwebp: %w — stderr: %s", err, errb.String())
	}
	return nil
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
