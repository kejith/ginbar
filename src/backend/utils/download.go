package utils

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"
)

const (
	downloadTimeout     = 30 * time.Second
	downloadMaxRedirect = 5
)

var httpClient = &http.Client{
	Timeout: downloadTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= downloadMaxRedirect {
			return fmt.Errorf("download: too many redirects (max %d)", downloadMaxRedirect)
		}
		return nil
	},
}

// DownloadFile downloads a file from url into dir and returns the local path.
func DownloadFile(url string, dir string) (filePath string, err error) {
	dst := filepath.Join(dir, path.Base(url))

	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("download: GET failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: unexpected status %d for %s", resp.StatusCode, url)
	}

	f, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("download: create file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err = io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("download: write file: %w", err)
	}

	return dst, nil
}
