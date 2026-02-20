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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: unexpected status %d for %s", resp.StatusCode, url)
	}

	f, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("download: create file: %w", err)
	}
	defer f.Close()

	if _, err = io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("download: write file: %w", err)
	}

	return dst, nil
}

// LoadFileFromURL performs a GET and returns the response + content-type parts.
func LoadFileFromURL(url string) (resp *http.Response, fileType, fileFormat string, err error) {
	resp, err = httpClient.Get(url)
	if err != nil {
		return nil, "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", "", fmt.Errorf("load: status %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	fileType, fileFormat = splitContentType(ct)
	return resp, fileType, fileFormat, nil
}

func splitContentType(ct string) (fileType, fileFormat string) {
	for i, c := range ct {
		if c == '/' {
			return ct[:i], ct[i+1:]
		}
	}
	return ct, ""
}
