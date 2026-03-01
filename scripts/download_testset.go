//go:build ignore

// download_testset fetches 100 pr0gramm images tagged "da mal Sättigung rausdrehen"
// and stores them permanently as the worker's canonical test dataset.
//
//   go run ./scripts/download_testset.go        # from workspace root
//   make test-data                              # via Makefile
//
// Output:
//   src/worker/test_data/manifest.json          – committed (frozen URL list)
//   src/worker/test_data/images/<filename>      – gitignored (actual blobs)
//
// The manifest is the source of truth.  Re-running is idempotent: already-
// downloaded files are skipped.  Images not available on the CDN are skipped
// with a warning.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	targetCount  = 100
	searchTags   = "da mal Sättigung rausdrehen"
	searchFlags  = 1 // SFW only
	apiBase      = "https://pr0gramm.com/api/items/get"
	imgBase      = "https://img.pr0gramm.com/"
	pageDelay    = 600 * time.Millisecond
	downloadPace = 250 * time.Millisecond
	maxRetries   = 4
)

// ── Types ─────────────────────────────────────────────────────────────────────

type pr0grammItem struct {
	ID       int64  `json:"id"`
	Promoted int64  `json:"promoted"`
	Image    string `json:"image"`
}

type pr0grammResp struct {
	Items []pr0grammItem `json:"items"`
	AtEnd bool           `json:"atEnd"`
}

// ManifestEntry is the serialised form committed to the repo.
type ManifestEntry struct {
	ID    int64  `json:"id"`
	Image string `json:"image"`
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// ── pr0gramm API ──────────────────────────────────────────────────────────────

func fetchPage(tags string, flags int, older int64) (*pr0grammResp, error) {
	params := url.Values{}
	params.Set("tags", tags)
	params.Set("flags", fmt.Sprintf("%d", flags))
	params.Set("promoted", "1")
	params.Set("show_junk", "0")
	if older > 0 {
		params.Set("older", fmt.Sprintf("%d", older))
	}
	apiURL := apiBase + "?" + params.Encode()

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, _ := http.NewRequest(http.MethodGet, apiURL, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; wallium-testset/1.0)")
		req.Header.Set("Accept", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", apiURL, err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			wait := time.Duration(math.Pow(2, float64(attempt+1))) * time.Second
			if wait > 60*time.Second {
				wait = 60 * time.Second
			}
			fmt.Printf("  rate-limited, waiting %s…\n", wait)
			time.Sleep(wait)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
		}
		var result pr0grammResp
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode: %w", err)
		}
		resp.Body.Close()
		return &result, nil
	}
	return nil, fmt.Errorf("rate limit exceeded after %d retries", maxRetries)
}

func minPromoted(items []pr0grammItem) int64 {
	if len(items) == 0 {
		return 0
	}
	m := items[0].Promoted
	for _, it := range items[1:] {
		if it.Promoted < m {
			m = it.Promoted
		}
	}
	return m
}

// ── Download helpers ──────────────────────────────────────────────────────────

// imageFilename flattens the CDN path to a single file name suitable for
// storing in a flat directory (e.g. "2024/01/abc.jpg" → "2024_01_abc.jpg").
func imageFilename(imgPath string) string {
	return strings.ReplaceAll(imgPath, "/", "_")
}

func downloadImage(imgPath, outDir string) (string, error) {
	cdnURL := imgBase + imgPath
	filename := imageFilename(imgPath)
	outPath := filepath.Join(outDir, filename)

	req, _ := http.NewRequest(http.MethodGet, cdnURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Wallium/1.0)")
	req.Header.Set("Referer", "https://pr0gramm.com/")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, cdnURL)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return outPath, nil
}

// ── Entry point ───────────────────────────────────────────────────────────────

func main() {
	// Allow path overrides via environment so CI or custom layouts work.
	manifestPath := envOr("TESTSET_MANIFEST", filepath.Join("src", "worker", "test_data", "manifest.json"))
	imagesDir := envOr("TESTSET_IMAGES_DIR", filepath.Join("src", "worker", "test_data", "images"))

	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		fatalf("create images dir: %v", err)
	}

	// ── Phase 1: collect item list from pr0gramm API ─────────────────────────

	// If manifest already exists, skip the API fetch and use it directly.
	var items []pr0grammItem
	if _, err := os.Stat(manifestPath); err == nil {
		fmt.Printf("manifest already exists — using %s (delete it to re-fetch)\n", manifestPath)
		data, _ := os.ReadFile(manifestPath)
		var entries []ManifestEntry
		if json.Unmarshal(data, &entries) == nil {
			for _, e := range entries {
				items = append(items, pr0grammItem{ID: e.ID, Image: e.Image})
			}
			fmt.Printf("loaded %d items from existing manifest\n", len(items))
		}
	}

	if len(items) == 0 {
		fmt.Printf("fetching pr0gramm items for: %q  (flags=%d)\n", searchTags, searchFlags)
		older := int64(0)
		for page := 1; len(items) < targetCount; page++ {
			resp, err := fetchPage(searchTags, searchFlags, older)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  page %d FAILED: %v\n", page, err)
				break
			}
			accepted := 0
			for _, it := range resp.Items {
				ext := strings.ToLower(path.Ext(it.Image))
				switch ext {
				case ".jpg", ".jpeg", ".webp", ".avif", ".jxl", ".gif":
					items = append(items, it)
					accepted++
				}
			}
			fmt.Printf("  page %d: %d items accepted (%d total)\n", page, accepted, len(items))
			if resp.AtEnd || len(resp.Items) == 0 {
				fmt.Println("  reached end of results")
				break
			}
			older = minPromoted(resp.Items)
			time.Sleep(pageDelay)
		}

		if len(items) > targetCount {
			items = items[:targetCount]
		}
		fmt.Printf("collected %d items\n", len(items))

		// Write manifest.
		manifest := make([]ManifestEntry, len(items))
		for i, it := range items {
			manifest[i] = ManifestEntry{ID: it.ID, Image: it.Image}
		}
		b, _ := json.MarshalIndent(manifest, "", "  ")
		if err := os.WriteFile(manifestPath, b, 0o644); err != nil {
			fatalf("write manifest: %v", err)
		}
		fmt.Printf("wrote manifest → %s\n", manifestPath)
	}

	// ── Phase 2: download images ──────────────────────────────────────────────

	downloaded, skipped, failed := 0, 0, 0
	for i, it := range items {
		outPath := filepath.Join(imagesDir, imageFilename(it.Image))
		if _, err := os.Stat(outPath); err == nil {
			skipped++
			continue
		}
		fmt.Printf("[%d/%d] %s\n", i+1, len(items), it.Image)
		if _, err := downloadImage(it.Image, imagesDir); err != nil {
			fmt.Fprintf(os.Stderr, "  WARN: download failed: %v\n", err)
			failed++
		} else {
			downloaded++
		}
		time.Sleep(downloadPace)
	}

	fmt.Printf("\ndone: %d downloaded, %d already present, %d failed\n", downloaded, skipped, failed)

	total := downloaded + skipped
	if total < len(items)/2 {
		fmt.Fprintf(os.Stderr, "ERROR: only %d/%d images available\n", total, len(items))
		os.Exit(1)
	}

	fmt.Printf("test dataset ready: %d images in %s\n", total, imagesDir)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
