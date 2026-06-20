package memory_test

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-doc/internal/storage/memory"
	"github.com/Mininglamp-OSS/octo-doc/internal/storage/storagetest"
)

func TestMemoryMetadataContract(t *testing.T) {
	storagetest.RunMetadata(t, memory.New())
}

func TestMemoryBlobContract(t *testing.T) {
	storagetest.RunBlob(t, memory.New())
}
