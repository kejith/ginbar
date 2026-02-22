package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wallium/db"
	"wallium/utils"

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
// Tags is passed directly to the pr0gramm search API.
Tags string `json:"tags" form:"tags"`
// Flags controls content filtering: 1=SFW, 2=NSFW, 4=NSFL (bitwise OR).
Flags int `json:"flags" form:"flags"`
// MaxPages caps the number of pr0gramm pages to fetch.  Defaults to 5.
MaxPages int `json:"maxPages" form:"maxPages"`
}

// ── Import results (kept for test / legacy compatibility) ─────────────────────

type pr0grammImportResult struct {
SourceID int64  `json:"source_id"`
PostID   int32  `json:"post_id"`
Image    string `json:"image"`
}

// importConcurrency caps simultaneous download+process goroutines.
// Must match the constant referenced in import_test.go.
const importConcurrency = 16

// pr0grammPageDelay is the minimum pause between successive API page requests
// to avoid triggering rate limiting.
const pr0grammPageDelay = 500 * time.Millisecond

// pr0grammMaxRetries is the number of times a page fetch is retried on HTTP 429.
const pr0grammMaxRetries = 4

// ── SSE event shapes ──────────────────────────────────────────────────────────

type ssePhase string

const (
phFetching   ssePhase = "fetching"
phInserted   ssePhase = "inserted"
phProcessing ssePhase = "processing"
phDone       ssePhase = "done"
phError      ssePhase = "error"
)

type sseFetchingEvent struct {
Phase        ssePhase `json:"phase"`
Page         int      `json:"page"`
MaxPages     int      `json:"max_pages"`
TotalRead    int      `json:"total_read"`
AtEnd        bool     `json:"at_end"`
SuccessPages int      `json:"success_pages"`
FailedPages  int      `json:"failed_pages"`
}

type sseInsertedEvent struct {
Phase         ssePhase `json:"phase"`
Total         int      `json:"total"`
FilteredExt   int      `json:"filtered_ext"`   // unsupported file extension
SkippedDedup  int      `json:"skipped_dedup"`  // URL already in DB
InsertErrors  int      `json:"insert_errors"`  // CreateDirtyPost failures
}

type sseProcessingEvent struct {
Phase     ssePhase `json:"phase"`
Total     int      `json:"total"`
Processed int      `json:"processed"`
Imported  int      `json:"imported"`
Failed    int      `json:"failed"`
}

type sseDoneEvent struct {
Phase    ssePhase `json:"phase"`
Total    int      `json:"total"`
Imported int      `json:"imported"`
Failed   int      `json:"failed"`
}

type sseErrorEvent struct {
Phase   ssePhase `json:"phase"`
Message string   `json:"message"`
}

// ── SSE wire helper ───────────────────────────────────────────────────────────

// writeSSE marshals v as a JSON SSE data frame and flushes immediately.
func writeSSE(w *bufio.Writer, v any) {
b, _ := json.Marshal(v)
fmt.Fprintf(w, "data: %s\n\n", b)
w.Flush()
}

// ── Handler ───────────────────────────────────────────────────────────────────

// ImportFromPr0gramm runs a multi-phase, SSE-streamed pr0gramm import.
//
// Phases
//  1. Fetch all JSON pages from pr0gramm (up to maxPages)  → "fetching" events per page.
//  2. Batch-dedup; insert dirty placeholder rows           → one "inserted" event.
//  3. Download + process each row concurrently             → "processing" event per item.
//  4. Emit "done".
//
//POST /api/post/import/pr0gramm
//Content-Type: application/json
//Body: { "tags": "...", "flags": 1, "maxPages": 5 }
//Response: text/event-stream  (SSE)
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
maxPages := form.MaxPages
if maxPages <= 0 {
maxPages = 5
}
if maxPages > 50 {
maxPages = 50
}

tags := strings.TrimSpace(form.Tags)
userName := u.Name

// Bind the correlation ID to every log line this import produces.
reqID, _ := c.Locals("request_id").(string)
log := s.log.With("request_id", reqID)

// SSE headers must be set before SendStreamWriter.
c.Set("Content-Type", "text/event-stream")
c.Set("Cache-Control", "no-cache")
c.Set("Connection", "keep-alive")
c.Set("X-Accel-Buffering", "no")

c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
ctx := context.Background()

log.InfoContext(ctx, "pr0gramm import started",
slog.String("tags", tags),
slog.Int("flags", flags),
slog.Int("max_pages", maxPages),
slog.String("user", userName),
)

// ── Phase 1: Fetch all pr0gramm JSON pages ────────────────────────────
var allItems []pr0grammItem
older := int64(0)

successPages := 0
failedPages := 0

for page := 1; page <= maxPages; page++ {
resp, fetchErr := fetchPr0grammPage(tags, flags, older)
if fetchErr != nil {
failedPages++
log.ErrorContext(ctx, "pr0gramm page fetch failed",
slog.Int("page", page),
slog.Int("success_pages_so_far", successPages),
slog.Int("failed_pages_so_far", failedPages),
slog.Any("err", fetchErr))
writeSSE(w, sseFetchingEvent{
Phase:        phFetching,
Page:         page,
MaxPages:     maxPages,
TotalRead:    len(allItems),
AtEnd:        false,
SuccessPages: successPages,
FailedPages:  failedPages,
})
// Do not abort — continue to next page so one bad page doesn't kill the import.
continue
}

successPages++
allItems = append(allItems, resp.Items...)
older = minPromoted(resp.Items)
atEnd := resp.AtEnd || len(resp.Items) == 0

writeSSE(w, sseFetchingEvent{
Phase:        phFetching,
Page:         page,
MaxPages:     maxPages,
TotalRead:    len(allItems),
AtEnd:        atEnd,
SuccessPages: successPages,
FailedPages:  failedPages,
})

log.InfoContext(ctx, "pr0gramm page fetched",
slog.Int("page", page),
slog.Int("items_this_page", len(resp.Items)),
slog.Int("total_so_far", len(allItems)),
slog.Int("success_pages", successPages),
slog.Int("failed_pages", failedPages),
slog.Bool("api_at_end", resp.AtEnd),
slog.Bool("at_end", atEnd),
)

if atEnd {
log.InfoContext(ctx, "pr0gramm fetch stopped",
slog.Int("page", page),
slog.Int("success_pages", successPages),
slog.Int("failed_pages", failedPages),
slog.String("reason", func() string {
if resp.AtEnd {
return "pr0gramm API returned atEnd=true"
}
return "empty page"
}()),
)
break
}
}

log.InfoContext(ctx, "pr0gramm fetch phase complete",
slog.Int("success_pages", successPages),
slog.Int("failed_pages", failedPages),
slog.Int("total_items", len(allItems)),
)

// ── Phase 2: Filter, batch dedup, insert dirty rows ───────────────────
type candidate struct {
item     pr0grammItem
imageURL string
}

// Drop non-image extensions immediately.
filteredExt := 0
var candidates []candidate
for _, item := range allItems {
if item.Image == "" {
filteredExt++
continue
}
ext := strings.ToLower(filepath.Ext(item.Image))
switch ext {
case ".jxl", ".avif", ".webp", ".jpg", ".gif":
// supported
default:
log.DebugContext(ctx, "pr0gramm item skipped: unsupported extension",
slog.String("image", item.Image),
slog.String("ext", ext),
)
filteredExt++
continue
}
candidates = append(candidates, candidate{
item:     item,
imageURL: pr0grammImgBase + item.Image,
})
}
log.InfoContext(ctx, "pr0gramm extension filter",
slog.Int("raw_items", len(allItems)),
slog.Int("filtered_ext", filteredExt),
slog.Int("candidates", len(candidates)),
)

allURLs := make([]string, len(candidates))
for i, c := range candidates {
allURLs[i] = c.imageURL
}

skippedDedup := 0
var toProcess []candidate

if s.store != nil {
existing, dedupErr := s.store.FilterExistingURLs(ctx, allURLs)
if dedupErr != nil {
log.WarnContext(ctx, "batch URL dedup failed, proceeding without dedup",
slog.Any("err", dedupErr))
toProcess = candidates
} else {
for _, c := range candidates {
if existing[c.imageURL] {
skippedDedup++
} else {
toProcess = append(toProcess, c)
}
}
}
} else {
toProcess = candidates
}
log.InfoContext(ctx, "pr0gramm dedup",
slog.Int("candidates", len(candidates)),
slog.Int("skipped_dedup", skippedDedup),
slog.Int("to_process", len(toProcess)),
)

// Insert placeholder dirty rows (one per new URL).
type dirtyEntry struct {
postID   int32
item     pr0grammItem
imageURL string
}

insertErrors := 0
var dirtyPosts []dirtyEntry
for _, cand := range toProcess {
if s.store == nil {
break
}
post, insertErr := s.store.CreateDirtyPost(ctx, cand.imageURL, userName)
if insertErr != nil {
log.WarnContext(ctx, "failed to insert dirty post",
slog.String("url", cand.imageURL),
slog.Any("err", insertErr))
insertErrors++
continue
}
dirtyPosts = append(dirtyPosts, dirtyEntry{
postID:   post.ID,
item:     cand.item,
imageURL: cand.imageURL,
})
}

total := len(dirtyPosts)
writeSSE(w, sseInsertedEvent{
Phase:        phInserted,
Total:        total,
FilteredExt:  filteredExt,
SkippedDedup: skippedDedup,
InsertErrors: insertErrors,
})

log.InfoContext(ctx, "pr0gramm dirty posts inserted",
slog.Int("total_queued", total),
slog.Int("filtered_ext", filteredExt),
slog.Int("skipped_dedup", skippedDedup),
slog.Int("insert_errors", insertErrors),
)

// ── Hand off to background process queue ──────────────────────────────
s.queue.Notify()

writeSSE(w, sseDoneEvent{
Phase:    phDone,
Total:    total,
Imported: 0,
Failed:   0,
})

log.InfoContext(ctx, "pr0gramm import: dirty posts queued",
slog.Int("total_queued", total),
slog.Int("filtered_ext", filteredExt),
slog.Int("skipped_dedup", skippedDedup),
slog.Int("insert_errors", insertErrors),
)
})
	return nil
}

// processAndFinalizeDirtyPost downloads the image at imageURL, runs the image
// pipeline, and calls FinalizePost to make the dirty row visible.
// On any failure the placeholder row is deleted to free the URL for future imports.
func (s *Server) processAndFinalizeDirtyPost(ctx context.Context, postID int32, imageURL string) error {
tmpPath, err := downloadPr0grammFile(imageURL, s.dirs.Tmp)
if err != nil {
if s.store != nil {
_ = s.store.DeleteDirtyPost(ctx, postID)
}
return fmt.Errorf("download failed: %w", err)
}
defer os.Remove(tmpPath)

mimeType := mime.TypeByExtension(filepath.Ext(tmpPath))
fileType := strings.SplitN(mimeType, "/", 2)[0]
if fileType != "image" {
if s.store != nil {
_ = s.store.DeleteDirtyPost(ctx, postID)
}
return fmt.Errorf("unsupported file type: %s", mimeType)
}

res, err := utils.ProcessImage(tmpPath, s.dirs)
if err != nil {
if s.store != nil {
_ = s.store.DeleteDirtyPost(ctx, postID)
}
return fmt.Errorf("image processing failed: %w", err)
}

h := res.PerceptionHash.GetHash()

if s.store != nil {
if err := s.store.FinalizePost(ctx, db.FinalizePostParams{
ID:                postID,
Filename:          res.Filename,
ThumbnailFilename: res.ThumbnailFilename,
UploadedFilename:  res.UploadedFilename,
ContentType:       "image",
PHash0:            int64(h[0]),
PHash1:            int64(h[1]),
PHash2:            int64(h[2]),
PHash3:            int64(h[3]),
Width:             int32(res.Width),
Height:            int32(res.Height),
}); err != nil {
_ = s.store.DeleteDirtyPost(ctx, postID)
return fmt.Errorf("finalize post: %w", err)
}
}

return nil
}

// ── Legacy helper (kept for unit / benchmark test compatibility) ──────────────

// importPr0grammItem validates and inserts a single pr0gramm item as a regular
// (non-dirty) post.  Production imports now use the phased SSE handler above;
// this function is preserved because import_test.go exercises it directly.
func (s *Server) importPr0grammItem(ctx context.Context, item pr0grammItem, userName string) (*pr0grammImportResult, string) {
if item.Image == "" {
return nil, "empty image path"
}

ext := strings.ToLower(filepath.Ext(item.Image))
switch ext {
case ".jxl", ".avif", ".webp", ".jpg", ".gif":
// supported static image
default:
return nil, fmt.Sprintf("skipped non-image media (%s)", ext)
}

imageURL := pr0grammImgBase + item.Image

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

// fetchPr0grammPage calls the pr0gramm items API.  When older > 0 the cursor
// is included so the API returns items older than that promoted ID.
// The function automatically retries up to pr0grammMaxRetries times when the
// server responds with HTTP 429, honouring the Retry-After header when present
// and falling back to exponential backoff otherwise.
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

	for attempt := 0; attempt <= pr0grammMaxRetries; attempt++ {
		req, err := http.NewRequest(http.MethodGet, apiURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; wallium-importer/1.0)")
		req.Header.Set("Accept", "application/json")

		resp, err := pr0grammClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", apiURL, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			// Determine how long to wait before the next attempt.
			wait := rateLimitBackoff(resp, attempt)
			_ = resp.Body.Close()
			if attempt == pr0grammMaxRetries {
				return nil, fmt.Errorf("pr0gramm API rate limit exceeded after %d retries", pr0grammMaxRetries)
			}
			time.Sleep(wait)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			_ = resp.Body.Close()
			return nil, fmt.Errorf("pr0gramm API returned HTTP %d: %s", resp.StatusCode, body)
		}

		var result pr0grammResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("decode response: %w", err)
		}
		_ = resp.Body.Close()
		return &result, nil
	}

	// Unreachable, but keeps the compiler happy.
	return nil, fmt.Errorf("pr0gramm API rate limit exceeded after %d retries", pr0grammMaxRetries)
}

// rateLimitBackoff returns how long to wait after a 429 response.
// It reads the Retry-After header first; if absent it uses exponential backoff
// starting at 2 s and capped at 60 s.
func rateLimitBackoff(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		// Retry-After can be a delay-seconds integer or an HTTP-date; try integer first.
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
		if t, err := http.ParseTime(ra); err == nil {
			if d := time.Until(t); d > 0 {
				return d
			}
		}
	}
	// Exponential backoff: 2s, 4s, 8s, 16s … capped at 60s.
	backoff := time.Duration(math.Pow(2, float64(attempt+1))) * time.Second
	if backoff > 60*time.Second {
		backoff = 60 * time.Second
	}
	return backoff
}

// downloadPr0grammFile downloads a file from the pr0gramm image CDN into dir.
func downloadPr0grammFile(imageURL, dir string) (string, error) {
req, err := http.NewRequest(http.MethodGet, imageURL, nil)
if err != nil {
return "", err
}
req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; wallium-importer/1.0)")
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
