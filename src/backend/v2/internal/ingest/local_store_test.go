package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalStoreStagesAndRemovesSource(t *testing.T) {
	store, err := NewLocalStore(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("ginbar-media-source")
	staged, err := store.Stage(context.Background(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if staged.ByteSize != int64(len(body)) {
		t.Fatalf("byte size = %d want %d", staged.ByteSize, len(body))
	}
	wantHash := sha256.Sum256(body)
	if staged.SHA256 != wantHash {
		t.Fatalf("sha256 = %x want %x", staged.SHA256, wantHash)
	}
	path, err := store.pathForKey(staged.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("stored body = %q want %q", got, body)
	}
	if err := store.Remove(context.Background(), staged.StorageKey); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists after remove: %v", err)
	}
}

func TestLocalStoreRejectsOversizeAndEmptyWithoutFiles(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stage(context.Background(), bytes.NewReader([]byte("12345"))); !errors.Is(err, ErrSourceTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	if _, err := store.Stage(context.Background(), bytes.NewReader(nil)); !errors.Is(err, ErrEmptySource) {
		t.Fatalf("empty error = %v", err)
	}
	assertNoSourceFiles(t, root)
}

func TestLocalStoreCanceledStageCleansTemporaryFile(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Stage(ctx, bytes.NewReader([]byte("content"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	assertNoSourceFiles(t, root)
}

func TestLocalStoreDuplicateBytesUseDistinctKeys(t *testing.T) {
	store, err := NewLocalStore(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("same bytes")
	first, err := store.Stage(context.Background(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Stage(context.Background(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if first.StorageKey == second.StorageKey {
		t.Fatalf("duplicate sources reused storage key %q", first.StorageKey)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatal("duplicate bytes produced different hashes")
	}
}

func TestLocalStoreRejectsNonCanonicalStorageKey(t *testing.T) {
	store, err := NewLocalStore(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"",
		"../sources/a",
		"sources/../sources/a",
		"/sources/a",
		"other/a",
		"sources/a/not-hex",
		"sources/aa/not-32-hex",
		"sources/ab/0123456789abcdef0123456789abcdef",
		"sources/01/0123456789abcdef0123456789abcdeg",
		"sources/01/0123456789ABCDEF0123456789ABCDEF",
		"sources/01/0123456789abcdef0123456789abcdef/extra",
	} {
		if err := store.Remove(context.Background(), key); err == nil {
			t.Fatalf("Remove(%q) unexpectedly succeeded", key)
		}
	}
}

func TestValidSourceStorageKeyRequiresGeneratedShape(t *testing.T) {
	valid := "sources/01/0123456789abcdef0123456789abcdef"
	if !validSourceStorageKey(valid) {
		t.Fatalf("validSourceStorageKey(%q) = false", valid)
	}
	for _, key := range []string{
		"sources/0/0123456789abcdef0123456789abcdef",
		"sources/01/0123456789abcdef0123456789abcde",
		"sources/02/0123456789abcdef0123456789abcdef",
		"sources/01/0123456789abcdef0123456789abcdeg",
		"sources/01/0123456789ABCDEF0123456789ABCDEF",
	} {
		if validSourceStorageKey(key) {
			t.Fatalf("validSourceStorageKey(%q) = true", key)
		}
	}
}

func assertNoSourceFiles(t *testing.T, root string) {
	t.Helper()
	for _, directory := range []string{filepath.Join(root, ".staging"), filepath.Join(root, "sources")} {
		err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				t.Fatalf("unexpected staged file %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkLocalStoreStage8MiB(b *testing.B) {
	payload := bytes.Repeat([]byte{0x5a}, 8<<20)
	store, err := NewLocalStore(b.TempDir(), int64(len(payload)))
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for range b.N {
		staged, err := store.Stage(context.Background(), bytes.NewReader(payload))
		if err != nil {
			b.Fatal(err)
		}
		if err := store.Remove(context.Background(), staged.StorageKey); err != nil {
			b.Fatal(err)
		}
	}
}
