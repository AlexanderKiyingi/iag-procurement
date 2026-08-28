package objectstore

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The interface exists so a service can swap backends without touching call
// sites. That is only true if the backends agree on behaviour, so the contract
// is asserted here rather than assumed.

func TestDiskStoreRoundTrip(t *testing.T) {
	s, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	n, err := s.Put("a/b/file.txt", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if n != 5 {
		t.Fatalf("put returned %d bytes, want 5", n)
	}
	rc, err := s.Open("a/b/file.txt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello" {
		t.Fatalf("read %q, want %q", got, "hello")
	}
}

// A missing object must be distinguishable from a broken one, identically
// across backends, or callers cannot handle "not found" without knowing which
// backend they have.
func TestDiskStoreMissingObjectIsErrNotExist(t *testing.T) {
	s, _ := NewDiskStore(t.TempDir())
	_, err := s.Open("nope.txt")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("open missing: got %v, want an error satisfying fs.ErrNotExist", err)
	}
}

// Deleting something that was never there is success, not an error - callers
// should not have to check first.
func TestDiskStoreDeleteMissingIsNotAnError(t *testing.T) {
	s, _ := NewDiskStore(t.TempDir())
	if err := s.Delete("never-existed.txt"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

// Keys arrive from callers and may be attacker-influenced. Traversal is
// NEUTRALISED rather than rejected: the key is cleaned against a leading "/", so
// "../escaped.txt" becomes "/escaped.txt" and lands inside the base directory.
// The property that matters is containment, so that is what is asserted - an
// earlier version of this test expected an error and was wrong about the
// contract.
func TestDiskStoreContainsPathTraversal(t *testing.T) {
	base := t.TempDir()
	s, _ := NewDiskStore(base)
	if _, err := s.Put("../escaped.txt", "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Nothing may appear outside the base directory.
	if _, err := os.Stat(filepath.Join(filepath.Dir(base), "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("traversal escaped the base directory: %v", err)
	}
	// And the object is readable back under its cleaned key.
	rc, err := s.Open("escaped.txt")
	if err != nil {
		t.Fatalf("open cleaned key: %v", err)
	}
	rc.Close()
}

// An unconfigured bucket must yield nil rather than a Store that fails on every
// call, so the caller can fall back to disk with a single nil check.
func TestNewS3StoreNilWhenUnconfigured(t *testing.T) {
	for _, tc := range []struct {
		name                                           string
		endpoint, region, bucket, accessKey, secretKey string
	}{
		{"all empty", "", "", "", "", ""},
		{"no bucket", "s3.example.com", "auto", "", "ak", "sk"},
		{"no credentials", "s3.example.com", "auto", "b", "", ""},
		{"no endpoint", "", "auto", "b", "ak", "sk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewS3Store(tc.endpoint, tc.region, tc.bucket, tc.accessKey, tc.secretKey, true); got != nil {
				t.Fatalf("expected nil for %s, got %#v", tc.name, got)
			}
		})
	}
}

func TestNewS3StoreConfigured(t *testing.T) {
	s := NewS3Store("s3.example.com", "auto", "bucket", "ak", "sk", true)
	if s == nil {
		t.Fatal("expected a store when fully configured")
	}
	if url := s.PresignGet("some/key.pdf", presignExpiry); !strings.Contains(url, "bucket") {
		t.Fatalf("presigned URL does not address the bucket: %s", url)
	}
}

// Both backends must satisfy Store; this fails at compile time if either drifts.
var (
	_ Store = (*DiskStore)(nil)
	_ Store = (*S3Store)(nil)
)
