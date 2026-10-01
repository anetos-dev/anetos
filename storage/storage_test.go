// SPDX-License-Identifier: Apache-2.0

package storage_test

import (
	"testing"

	"anetos.dev/anetos/storage"
	"anetos.dev/anetos/storage/storagetest"
)

func TestMemoryConformance(t *testing.T) {
	storagetest.Run(t, storage.NewMemoryBackend(), storagetest.Features{ContentTypes: true})
}

func TestLocalConformance(t *testing.T) {
	b, err := storage.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	storagetest.Run(t, b, storagetest.Features{})
}
