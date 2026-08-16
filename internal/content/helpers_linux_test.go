//go:build linux

package content

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// openTestCAS opens a cover cache in a directory the test owns. It is
// named for the store this package used to have, because the tests that
// call it are the ones that survived it.
func openTestCAS(t *testing.T) *Cache {
	t.Helper()
	// t.TempDir() is 0700 but sits under a 0755 parent; the cache checks
	// its own directory, so a nested one is what the test needs.
	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	c, err := OpenCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
