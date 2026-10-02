package ingest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const copyBufferSize = 64 << 10

type LocalStore struct {
	root     string
	staging  string
	sources  string
	maxBytes int64
}

func NewLocalStore(root string, maxBytes int64) (*LocalStore, error) {
	if root == "" {
		return nil, errors.New("media source root is required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("media source max bytes must be positive")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve media source root: %w", err)
	}
	store := &LocalStore{
		root:     absolute,
		staging:  filepath.Join(absolute, ".staging"),
		sources:  filepath.Join(absolute, "sources"),
		maxBytes: maxBytes,
	}
	if err := os.MkdirAll(store.staging, 0o750); err != nil {
		return nil, fmt.Errorf("create media staging directory: %w", err)
	}
	if err := os.MkdirAll(store.sources, 0o750); err != nil {
		return nil, fmt.Errorf("create media source directory: %w", err)
	}
	return store, nil
}

func (s *LocalStore) Stage(ctx context.Context, source io.Reader) (StagedSource, error) {
	if source == nil {
		return StagedSource{}, errors.New("source reader is required")
	}
	temp, err := os.CreateTemp(s.staging, "source-*")
	if err != nil {
		return StagedSource{}, fmt.Errorf("create staged source: %w", err)
	}
	tempName := temp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = temp.Close()
			_ = os.Remove(tempName)
		}
	}()

	hash := sha256.New()
	limited := io.LimitReader(&contextReader{ctx: ctx, reader: source}, s.maxBytes+1)
	written, copyErr := io.CopyBuffer(io.MultiWriter(temp, hash), limited, make([]byte, copyBufferSize))
	if copyErr != nil {
		return StagedSource{}, fmt.Errorf("stage media source: %w", copyErr)
	}
	if written > s.maxBytes {
		return StagedSource{}, ErrSourceTooLarge
	}
	if written == 0 {
		return StagedSource{}, ErrEmptySource
	}
	if err := ctx.Err(); err != nil {
		return StagedSource{}, err
	}
	if err := temp.Sync(); err != nil {
		return StagedSource{}, fmt.Errorf("sync staged source: %w", err)
	}
	if err := temp.Chmod(0o640); err != nil {
		return StagedSource{}, fmt.Errorf("set staged source permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		return StagedSource{}, fmt.Errorf("close staged source: %w", err)
	}

	key, err := s.publish(tempName)
	if err != nil {
		return StagedSource{}, err
	}
	keepTemp = false

	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return StagedSource{StorageKey: key, ByteSize: written, SHA256: digest}, nil
}

func (s *LocalStore) publish(tempName string) (string, error) {
	for range 4 {
		key, finalPath, err := s.newKey()
		if err != nil {
			return "", err
		}
		finalDir := filepath.Dir(finalPath)
		if err := os.MkdirAll(finalDir, 0o750); err != nil {
			return "", fmt.Errorf("create source shard directory: %w", err)
		}
		if err := os.Link(tempName, finalPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", fmt.Errorf("publish staged source: %w", err)
		}
		if err := os.Remove(tempName); err != nil {
			_ = os.Remove(finalPath)
			return "", fmt.Errorf("remove staged source link: %w", err)
		}
		if err := syncDirectory(finalDir); err != nil {
			_ = os.Remove(finalPath)
			return "", fmt.Errorf("sync source directory: %w", err)
		}
		return key, nil
	}
	return "", errors.New("could not allocate unique media source key")
}

func (s *LocalStore) Remove(ctx context.Context, storageKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.pathForKey(storageKey)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove media source: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync source deletion: %w", err)
	}
	return nil
}

func (s *LocalStore) newKey() (string, string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", "", fmt.Errorf("generate media source key: %w", err)
	}
	encoded := hex.EncodeToString(id[:])
	key := filepath.ToSlash(filepath.Join("sources", encoded[:2], encoded))
	path, err := s.pathForKey(key)
	if err != nil {
		return "", "", err
	}
	return key, path, nil
}

func (s *LocalStore) pathForKey(storageKey string) (string, error) {
	if storageKey == "" || strings.ContainsRune(storageKey, '\x00') {
		return "", errors.New("invalid media source storage key")
	}
	cleaned := filepath.Clean(filepath.FromSlash(storageKey))
	if filepath.ToSlash(cleaned) != storageKey {
		return "", errors.New("invalid media source storage key")
	}
	sourcePrefix := "sources" + string(os.PathSeparator)
	if cleaned == "sources" || !strings.HasPrefix(cleaned, sourcePrefix) || filepath.IsAbs(cleaned) {
		return "", errors.New("invalid media source storage key")
	}
	path := filepath.Join(s.root, cleaned)
	relative, err := filepath.Rel(s.sources, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", errors.New("invalid media source storage key")
	}
	return path, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
