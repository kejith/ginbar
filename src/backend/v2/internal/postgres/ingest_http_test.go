package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/httpapi"
	"github.com/kejith/ginbar/backend/v2/internal/ingest"
	"github.com/kejith/ginbar/backend/v2/internal/profile"
)

func TestHTTPUploadCreatesAuthoritativeUnreleasedIngestion(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()

	ctx := context.Background()
	var userID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ('http-ingest-user') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var unrelatedUserID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ('unrelated-user') RETURNING id").Scan(&unrelatedUserID); err != nil {
		t.Fatal(err)
	}

	rawToken := bytes.Repeat([]byte{0x42}, 32)
	tokenHash := sha256.Sum256(rawToken)
	now := time.Now().UTC()
	if err := store.CreateSession(ctx, userID, tokenHash, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	mediaRoot := t.TempDir()
	localStore, err := ingest.NewLocalStore(mediaRoot, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ingestService, err := ingest.New(store, localStore, nil, ingest.Config{
		StageTimeout:   time.Second,
		DBTimeout:      time.Second,
		CleanupTimeout: time.Second,
		MaxConcurrent:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := httpapi.DefaultConfig()
	cfg.CookieSecure = false
	cfg.Ingest = ingestService
	cfg.IngestRequestTimeout = 2 * time.Second
	server := httpapi.NewWithConfig(store, cfg)

	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "image.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("real staged bytes")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://ginbar.test/api/v2/posts/upload?filter=sfw", bytes.NewReader(upload.Bytes()))
	req.Header.Set("Origin", "http://ginbar.test")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "ginbar_session", Value: base64.RawURLEncoding.EncodeToString(rawToken)})
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var created struct {
		PostID int64 `json:"postId"`
		JobID  int64 `json:"jobId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.PostID <= 0 || created.JobID <= 0 {
		t.Fatalf("created=%#v", created)
	}

	var authorID int64
	var releaseState int16
	if err := store.pool.QueryRow(ctx, "SELECT author_user_id, release_state FROM posts WHERE id = $1", created.PostID).Scan(&authorID, &releaseState); err != nil {
		t.Fatal(err)
	}
	if authorID != userID || releaseState != 0 {
		t.Fatalf("author/release=%d/%d want=%d/0", authorID, releaseState, userID)
	}
	var sourceCount, jobCount int
	var storageKey string
	if err := store.pool.QueryRow(ctx, "SELECT count(*), min(storage_key) FROM media_sources WHERE post_id = $1", created.PostID).Scan(&sourceCount, &storageKey); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM media_jobs WHERE post_id = $1 AND id = $2", created.PostID, created.JobID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != 1 || jobCount != 1 {
		t.Fatalf("source/job counts=%d/%d", sourceCount, jobCount)
	}
	if _, err := os.Stat(filepath.Join(mediaRoot, filepath.FromSlash(storageKey))); err != nil {
		t.Fatalf("staged source %q: %v", storageKey, err)
	}

	feedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(feedRes, httptest.NewRequest(http.MethodGet, "http://ginbar.test/api/v2/feed", nil))
	var feedPage feed.Page
	if feedRes.Code != http.StatusOK || json.Unmarshal(feedRes.Body.Bytes(), &feedPage) != nil || len(feedPage.Posts) != 0 {
		t.Fatalf("feed status=%d page=%#v body=%s", feedRes.Code, feedPage, feedRes.Body.String())
	}

	searchRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(searchRes, httptest.NewRequest(http.MethodGet, "http://ginbar.test/api/v2/feed?q=score:%3E%3D0", nil))
	var searchPage feed.Page
	if searchRes.Code != http.StatusOK || json.Unmarshal(searchRes.Body.Bytes(), &searchPage) != nil || len(searchPage.Posts) != 0 {
		t.Fatalf("search status=%d page=%#v body=%s", searchRes.Code, searchPage, searchRes.Body.String())
	}

	profileRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(profileRes, httptest.NewRequest(http.MethodGet, "http://ginbar.test/api/v2/users/"+strconv.FormatInt(userID, 10), nil))
	var profilePage profile.Page
	if profileRes.Code != http.StatusOK || json.Unmarshal(profileRes.Body.Bytes(), &profilePage) != nil || len(profilePage.Posts) != 0 {
		t.Fatalf("profile status=%d page=%#v body=%s", profileRes.Code, profilePage, profileRes.Body.String())
	}

	var unrelatedUsername string
	if err := store.pool.QueryRow(ctx, "SELECT username FROM users WHERE id = $1", unrelatedUserID).Scan(&unrelatedUsername); err != nil {
		t.Fatal(err)
	}
	if unrelatedUsername != "unrelated-user" {
		t.Fatalf("unrelated username=%q", unrelatedUsername)
	}
}
