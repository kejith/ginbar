package utils

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProcessVideo moves the input file to the video directory and creates a
// thumbnail.
func ProcessVideo(inputFilePath, format string, dirs Directories) (fileName, thumbnailFilename string, err error) {
	ext := filepath.Ext(inputFilePath)
	dstFileName := GenerateFilename(ext)
	dst := filepath.Join(dirs.Video, dstFileName)

	if err = exec.Command("mv", inputFilePath, dst).Run(); err != nil {
		return "", "", fmt.Errorf("move video: %w", err)
	}

	base := dstFileName[:len(dstFileName)-len(ext)]
	thumbnailFilename, err = CreateVideoThumbnail(dst, base, dirs)
	if err != nil {
		return "", "", fmt.Errorf("video thumbnail: %w", err)
	}
	return dstFileName, thumbnailFilename, nil
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
