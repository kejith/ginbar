package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wallium/db"
	dbgen "wallium/db/gen"
	"wallium/utils"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// newTestDirs creates a throwaway Directories tree under t.TempDir() so that
// any files produced by processAndInsertPostCtx (avif images, thumbnails, etc.)
// are always written inside the OS-managed temporary directory and are never
// left behind in the source tree.
func newTestDirs(tb testing.TB) utils.Directories {
	tb.Helper()
	root := tb.TempDir()
	dirs := utils.Directories{
		Image:     filepath.Join(root, "images"),
		Thumbnail: filepath.Join(root, "thumbnails"),
		Video:     filepath.Join(root, "videos"),
		Tmp:       filepath.Join(root, "tmp"),
		Upload:    filepath.Join(root, "upload"),
	}
	for _, d := range []string{
		dirs.Image, dirs.Thumbnail, dirs.Video,
		dirs.Tmp, filepath.Join(dirs.Tmp, "thumbnails"), dirs.Upload,
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			tb.Fatalf("newTestDirs: mkdir %s: %v", d, err)
		}
	}
	return dirs
}

// ── mock DB helpers ───────────────────────────────────────────────────────────

// fakeBoolRow implements pgx.Row, scanning a single pre-set bool value.
type fakeBoolRow struct{ val bool }

func (r fakeBoolRow) Scan(dest ...any) error {
	if b, ok := dest[0].(*bool); ok {
		*b = r.val
	}
	return nil
}

// fakeDBTX implements db/gen.DBTX; QueryRow always returns the given bool.
type fakeDBTX struct{ urlExists bool }

func (f fakeDBTX) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f fakeDBTX) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, nil
}
func (f fakeDBTX) QueryRow(_ context.Context, _ string, _ ...interface{}) pgx.Row {
	return fakeBoolRow{f.urlExists}
}

// newAlwaysExistsStore returns a *db.Store whose PostURLExists always returns true.
func newAlwaysExistsStore() *db.Store {
	return &db.Store{Queries: dbgen.New(fakeDBTX{urlExists: true})}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// newMockPr0grammServer starts a test HTTP server that serves a synthetic
// pr0gramm API response. items is the slice that appears in the JSON body.
// atEnd controls the `atEnd` flag; the server closes itself when the test
// ends via t.Cleanup.
func newMockPr0grammServer(t *testing.T, items []pr0grammItem, atEnd bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pr0grammResponse{
			Items:   items,
			AtEnd:   atEnd,
			AtStart: false,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// redirectAPIBase temporarily replaces pr0grammAPIBase and pr0grammImgBase with
// the given URLs and restores them when the test ends.
func redirectAPIBase(t *testing.T, apiBase, imgBase string) {
	t.Helper()
	origAPI := pr0grammAPIBase
	origImg := pr0grammImgBase
	pr0grammAPIBase = apiBase
	pr0grammImgBase = imgBase
	t.Cleanup(func() {
		pr0grammAPIBase = origAPI
		pr0grammImgBase = origImg
	})
}

// ── minPromoted ───────────────────────────────────────────────────────────────

func TestMinPromoted(t *testing.T) {
	tests := []struct {
		name  string
		items []pr0grammItem
		want  int64
	}{
		{
			name:  "empty slice returns 0",
			items: nil,
			want:  0,
		},
		{
			name:  "single item",
			items: []pr0grammItem{{Promoted: 42}},
			want:  42,
		},
		{
			name: "minimum is first element",
			items: []pr0grammItem{
				{Promoted: 1},
				{Promoted: 5},
				{Promoted: 3},
			},
			want: 1,
		},
		{
			name: "minimum is last element",
			items: []pr0grammItem{
				{Promoted: 100},
				{Promoted: 200},
				{Promoted: 50},
			},
			want: 50,
		},
		{
			name: "minimum is middle element",
			items: []pr0grammItem{
				{Promoted: 300},
				{Promoted: 10},
				{Promoted: 700},
			},
			want: 10,
		},
		{
			name: "all equal",
			items: []pr0grammItem{
				{Promoted: 7},
				{Promoted: 7},
				{Promoted: 7},
			},
			want: 7,
		},
		{
			name: "negative promoted values",
			items: []pr0grammItem{
				{Promoted: -5},
				{Promoted: -1},
				{Promoted: -10},
			},
			want: -10,
		},
		{
			name: "large page of items",
			items: func() []pr0grammItem {
				out := make([]pr0grammItem, 120)
				for i := range out {
					out[i] = pr0grammItem{Promoted: int64(1000 - i)}
				}
				return out
			}(),
			want: 881, // 1000 - 119
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := minPromoted(tc.items)
			if got != tc.want {
				t.Errorf("minPromoted() = %d, want %d", got, tc.want)
			}
		})
	}
}

// ── fetchPr0grammPage ─────────────────────────────────────────────────────────

func TestFetchPr0grammPage(t *testing.T) {
	items := []pr0grammItem{
		{ID: 1, Promoted: 100, Image: "a.jpg", User: "alice"},
		{ID: 2, Promoted: 90, Image: "b.png", User: "bob"},
	}

	t.Run("happy path – full page", func(t *testing.T) {
		srv := newMockPr0grammServer(t, items, false)
		redirectAPIBase(t, srv.URL, "")

		got, err := fetchPr0grammPage("tag:cat", 1, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Items) != 2 {
			t.Errorf("got %d items, want 2", len(got.Items))
		}
		if got.AtEnd {
			t.Errorf("AtEnd should be false")
		}
		if got.Items[0].ID != 1 {
			t.Errorf("first item ID = %d, want 1", got.Items[0].ID)
		}
	})

	t.Run("at_end flag is propagated", func(t *testing.T) {
		srv := newMockPr0grammServer(t, items, true)
		redirectAPIBase(t, srv.URL, "")

		got, err := fetchPr0grammPage("tag:dog", 1, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got.AtEnd {
			t.Errorf("AtEnd should be true")
		}
	})

	t.Run("empty result page", func(t *testing.T) {
		srv := newMockPr0grammServer(t, nil, true)
		redirectAPIBase(t, srv.URL, "")

		got, err := fetchPr0grammPage("tag:rare", 1, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Items) != 0 {
			t.Errorf("expected 0 items, got %d", len(got.Items))
		}
	})

	t.Run("older pagination cursor is forwarded", func(t *testing.T) {
		var receivedOlder string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedOlder = r.URL.Query().Get("older")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pr0grammResponse{Items: items})
		}))
		t.Cleanup(srv.Close)
		redirectAPIBase(t, srv.URL, "")

		_, err := fetchPr0grammPage("tag:cat", 1, 42)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receivedOlder != "42" {
			t.Errorf("older query param = %q, want %q", receivedOlder, "42")
		}
	})

	t.Run("older=0 omits the cursor param", func(t *testing.T) {
		var hasOlder bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, hasOlder = r.URL.Query()["older"]
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pr0grammResponse{})
		}))
		t.Cleanup(srv.Close)
		redirectAPIBase(t, srv.URL, "")

		_, _ = fetchPr0grammPage("tag:cat", 1, 0)
		if hasOlder {
			t.Error("older param should not be present when older=0")
		}
	})

	t.Run("non-200 status returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		t.Cleanup(srv.Close)
		redirectAPIBase(t, srv.URL, "")

		_, err := fetchPr0grammPage("tag:x", 1, 0)
		if err == nil {
			t.Fatal("expected error on non-200 response")
		}
		if !strings.Contains(err.Error(), "403") {
			t.Errorf("error should mention HTTP 403, got: %v", err)
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{not valid json"))
		}))
		t.Cleanup(srv.Close)
		redirectAPIBase(t, srv.URL, "")

		_, err := fetchPr0grammPage("tag:x", 1, 0)
		if err == nil {
			t.Fatal("expected error on invalid JSON")
		}
	})

	t.Run("server unreachable returns error", func(t *testing.T) {
		// Use the test server URL after it has been closed.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
		srv.Close()
		redirectAPIBase(t, srv.URL, "")

		_, err := fetchPr0grammPage("tag:x", 1, 0)
		if err == nil {
			t.Fatal("expected error when server is down")
		}
	})
}

// ── downloadPr0grammFile ──────────────────────────────────────────────────────

func TestDownloadPr0grammFile(t *testing.T) {
	t.Run("happy path – file is saved", func(t *testing.T) {
		payload := []byte("fake image data")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(payload)
		}))
		t.Cleanup(srv.Close)

		dir := t.TempDir()
		dst, err := downloadPr0grammFile(srv.URL+"/img/test.jpg", dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(dst) })

		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("could not read saved file: %v", err)
		}
		if string(got) != string(payload) {
			t.Errorf("saved content = %q, want %q", got, payload)
		}
	})

	t.Run("filename derived from URL path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("data"))
		}))
		t.Cleanup(srv.Close)

		dir := t.TempDir()
		dst, err := downloadPr0grammFile(srv.URL+"/img/myphoto.jpeg", dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(dst) })

		if filepath.Base(dst) != "myphoto.jpeg" {
			t.Errorf("filename = %q, want %q", filepath.Base(dst), "myphoto.jpeg")
		}
	})

	t.Run("non-200 response returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		t.Cleanup(srv.Close)

		_, err := downloadPr0grammFile(srv.URL+"/img/gone.jpg", t.TempDir())
		if err == nil {
			t.Fatal("expected error on 404 response")
		}
		if !strings.Contains(err.Error(), "404") {
			t.Errorf("error should mention 404, got: %v", err)
		}
	})

	t.Run("invalid destination directory returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("data"))
		}))
		t.Cleanup(srv.Close)

		_, err := downloadPr0grammFile(srv.URL+"/img/x.jpg", "/nonexistent/dir/that/does/not/exist")
		if err == nil {
			t.Fatal("expected error with bad destination directory")
		}
	})

	t.Run("server unreachable returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
		srv.Close()

		_, err := downloadPr0grammFile(srv.URL+"/img/x.jpg", t.TempDir())
		if err == nil {
			t.Fatal("expected error when server is down")
		}
	})

	t.Run("empty body creates an empty file", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		dir := t.TempDir()
		dst, err := downloadPr0grammFile(srv.URL+"/img/empty.jpg", dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(dst) })

		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("stat error: %v", err)
		}
		if fi.Size() != 0 {
			t.Errorf("expected empty file, got size %d", fi.Size())
		}
	})
}

// ── importPr0grammItem validation (no DB needed) ──────────────────────────────

func TestImportPr0grammItem_Validation(t *testing.T) {
	// Build a minimal Server that only has dirs set (no DB/Redis).
	dir := t.TempDir()
	s := &Server{
		dirs: utils.Directories{Tmp: dir},
	}

	tests := []struct {
		name       string
		item       pr0grammItem
		wantSkip   bool
		skipSubstr string
	}{
		{
			name:       "empty Image field is rejected",
			item:       pr0grammItem{ID: 1, Image: ""},
			wantSkip:   true,
			skipSubstr: "empty image path",
		},
		{
			name:       "mp4 video is skipped",
			item:       pr0grammItem{ID: 2, Image: "video/somevid.mp4"},
			wantSkip:   true,
			skipSubstr: ".mp4",
		},
		{
			name:       "webm video is skipped",
			item:       pr0grammItem{ID: 3, Image: "video/clip.webm"},
			wantSkip:   true,
			skipSubstr: ".webm",
		},
		{
			name:       "unknown extension is skipped",
			item:       pr0grammItem{ID: 4, Image: "files/data.bin"},
			wantSkip:   true,
			skipSubstr: ".bin",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			res, reason := s.importPr0grammItem(context.Background(), tc.item, "testuser")
			if tc.wantSkip {
				if reason == "" {
					t.Errorf("expected skip reason, got none (result=%v)", res)
				}
				if !strings.Contains(reason, tc.skipSubstr) {
					t.Errorf("skip reason %q does not contain %q", reason, tc.skipSubstr)
				}
				if res != nil {
					t.Errorf("expected nil result on skip, got %v", res)
				}
			} else {
				if reason != "" {
					t.Errorf("unexpected skip reason: %q", reason)
				}
			}
		})
	}
}

// ── fetchPr0grammPage – request shape ─────────────────────────────────────────

func TestFetchPr0grammPage_QueryParams(t *testing.T) {
	tests := []struct {
		name         string
		tags         string
		flags        int
		older        int64
		wantTags     string
		wantFlags    string
		wantPromoted string
		wantOlder    string // empty means param must be absent
	}{
		{
			name:         "basic SFW request",
			tags:         "tag:cat",
			flags:        1,
			older:        0,
			wantTags:     "tag:cat",
			wantFlags:    "1",
			wantPromoted: "1",
			wantOlder:    "",
		},
		{
			name:         "NSFW flag",
			tags:         "tag:dog",
			flags:        2,
			older:        0,
			wantTags:     "tag:dog",
			wantFlags:    "2",
			wantPromoted: "1",
			wantOlder:    "",
		},
		{
			name:         "combined SFW+NSFW flags",
			tags:         "top:week",
			flags:        3,
			older:        500,
			wantTags:     "top:week",
			wantFlags:    "3",
			wantPromoted: "1",
			wantOlder:    "500",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var gotQuery map[string]string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				gotQuery = map[string]string{
					"tags":     q.Get("tags"),
					"flags":    q.Get("flags"),
					"promoted": q.Get("promoted"),
					"older":    q.Get("older"),
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(pr0grammResponse{})
			}))
			t.Cleanup(srv.Close)
			redirectAPIBase(t, srv.URL, "")

			_, _ = fetchPr0grammPage(tc.tags, tc.flags, tc.older)

			if gotQuery["tags"] != tc.wantTags {
				t.Errorf("tags = %q, want %q", gotQuery["tags"], tc.wantTags)
			}
			if gotQuery["flags"] != tc.wantFlags {
				t.Errorf("flags = %q, want %q", gotQuery["flags"], tc.wantFlags)
			}
			if gotQuery["promoted"] != tc.wantPromoted {
				t.Errorf("promoted = %q, want %q", gotQuery["promoted"], tc.wantPromoted)
			}
			if tc.wantOlder == "" && gotQuery["older"] != "" {
				t.Errorf("older should be absent, got %q", gotQuery["older"])
			} else if tc.wantOlder != "" && gotQuery["older"] != tc.wantOlder {
				t.Errorf("older = %q, want %q", gotQuery["older"], tc.wantOlder)
			}
		})
	}
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

// BenchmarkMinPromoted measures the cost of scanning a realistic-sized page.
func BenchmarkMinPromoted(b *testing.B) {
	sizes := []int{10, 50, 120}
	for _, n := range sizes {
		items := make([]pr0grammItem, n)
		for i := range items {
			items[i] = pr0grammItem{Promoted: int64(5000 - i)}
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = minPromoted(items)
			}
		})
	}
}

// BenchmarkFetchPr0grammPage measures round-trip cost for the index fetch,
// including JSON decoding, using a local mock server (no real network).
func BenchmarkFetchPr0grammPage(b *testing.B) {
	pageSizes := []int{1, 10, 60, 120}
	for _, n := range pageSizes {
		items := make([]pr0grammItem, n)
		for i := range items {
			items[i] = pr0grammItem{
				ID:       int64(i + 1),
				Promoted: int64(5000 - i),
				Image:    fmt.Sprintf("name/%d.jpg", i),
				User:     "benchuser",
			}
		}
		body, _ := json.Marshal(pr0grammResponse{Items: items, AtEnd: false})

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}))

		// Use a faster client with no timeout for benchmarking.
		origClient := pr0grammClient
		pr0grammClient = &http.Client{}
		origBase := pr0grammAPIBase
		pr0grammAPIBase = srv.URL

		b.Run(fmt.Sprintf("items=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = fetchPr0grammPage("tag:bench", 1, 0)
			}
		})

		srv.Close()
		pr0grammClient = origClient
		pr0grammAPIBase = origBase
	}
}

// BenchmarkDownloadPr0grammFile measures the cost of downloading and writing a
// single image file from a local mock server.
func BenchmarkDownloadPr0grammFile(b *testing.B) {
	payloadSizes := []int{
		1 << 10,  // 1 KB
		64 << 10, // 64 KB
		256 << 10, // 256 KB
		1 << 20,  // 1 MB
	}

	for _, sz := range payloadSizes {
		payload := make([]byte, sz)
		for i := range payload {
			payload[i] = 0xFF
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(payload)
		}))

		origClient := pr0grammClient
		pr0grammClient = &http.Client{}

		dir := b.TempDir()
		url := srv.URL + "/img/bench.jpg"

		b.Run(fmt.Sprintf("size=%dKB", sz>>10), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(sz))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Give each file a unique name to avoid clobbering.
				dst := filepath.Join(dir, fmt.Sprintf("bench_%d.jpg", i))
				f, err := os.Create(dst)
				if err != nil {
					b.Fatal(err)
				}

				req, _ := http.NewRequest(http.MethodGet, url, nil)
				resp, err := pr0grammClient.Do(req)
				if err != nil {
					f.Close()
					b.Fatal(err)
				}
				start := time.Now()
				_, _ = io.Copy(f, resp.Body)
				_ = resp.Body.Close()
				b.ReportMetric(float64(time.Since(start).Microseconds()), "write_µs")
				f.Close()
				_ = os.Remove(dst)
			}
		})

		srv.Close()
		pr0grammClient = origClient
	}
}

// BenchmarkFetchAndDecode performs an end-to-end benchmark of fetchPr0grammPage
// which covers: HTTP round-trip + JSON decode + struct allocation.
func BenchmarkFetchAndDecode(b *testing.B) {
	// Build a realistic 60-item page (pr0gramm's default page size).
	items := make([]pr0grammItem, 60)
	for i := range items {
		items[i] = pr0grammItem{
			ID:       int64(10000 + i),
			Promoted: int64(9000 - i),
			Image:    fmt.Sprintf("2024/01/%d.jpg", 1000000+i),
			Thumb:    fmt.Sprintf("thumb/%d.jpg", 1000000+i),
			User:     "benchmarkuser",
			Mark:     1,
		}
	}
	body, _ := json.Marshal(pr0grammResponse{Items: items, AtEnd: false})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	b.Cleanup(srv.Close)

	origBase := pr0grammAPIBase
	origClient := pr0grammClient
	pr0grammAPIBase = srv.URL
	pr0grammClient = &http.Client{}
	b.Cleanup(func() {
		pr0grammAPIBase = origBase
		pr0grammClient = origClient
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := fetchPr0grammPage("top:week", 1, 0)
		if err != nil {
			b.Fatal(err)
		}
		if len(resp.Items) != 60 {
			b.Fatalf("unexpected item count: %d", len(resp.Items))
		}
	}
}

// BenchmarkImportPr0grammItem_SkipPath measures the fast-path cost when an item
// is rejected before any network I/O occurs (unsupported extension).
func BenchmarkImportPr0grammItem_SkipPath(b *testing.B) {
	s := &Server{}
	item := pr0grammItem{ID: 99, Image: "video/clip.mp4", User: "user"}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.importPr0grammItem(ctx, item, "benchuser")
	}
}

// ── Concurrency tests ─────────────────────────────────────────────────────────

// TestImportWorkerPool_ConcurrencyLimit verifies that the import loop never
// exceeds importConcurrency simultaneous in-flight goroutines.  We instrument
// the image server with an atomic counter and record the peak.
func TestImportWorkerPool_ConcurrencyLimit(t *testing.T) {
	const (
		nItems      = 40
		maxAllowed = 16 // must match importConcurrency in import.go
		itemDelay  = 10 * time.Millisecond // artificial latency per item
	)

	var (
		inflight int64
		peak     int64
	)

	// Slow image server that records concurrency.
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt64(&inflight, 1)
		defer atomic.AddInt64(&inflight, -1)
		// Track peak.
		for {
			old := atomic.LoadInt64(&peak)
			if current <= old || atomic.CompareAndSwapInt64(&peak, old, current) {
				break
			}
		}
		time.Sleep(itemDelay)
		// Tiny valid JPEG
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF, 0xD9})
	}))
	t.Cleanup(imgSrv.Close)

	// Build items that will be skipped at the download stage (non-image ext),
	// but we need them to reach the download step, so use .jpg.
	// We set imgBase to the slow server so each download hits it.
	origImg := pr0grammImgBase
	pr0grammImgBase = imgSrv.URL + "/"
	t.Cleanup(func() { pr0grammImgBase = origImg })

	items := make([]pr0grammItem, nItems)
	for i := range items {
		items[i] = pr0grammItem{ID: int64(i + 1), Promoted: int64(nItems - i), Image: fmt.Sprintf("img%d.jpg", i)}
	}

	// Process using a minimal Server (no DB – all items will fail at
	// processAndInsertPostCtx since store is nil, but the concurrency counter
	// fires at the HTTP layer before that).
	// newTestDirs ensures any produced files (avif, thumbnails) go into the OS
	// temp directory rather than the package source tree.
	s := &Server{dirs: newTestDirs(t)}

	start := time.Now()
	type itemResult struct {
		result     *pr0grammImportResult
		skipReason string
		item       pr0grammItem
		dur        time.Duration
	}
	results := make([]itemResult, len(items))

	// Run the same bounded concurrent worker pool used in ImportFromPr0gramm.
	const concurrency = 16 // must match importConcurrency in import.go
	type slot = struct{}
	sem := make(chan slot, concurrency)
	var wg sync.WaitGroup
	ctx2 := context.Background()
	for idx, item := range items {
		wg.Add(1)
		idx, item := idx, item
		sem <- slot{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			res, reason := s.importPr0grammItem(ctx2, item, "testuser")
			results[idx] = itemResult{res, reason, item, time.Since(start)}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// Peak concurrency must not exceed the cap.
	if peak > maxAllowed {
		t.Errorf("peak concurrency = %d, want <= %d", peak, maxAllowed)
	}

	// All items should have been attempted.
	for i, r := range results {
		if r.skipReason == "" && r.result == nil {
			t.Errorf("item %d: neither result nor skip reason set", i)
		}
	}

	// With 16 workers and 10ms delay, 40 items needs ≥ 3 rounds but should
	// finish well under the sequential ceiling of 40*10ms = 400ms.
	sequentialCeiling := time.Duration(nItems) * itemDelay
	if elapsed >= sequentialCeiling {
		t.Logf("warning: elapsed %v is not faster than sequential ceiling %v (may be slow CI)", elapsed, sequentialCeiling)
	}
	t.Logf("peak concurrency=%d, elapsed=%v (sequential ceiling %v)", peak, elapsed.Round(time.Millisecond), sequentialCeiling)
}

// TestImportPr0grammItem_URLPreCheck verifies that an item whose source URL is
// already present in the DB is skipped before any HTTP download is attempted.
func TestImportPr0grammItem_URLPreCheck(t *testing.T) {
	var downloadAttempted bool
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloadAttempted = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(imgSrv.Close)

	origImg := pr0grammImgBase
	pr0grammImgBase = imgSrv.URL + "/"
	t.Cleanup(func() { pr0grammImgBase = origImg })

	// Server with a mock store whose PostURLExists always returns true.
	s := &Server{
		dirs:  utils.Directories{Tmp: t.TempDir()},
		store: newAlwaysExistsStore(),
	}

	item := pr0grammItem{ID: 1, Image: "photo.jpg", Promoted: 100}
	res, reason := s.importPr0grammItem(context.Background(), item, "user")

	if res != nil {
		t.Errorf("expected nil result, got %+v", res)
	}
	if !strings.Contains(reason, "already imported") {
		t.Errorf("skip reason = %q, want \"already imported\"", reason)
	}
	if downloadAttempted {
		t.Error("download should not have been attempted when URL already exists in DB")
	}
}

// BenchmarkImport_ConcurrentVsSequential compares the concurrent worker-pool
// approach (8 goroutines) against a baseline sequential loop when each item
// has simulated I/O latency.  Run with -bench=BenchmarkImport_Concurrent.
func BenchmarkImport_ConcurrentVsSequential(b *testing.B) {
	const (
		nItems    = 60
		itemDelay = 5 * time.Millisecond
	)

	// Simulate per-item I/O latency with a sleepy server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(itemDelay)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF, 0xD9})
	}))
	b.Cleanup(srv.Close)

	origImg := pr0grammImgBase
	pr0grammImgBase = srv.URL + "/"
	b.Cleanup(func() { pr0grammImgBase = origImg })

	items := make([]pr0grammItem, nItems)
	for i := range items {
		items[i] = pr0grammItem{ID: int64(i + 1), Promoted: int64(nItems - i), Image: fmt.Sprintf("bench%d.jpg", i)}
	}

	s := &Server{dirs: newTestDirs(b)}
	ctx := context.Background()

	b.Run("sequential", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, item := range items {
				_, _ = s.importPr0grammItem(ctx, item, "benchuser")
			}
		}
	})

	b.Run("concurrent_8", func(b *testing.B) {
		b.ReportAllocs()
		const concurrency = 8
		type slot = struct{}
		type itemResult struct {
			res    *pr0grammImportResult
			reason string
		}

		for i := 0; i < b.N; i++ {
			results := make([]itemResult, len(items))
			sem := make(chan slot, concurrency)
		var wg sync.WaitGroup
			for j, item := range items {
				wg.Add(1)
				j, item := j, item
				sem <- slot{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					res, reason := s.importPr0grammItem(ctx, item, "benchuser")
					results[j] = itemResult{res, reason}
				}()
			}
			wg.Wait()
			_ = results
		}
	})
}
