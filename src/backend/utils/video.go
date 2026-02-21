package utils

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// VideoProcessResult is returned by ProcessVideo.
type VideoProcessResult struct {
	Filename          string
	ThumbnailFilename string
	Width             int
	Height            int
}

// ProcessVideo moves the input file to the video directory, creates a
// thumbnail, and probes the video dimensions.
func ProcessVideo(inputFilePath, format string, dirs Directories) (*VideoProcessResult, error) {
	ext := filepath.Ext(inputFilePath)
	dstFileName := GenerateFilename(ext)
	dst := filepath.Join(dirs.Video, dstFileName)

	if err := exec.Command("mv", inputFilePath, dst).Run(); err != nil {
		return nil, fmt.Errorf("move video: %w", err)
	}

	base := dstFileName[:len(dstFileName)-len(ext)]
	thumbnailFilename, err := CreateVideoThumbnail(dst, base, dirs)
	if err != nil {
		return nil, fmt.Errorf("video thumbnail: %w", err)
	}

	w, h, _ := GetVideoDimensions(dst)

	return &VideoProcessResult{
		Filename:          dstFileName,
		ThumbnailFilename: thumbnailFilename,
		Width:             w,
		Height:            h,
	}, nil
}

// GetVideoDimensions uses ffprobe to return the width and height of the first
// video stream. Returns (0, 0, nil) when ffprobe is unavailable or fails.
func GetVideoDimensions(filePath string) (width, height int, err error) {
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0",
		filePath,
	}
	var outb bytes.Buffer
	cmd := exec.Command("ffprobe", args...)
	cmd.Stdout = &outb
	if err = cmd.Run(); err != nil {
		return 0, 0, fmt.Errorf("ffprobe: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(outb.String()), ",")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("ffprobe: unexpected output %q", outb.String())
	}
	width, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe width: %w", err)
	}
	height, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe height: %w", err)
	}
	return width, height, nil
}

// CreateVideoThumbnail extracts a frame at 1s with ffmpeg then converts to webp.
func CreateVideoThumbnail(inputFilePath, name string, dirs Directories) (string, error) {
	jpgFilename := name + ".jpg"
	webpFilename := name + ".webp"
	tmpPath := filepath.Join(dirs.Tmp, jpgFilename)

	args := fmt.Sprintf("-i %s -ss 00:00:01.000 -vframes 1 %s -hide_banner -loglevel panic",
		inputFilePath, tmpPath)
	cmd := exec.Command("ffmpeg", strings.Split(args, " ")...)
	var outb, errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg thumbnail: %w — %s", err, errb.String())
	}

	dstPath := filepath.Join(dirs.Thumbnail, webpFilename)
	if err := CreateThumbnailFromFile(tmpPath, dstPath, dirs); err != nil {
		return "", fmt.Errorf("convert video thumbnail: %w", err)
	}
	return webpFilename, nil
}
