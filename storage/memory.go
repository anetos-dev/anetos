// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"iter"
	"slices"
	"strings"
	"sync"

	"anetos.dev/anetos"
)

// MemoryBackend keeps files in memory: for tests and development. Files
// are lost when the process stops. Make one with [NewMemoryBackend]: its
// zero value isn't usable.
type MemoryBackend struct {
	mu    sync.RWMutex
	files map[string]memFile
}

type memFile struct {
	data []byte
	info FileInfo
}

// NewMemoryBackend returns an empty memory backend.
func NewMemoryBackend() *MemoryBackend { return &MemoryBackend{files: map[string]memFile{}} }

// Put implements [Backend].
func (m *MemoryBackend) Put(ctx context.Context, path string, r io.Reader, opts PutOptions) error {
	data, err := io.ReadAll(ctxReader{ctx, r})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	f := memFile{data: data, info: FileInfo{Path: path, Size: int64(len(data)), ModTime: anetos.Now(ctx).UTC(),
		ContentType: opts.ContentType, ETag: `"` + hex.EncodeToString(sum[:16]) + `"`}}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = f
	return nil
}

// Open implements [Backend].
func (m *MemoryBackend) Open(ctx context.Context, path string) (File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	f, ok := m.files[path]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return &memReader{Reader: bytes.NewReader(f.data), info: f.info}, nil
}

type memReader struct {
	*bytes.Reader
	info FileInfo
}

func (r *memReader) Close() error   { return nil }
func (r *memReader) Info() FileInfo { return r.info }

// Stat implements [Backend].
func (m *MemoryBackend) Stat(ctx context.Context, path string) (FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return FileInfo{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.files[path]
	if !ok {
		return FileInfo{}, ErrNotFound
	}
	return f.info, nil
}

// Delete implements [Backend].
func (m *MemoryBackend) Delete(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, path)
	return nil
}

// List implements [Backend].
func (m *MemoryBackend) List(ctx context.Context, prefix string) iter.Seq2[FileInfo, error] {
	return func(yield func(FileInfo, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(FileInfo{}, err)
			return
		}
		m.mu.RLock()
		var infos []FileInfo
		for p, f := range m.files {
			if strings.HasPrefix(p, prefix) {
				infos = append(infos, f.info)
			}
		}
		m.mu.RUnlock()
		slices.SortFunc(infos, func(a, b FileInfo) int { return strings.Compare(a.Path, b.Path) })
		for _, info := range infos {
			if !yield(info, nil) {
				return
			}
		}
	}
}

// Copy implements [Backend].
func (m *MemoryBackend) Copy(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[src]
	if !ok {
		return ErrNotFound
	}
	f.info.Path = dst
	f.info.ModTime = anetos.Now(ctx).UTC()
	m.files[dst] = f // the data is never changed in place
	return nil
}

// ctxReader stops reading when ctx ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
