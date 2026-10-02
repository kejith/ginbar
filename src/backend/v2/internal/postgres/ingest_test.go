package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kejith/ginbar/backend/v2/internal/ingest"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/schema"
)

func TestCreateIngestionAtomicallyCreatesUnreleasedPostSourceAndJob(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()

	ctx := context.Background()
	var userID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ('ingest-user') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("source"))
	created, err := store.CreateIngestion(ctx, ingest.CreateRequest{
		AuthorUserID: userID,
		Filter:       model.FilterNSFP,
		Source: ingest.SourceRecord{
			Type:         ingest.SourceUpload,
			StorageKey:   "sources/aa/0123456789abcdef0123456789abcdef",
			OriginalName: "image.png",
			DeclaredMIME: "image/png",
			ByteSize:     6,
			SHA256:       hash,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.PostID <= 0 || created.JobID <= 0 {
		t.Fatalf("created = %#v", created)
	}

	var authorID int64
	var filter int16
	var releaseState int16
	if err := store.pool.QueryRow(ctx,
		"SELECT author_user_id, content_filter, release_state FROM posts WHERE id = $1",
		created.PostID,
	).Scan(&authorID, &filter, &releaseState); err != nil {
		t.Fatal(err)
	}
	if authorID != userID || filter != int16(model.FilterNSFP) || releaseState != 0 {
		t.Fatalf("post author/filter/release = %d/%d/%d", authorID, filter, releaseState)
	}

	var sourceType int16
	var sourceURL *string
	var originalName *string
	var declaredMIME string
	var byteSize int64
	var gotHash []byte
	if err := store.pool.QueryRow(ctx, `
		SELECT source_type, source_url, original_name, declared_mime_type, byte_size, sha256
		FROM media_sources
		WHERE post_id = $1
	`, created.PostID).Scan(&sourceType, &sourceURL, &originalName, &declaredMIME, &byteSize, &gotHash); err != nil {
		t.Fatal(err)
	}
	if sourceType != int16(ingest.SourceUpload) || sourceURL != nil || originalName == nil || *originalName != "image.png" || declaredMIME != "image/png" || byteSize != 6 || string(gotHash) != string(hash[:]) {
		t.Fatalf("source = type:%d url:%v name:%v mime:%q bytes:%d hash:%x", sourceType, sourceURL, originalName, declaredMIME, byteSize, gotHash)
	}

	var jobPostID int64
	var kind int16
	var state int16
	var attempts int32
	if err := store.pool.QueryRow(ctx,
		"SELECT post_id, kind, state, attempts FROM media_jobs WHERE id = $1",
		created.JobID,
	).Scan(&jobPostID, &kind, &state, &attempts); err != nil {
		t.Fatal(err)
	}
	if jobPostID != created.PostID || kind != mediaJobKindInitialProcess || state != 0 || attempts != 0 {
		t.Fatalf("job post/kind/state/attempts = %d/%d/%d/%d", jobPostID, kind, state, attempts)
	}

	var releasedFeedCount int
	if err := store.pool.QueryRow(ctx,
		"SELECT count(*) FROM posts WHERE id = $1 AND release_state = 1 AND deleted_at IS NULL",
		created.PostID,
	).Scan(&releasedFeedCount); err != nil {
		t.Fatal(err)
	}
	if releasedFeedCount != 0 {
		t.Fatalf("processing post visible as released: count=%d", releasedFeedCount)
	}
}

func TestCreateIngestionLaterConstraintFailureRollsBackPostSourceAndJob(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()

	ctx := context.Background()
	var userID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ('rollback-user') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("source"))
	_, err := store.CreateIngestion(ctx, ingest.CreateRequest{
		AuthorUserID: userID,
		Filter:       model.FilterSFW,
		Source: ingest.SourceRecord{
			Type:       ingest.SourceType(99),
			StorageKey: "sources/aa/ffffffffffffffffffffffffffffffff",
			ByteSize:   6,
			SHA256:     hash,
		},
	})
	if err == nil {
		t.Fatal("ingestion with invalid source type unexpectedly succeeded")
	}

	for _, table := range []string{"posts", "media_sources", "media_jobs"} {
		var count int
		if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("table %s retained %d rows after later transaction failure", table, count)
		}
	}
}

func testIngestionStore(t *testing.T) (*Store, func()) {
	t.Helper()
	url := os.Getenv("GINBAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("GINBAR_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	schemaName := fmt.Sprintf("ginbar_ingest_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		admin.Close(context.Background())
		t.Fatalf("create schema: %v", err)
	}
	if _, err := admin.Exec(ctx, "SET search_path TO "+schemaName); err != nil {
		admin.Close(context.Background())
		t.Fatalf("set migration search path: %v", err)
	}
	names, err := schema.MigrationNames()
	if err != nil {
		admin.Close(context.Background())
		t.Fatal(err)
	}
	for _, name := range names {
		migration, err := schema.Migration(name)
		if err != nil {
			admin.Close(context.Background())
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, string(migration), pgx.QueryExecModeSimpleProtocol); err != nil {
			admin.Close(context.Background())
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	poolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		admin.Close(context.Background())
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schemaName
	poolConfig.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		admin.Close(context.Background())
		t.Fatal(err)
	}
	store := &Store{pool: pool}
	cleanup := func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "SET search_path TO public")
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+schemaName+" CASCADE")
		_ = admin.Close(cleanupCtx)
	}
	return store, cleanup
}
