package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

// ── Pr0gramm API constants & types ────────────────────────────────────────────

// These are vars (not consts) so tests can redirect them to a local mock server.
var (
	pr0grammAPIBase = "https://pr0gramm.com/api/items/get"
	pr0grammImgBase = "https://img.pr0gramm.com/"
)

var pr0grammClient = &http.Client{
	Timeout: 60 * time.Second,
}

type pr0grammItem struct {
	ID       int64  `json:"id"`
	Promoted int64  `json:"promoted"`
	Image    string `json:"image"`
	Thumb    string `json:"thumb"`
	Fullsize string `json:"fullsize"`
	Audio    bool   `json:"audio"`
	Mark     int    `json:"mark"`
	User     string `json:"user"`
}

type pr0grammResponse struct {
	Items   []pr0grammItem `json:"items"`
	AtEnd   bool           `json:"atEnd"`
	AtStart bool           `json:"atStart"`
}

// ── Import request form ───────────────────────────────────────────────────────

type importPr0grammForm struct {
	// tags is passed directly to the pr0gramm search API.
	Tags string `json:"tags" form:"tags"`
	// flags controls content filtering: 1=SFW, 2=NSFW, 4=NSFL (bitwise OR).
	// Defaults to 1 (SFW only).
	Flags int `json:"flags" form:"flags"`
	// older is the pagination cursor — the minimum `promoted` value from the
	// previous response. Set to 0 (or omit) for the first request.
	Older int64 `json:"older" form:"older"`
}

// ── Import results ────────────────────────────────────────────────────────────

type pr0grammImportResult struct {
	SourceID int64  `json:"source_id"`
	PostID   int32  `json:"post_id"`
	Image    string `json:"image"`
}

type pr0grammImportError struct {
	SourceID int64  `json:"source_id"`
	Image    string `json:"image"`
	Error    string `json:"error"`
}

// ── Handler ───────────────────────────────────────────────────────────────────

// ImportFromPr0gramm fetches ONE page of pr0gramm items and imports them.
//
//	POST /api/post/import/pr0gramm
//	Content-Type: application/json
//	Body: { "tags": "...", "flags": 1, "older": 0 }
//
// The response includes `next_older` (cursor for the next call) and `at_end`
// so the client can loop page by page while showing a progress bar.
func (s *Server) ImportFromPr0gramm(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	form := new(importPr0grammForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if strings.TrimSpace(form.Tags) == "" {
		return fiber.NewError(fiber.StatusBadRequest, "tags field is required")
	}

	flags := form.Flags
	if flags <= 0 {
		flags = 1
	}

	ctx := context.Background()
	importStart := time.Now()

	// ── Phase 1: fetch page index from pr0gramm ───────────────────────────────
	fetchStart := time.Now()
	resp, err := fetchPr0grammPage(form.Tags, flags, form.Older)
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "pr0gramm API error: "+err.Error())
	}
	fetchDur := time.Since(fetchStart)
	s.log.InfoContext(ctx, "pr0gramm page fetched",
		slog.String("tags", form.Tags),
		slog.Int("item_count", len(resp.Items)),
		slog.Duration("fetch_dur", fetchDur),
	)

	// ── Phase 2: download + process each item concurrently ───────────────────
	// importConcurrency caps how many items are downloaded + processed at once.
	// High enough to hide per-item network latency; low enough to avoid
	// saturating the CDN connection or the DB connection pool.
	const importConcurrency = 16

	results := make([]struct {
		result     *pr0grammImportResult
		skipReason string
		item       pr0grammItem
		dur        time.Duration
	}, len(resp.Items))

	sem := make(chan struct{}, importConcurrency)
	var wg sync.WaitGroup

	for i, item := range resp.Items {
		wg.Add(1)
		i, item := i, item // capture loop vars
		sem <- struct{}{}  // acquire slot (blocks when all workers are busy)
		go func() {
			defer wg.Done()
			defer func() { <-sem }() // release slot
			start := time.Now()
			res, reason := s.importPr0grammItem(ctx, item, u.Name)
			results[i] = struct {
				result     *pr0grammImportResult
				skipReason string
				item       pr0grammItem
				dur        time.Duration
			}{res, reason, item, time.Since(start)}
		}()
	}
	wg.Wait()

	var (
		imported []pr0grammImportResult
		skipped  []pr0grammImportError
	)
	for i, r := range results {
		if r.skipReason != "" {
			s.log.DebugContext(ctx, "pr0gramm item skipped",
				slog.Int("index", i),
				slog.Int64("source_id", r.item.ID),
				slog.String("reason", r.skipReason),
				slog.Duration("dur", r.dur),
			)
			skipped = append(skipped, pr0grammImportError{
				SourceID: r.item.ID,
				Image:    r.item.Image,
				Error:    r.skipReason,
			})
			continue
		}
		s.log.DebugContext(ctx, "pr0gramm item imported",
			slog.Int("index", i),
			slog.Int64("source_id", r.item.ID),
			slog.Duration("dur", r.dur),
		)
		imported = append(imported, *r.result)
	}

	totalDur := time.Since(importStart)
	s.log.InfoContext(ctx, "pr0gramm import complete",
		slog.Int("imported", len(imported)),
		slog.Int("skipped", len(skipped)),
		slog.Duration("total_dur", totalDur),
	)

	nextOlder := minPromoted(resp.Items)
	atEnd := resp.AtEnd || len(resp.Items) == 0

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"imported":   imported,
		"skipped":    skipped,
		"read":       len(resp.Items),
		"at_end":     atEnd,
		"next_older": nextOlder,
		"counts": fiber.Map{
			"imported": len(imported),
			"skipped":  len(skipped),
		},
	})
}

// importPr0grammItem downloads a single pr0gramm item and inserts it.
// Returns (result, "") on success or (nil, reason) when skipped/failed.
func (s *Server) importPr0grammItem(ctx context.Context, item pr0grammItem, userName string) (*pr0grammImportResult, string) {
	if item.Image == "" {
		return nil, "empty image path"
	}

	ext := strings.ToLower(filepath.Ext(item.Image))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		// supported static image
	default:
		return nil, fmt.Sprintf("skipped non-image media (%s)", ext)
	}

	imageURL := pr0grammImgBase + item.Image

	// Pre-check: if we already have a post with this exact source URL, skip the
	// download entirely.  This is a fast indexed DB query vs a full CDN fetch.
	if s.store != nil {
		exists, err := s.store.PostURLExists(ctx, imageURL)
		if err == nil && exists {
			return nil, "already imported (url exists)"
		}
	}

	tmpPath, err := downloadPr0grammFile(imageURL, s.dirs.Tmp)
	if err != nil {
		return nil, "download failed: " + err.Error()
	}
	defer os.Remove(tmpPath)

	post, err := s.processAndInsertPostCtx(ctx, imageURL, tmpPath, userName)
	if err != nil {
		return nil, err.Error()
	}

	return &pr0grammImportResult{
		SourceID: item.ID,
		PostID:   post.ID,
		Image:    item.Image,
	}, ""
}

// ── Pr0gramm API helpers ──────────────────────────────────────────────────────

// fetchPr0grammPage calls the pr0gramm items API. When older > 0 the `older`
// cursor is included so that the API returns items older than that promoted ID.
func fetchPr0grammPage(tags string, flags int, older int64) (*pr0grammResponse, error) {
	params := url.Values{}
	params.Set("tags", tags)
	params.Set("flags", fmt.Sprintf("%d", flags))
	params.Set("promoted", "1")
	params.Set("show_junk", "0")
	if older > 0 {
		params.Set("older", fmt.Sprintf("%d", older))
	}

	apiURL := pr0grammAPIBase + "?" + params.Encode()

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ginbar-importer/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := pr0grammClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", apiURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("pr0gramm API returned HTTP %d: %s", resp.StatusCode, body)
	}

	var result pr0grammResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// downloadPr0grammFile downloads a file from the pr0gramm image CDN into dir.
func downloadPr0grammFile(imageURL, dir string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, imageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ginbar-importer/1.0)")
	req.Header.Set("Referer", "https://pr0gramm.com/")

	resp, err := pr0grammClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", imageURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d for %s", resp.StatusCode, imageURL)
	}

	filename := filepath.Base(imageURL)
	if filename == "" || filename == "." {
		filename = fmt.Sprintf("pr0gramm_%d", time.Now().UnixNano())
	}
	dst := filepath.Join(dir, filename)

	f, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", dst, err)
	}
	defer f.Close()

	if _, err = io.Copy(f, resp.Body); err != nil {
		_ = os.Remove(dst)
		return "", fmt.Errorf("write %s: %w", dst, err)
	}

	return dst, nil
}

// minPromoted returns the minimum promoted timestamp from a slice of items.
func minPromoted(items []pr0grammItem) int64 {
	if len(items) == 0 {
		return 0
	}
	m := items[0].Promoted
	for _, item := range items[1:] {
		if item.Promoted < m {
			m = item.Promoted
		}
	}
	return m
}
