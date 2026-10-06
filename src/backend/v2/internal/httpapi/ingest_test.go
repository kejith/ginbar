package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/ingest"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

type ingestAPIRepository struct {
	calls   atomic.Int32
	create  func(context.Context, ingest.CreateRequest) (ingest.Created, error)
	request ingest.CreateRequest
}

func (r *ingestAPIRepository) CreateIngestion(ctx context.Context, request ingest.CreateRequest) (ingest.Created, error) {
	r.calls.Add(1)
	r.request = request
	if r.create != nil {
		return r.create(ctx, request)
	}
	return ingest.Created{PostID: 101, JobID: 202}, nil
}

type ingestAPIStager struct {
	calls       atomic.Int32
	removeCalls atomic.Int32
	stage       func(context.Context, io.Reader) (ingest.StagedSource, error)
}

func (s *ingestAPIStager) Stage(ctx context.Context, reader io.Reader) (ingest.StagedSource, error) {
	s.calls.Add(1)
	if s.stage != nil {
		return s.stage(ctx, reader)
	}
	return ingestAPIStaged(), nil
}

func (s *ingestAPIStager) Remove(context.Context, string) error {
	s.removeCalls.Add(1)
	return nil
}

type ingestAPIFetcher struct {
	calls atomic.Int32
	fetch func(context.Context, string) (ingest.FetchedSource, error)
}

func (f *ingestAPIFetcher) Fetch(ctx context.Context, rawURL string) (ingest.FetchedSource, error) {
	f.calls.Add(1)
	if f.fetch != nil {
		return f.fetch(ctx, rawURL)
	}
	return ingest.FetchedSource{
		Body:         io.NopCloser(strings.NewReader("remote")),
		EffectiveURL: "https://cdn.example.test/media.jpg",
		DeclaredMIME: "image/jpeg",
	}, nil
}

func TestIngestionRequiresAuthentication(t *testing.T) {
	base := &apiStore{}
	repo := &ingestAPIRepository{}
	stager := &ingestAPIStager{}
	fetcher := &ingestAPIFetcher{}
	server := newIngestAPIServer(t, base, repo, stager, fetcher, nil)

	upload := multipartRequest(t, base, "/api/v2/posts/upload?filter=sfw", []byte("image"), false)
	upload.Header.Set("Origin", "http://ginbar.test")
	uploadRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(uploadRes, upload)
	if uploadRes.Code != http.StatusUnauthorized {
		t.Fatalf("upload status=%d body=%s", uploadRes.Code, uploadRes.Body.String())
	}

	urlReq := sameOriginRequest(http.MethodPost, "/api/v2/posts/import-url", `{"url":"https://example.test/image.jpg","filter":"sfw"}`)
	urlRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(urlRes, urlReq)
	if urlRes.Code != http.StatusUnauthorized {
		t.Fatalf("url status=%d body=%s", urlRes.Code, urlRes.Body.String())
	}
	if repo.calls.Load() != 0 || stager.calls.Load() != 0 || fetcher.calls.Load() != 0 {
		t.Fatalf("signed-out request reached ingestion repo=%d stage=%d fetch=%d", repo.calls.Load(), stager.calls.Load(), fetcher.calls.Load())
	}
}

func TestUploadUsesAuthenticatedNumericIdentityAndStreamsMultipart(t *testing.T) {
	base := &apiStore{}
	repo := &ingestAPIRepository{}
	stager := &ingestAPIStager{stage: func(_ context.Context, reader io.Reader) (ingest.StagedSource, error) {
		body, err := io.ReadAll(reader)
		if err != nil {
			return ingest.StagedSource{}, err
		}
		if string(body) != "image-data" {
			t.Fatalf("body=%q", body)
		}
		return ingestAPIStaged(), nil
	}}
	server := newIngestAPIServer(t, base, repo, stager, nil, nil)

	req := multipartRequest(t, base, "/api/v2/posts/upload?filter=nsfw", []byte("image-data"), true)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	assertIngestionResponse(t, res, 101, 202)
	if repo.request.AuthorUserID != 42 || repo.request.Filter != model.FilterNSFW {
		t.Fatalf("request=%#v", repo.request)
	}
	if repo.request.Source.Type != ingest.SourceUpload || repo.request.Source.OriginalName != "image.jpg" || repo.request.Source.DeclaredMIME != "image/jpeg" {
		t.Fatalf("source=%#v", repo.request.Source)
	}
}

func TestURLImportUsesControlledFetcherAndReturnsAuthoritativeIDs(t *testing.T) {
	base := &apiStore{}
	repo := &ingestAPIRepository{create: func(_ context.Context, request ingest.CreateRequest) (ingest.Created, error) {
		return ingest.Created{PostID: 303, JobID: 404}, nil
	}}
	stager := &ingestAPIStager{stage: func(_ context.Context, reader io.Reader) (ingest.StagedSource, error) {
		body, err := io.ReadAll(reader)
		if err != nil {
			return ingest.StagedSource{}, err
		}
		if string(body) != "remote" {
			t.Fatalf("body=%q", body)
		}
		return ingestAPIStaged(), nil
	}}
	fetcher := &ingestAPIFetcher{fetch: func(_ context.Context, rawURL string) (ingest.FetchedSource, error) {
		if rawURL != "https://example.test/image.jpg" {
			t.Fatalf("raw URL=%q", rawURL)
		}
		return ingest.FetchedSource{
			Body:         io.NopCloser(strings.NewReader("remote")),
			EffectiveURL: "https://cdn.example.test/final.jpg",
			DeclaredMIME: "image/jpeg",
		}, nil
	}}
	server := newIngestAPIServer(t, base, repo, stager, fetcher, nil)

	req := authenticatedRequest(base, http.MethodPost, "/api/v2/posts/import-url", `{"url":"https://example.test/image.jpg","filter":"secret"}`)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	assertIngestionResponse(t, res, 303, 404)
	if repo.request.AuthorUserID != 42 || repo.request.Filter != model.FilterSecret || repo.request.Source.Type != ingest.SourceURL || repo.request.Source.URL != "https://cdn.example.test/final.jpg" {
		t.Fatalf("request=%#v", repo.request)
	}
}

func TestIngestionRejectsCrossOriginBeforeSourceWork(t *testing.T) {
	base := &apiStore{}
	repo := &ingestAPIRepository{}
	stager := &ingestAPIStager{}
	fetcher := &ingestAPIFetcher{}
	server := newIngestAPIServer(t, base, repo, stager, fetcher, nil)

	upload := multipartRequest(t, base, "/api/v2/posts/upload?filter=sfw", []byte("x"), true)
	upload.Header.Set("Origin", "https://evil.test")
	uploadRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(uploadRes, upload)
	if uploadRes.Code != http.StatusForbidden {
		t.Fatalf("upload status=%d body=%s", uploadRes.Code, uploadRes.Body.String())
	}

	urlReq := authenticatedRequest(base, http.MethodPost, "/api/v2/posts/import-url", `{"url":"https://example.test/image.jpg","filter":"sfw"}`)
	urlReq.Header.Set("Origin", "https://evil.test")
	urlRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(urlRes, urlReq)
	if urlRes.Code != http.StatusForbidden {
		t.Fatalf("url status=%d body=%s", urlRes.Code, urlRes.Body.String())
	}
	if repo.calls.Load() != 0 || stager.calls.Load() != 0 || fetcher.calls.Load() != 0 {
		t.Fatalf("cross-origin request reached ingestion repo=%d stage=%d fetch=%d", repo.calls.Load(), stager.calls.Load(), fetcher.calls.Load())
	}
}

func TestIngestionAcceptsAllContentFiltersAndRejectsInvalidFilter(t *testing.T) {
	for raw, want := range map[string]model.ContentFilter{
		"sfw":    model.FilterSFW,
		"nsfp":   model.FilterNSFP,
		"nsfw":   model.FilterNSFW,
		"secret": model.FilterSecret,
	} {
		t.Run(raw, func(t *testing.T) {
			base := &apiStore{}
			repo := &ingestAPIRepository{}
			server := newIngestAPIServer(t, base, repo, &ingestAPIStager{}, nil, nil)
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, multipartRequest(t, base, "/api/v2/posts/upload?filter="+raw, []byte("x"), true))
			if res.Code != http.StatusCreated || repo.request.Filter != want {
				t.Fatalf("status=%d filter=%d body=%s", res.Code, repo.request.Filter, res.Body.String())
			}
		})
	}

	base := &apiStore{}
	repo := &ingestAPIRepository{}
	stager := &ingestAPIStager{}
	server := newIngestAPIServer(t, base, repo, stager, nil, nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, multipartRequest(t, base, "/api/v2/posts/upload?filter=other", []byte("x"), true))
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"invalid_filter"`) || stager.calls.Load() != 0 {
		t.Fatalf("status=%d stage=%d body=%s", res.Code, stager.calls.Load(), res.Body.String())
	}
}

func TestUploadRejectsMalformedEmptyAndOversizedSources(t *testing.T) {
	base := &apiStore{}
	repo := &ingestAPIRepository{}
	server := newIngestAPIServer(t, base, repo, &ingestAPIStager{}, nil, nil)

	malformed := authenticatedRequest(base, http.MethodPost, "/api/v2/posts/upload?filter=sfw", "not multipart")
	malformed.Header.Set("Content-Type", "text/plain")
	malformedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(malformedRes, malformed)
	if malformedRes.Code != http.StatusBadRequest || !strings.Contains(malformedRes.Body.String(), `"code":"invalid_upload"`) {
		t.Fatalf("malformed status=%d body=%s", malformedRes.Code, malformedRes.Body.String())
	}

	for _, tt := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"empty", ingest.ErrEmptySource, http.StatusBadRequest, "empty_source"},
		{"large", ingest.ErrSourceTooLarge, http.StatusRequestEntityTooLarge, "source_too_large"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stager := &ingestAPIStager{stage: func(context.Context, io.Reader) (ingest.StagedSource, error) { return ingest.StagedSource{}, tt.err }}
			server := newIngestAPIServer(t, base, repo, stager, nil, nil)
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, multipartRequest(t, base, "/api/v2/posts/upload?filter=sfw", []byte("x"), true))
			if res.Code != tt.wantStatus || !strings.Contains(res.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
}

func TestURLImportMapsMissingMalformedAndUnsafeURL(t *testing.T) {
	base := &apiStore{}
	repo := &ingestAPIRepository{}
	fetcher := &ingestAPIFetcher{fetch: func(context.Context, string) (ingest.FetchedSource, error) {
		return ingest.FetchedSource{}, ingest.ErrUnsafeURL
	}}
	server := newIngestAPIServer(t, base, repo, &ingestAPIStager{}, fetcher, nil)

	for _, tt := range []struct {
		body     string
		wantCode string
	}{
		{`{"filter":"sfw"}`, "missing_url"},
		{`{"url":"://bad","filter":"sfw"}`, "invalid_url"},
		{`{"url":"http://127.0.0.1/media","filter":"sfw"}`, "unsafe_url"},
	} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPost, "/api/v2/posts/import-url", tt.body))
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"`+tt.wantCode+`"`) {
			t.Fatalf("body=%s status=%d response=%s", tt.body, res.Code, res.Body.String())
		}
	}
	if fetcher.calls.Load() != 1 {
		t.Fatalf("fetch calls=%d", fetcher.calls.Load())
	}
}

func TestIngestionMapsInternalAndAmbiguousFailuresWithoutRetryOrCleanup(t *testing.T) {
	for _, tt := range []struct {
		name       string
		err        error
		wantCode   string
		wantRemove int32
	}{
		{"internal", errors.New("database unavailable"), "internal", 1},
		{"ambiguous", ingest.ErrCommitOutcomeUnknown, "ingestion_outcome_unknown", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			base := &apiStore{}
			repo := &ingestAPIRepository{create: func(context.Context, ingest.CreateRequest) (ingest.Created, error) { return ingest.Created{}, tt.err }}
			stager := &ingestAPIStager{}
			server := newIngestAPIServer(t, base, repo, stager, nil, nil)
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, multipartRequest(t, base, "/api/v2/posts/upload?filter=sfw", []byte("x"), true))
			if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			if repo.calls.Load() != 1 || stager.removeCalls.Load() != tt.wantRemove {
				t.Fatalf("repo calls=%d remove calls=%d", repo.calls.Load(), stager.removeCalls.Load())
			}
		})
	}
}

func TestUploadUsesIngestionRequestDeadlineInsteadOfShortReadDeadline(t *testing.T) {
	base := &apiStore{}
	repo := &ingestAPIRepository{}
	stager := &ingestAPIStager{stage: func(ctx context.Context, _ io.Reader) (ingest.StagedSource, error) {
		select {
		case <-time.After(10 * time.Millisecond):
			return ingestAPIStaged(), nil
		case <-ctx.Done():
			return ingest.StagedSource{}, ctx.Err()
		}
	}}
	cfg := DefaultConfig()
	cfg.RequestTimeout = time.Millisecond
	cfg.IngestRequestTimeout = 100 * time.Millisecond
	server := newIngestAPIServer(t, base, repo, stager, nil, &cfg)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, multipartRequest(t, base, "/api/v2/posts/upload?filter=sfw", []byte("x"), true))
	if res.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestUploadHandlerDoesNotPrebufferMultipartFile(t *testing.T) {
	base := &apiStore{}
	repo := &ingestAPIRepository{}
	stager := &ingestAPIStager{stage: func(context.Context, io.Reader) (ingest.StagedSource, error) {
		return ingestAPIStaged(), nil
	}}
	server := newIngestAPIServer(t, base, repo, stager, nil, nil)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "large.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), 1<<20)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	counter := &countingReader{reader: bytes.NewReader(body.Bytes())}
	req := authenticatedRequest(base, http.MethodPost, "/api/v2/posts/upload?filter=sfw", "")
	req.Body = io.NopCloser(counter)
	req.ContentLength = int64(body.Len())
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if counter.read >= body.Len()/2 {
		t.Fatalf("handler prebuffered multipart body: read=%d total=%d", counter.read, body.Len())
	}
}

func newIngestAPIServer(
	t *testing.T,
	base *apiStore,
	repo ingest.Repository,
	stager ingest.Stager,
	fetcher ingest.URLFetcher,
	cfgOverride *Config,
) *Server {
	t.Helper()
	service, err := ingest.New(repo, stager, fetcher, ingest.Config{
		StageTimeout:   time.Second,
		DBTimeout:      time.Second,
		CleanupTimeout: time.Second,
		MaxConcurrent:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	if cfgOverride != nil {
		cfg = *cfgOverride
	}
	cfg.Auth.PasswordParams = fastHTTPPasswordParams()
	cfg.Ingest = service
	return NewWithConfig(base, cfg)
}

func multipartRequest(t *testing.T, base *apiStore, path string, content []byte, authenticated bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreatePart(textprotoFileHeader("file", "image.jpg", "image/jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var req *http.Request
	if authenticated {
		req = authenticatedRequest(base, http.MethodPost, path, "")
	} else {
		req = sameOriginRequest(http.MethodPost, path, "")
	}
	req.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
	req.ContentLength = int64(body.Len())
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func textprotoFileHeader(field, filename, contentType string) textproto.MIMEHeader {
	return textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="` + field + `"; filename="` + filename + `"`},
		"Content-Type":        {contentType},
	}
}

func assertIngestionResponse(t *testing.T, res *httptest.ResponseRecorder, postID, jobID int64) {
	t.Helper()
	if res.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body createIngestionResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.PostID != postID || body.JobID != jobID {
		t.Fatalf("body=%#v", body)
	}
}

func ingestAPIStaged() ingest.StagedSource {
	var hash [32]byte
	hash[0] = 1
	return ingest.StagedSource{StorageKey: "sources/aa/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ByteSize: 1, SHA256: hash}
}

type countingReader struct {
	reader io.Reader
	read   int
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.read += n
	return n, err
}
