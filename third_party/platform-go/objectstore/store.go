// Package objectstore provides binary object storage behind a small interface,
// with an S3-compatible backend and a local-disk fallback.
//
// It exists because three services needed the same thing and two had already
// copied it: contract-management wrote the SigV4 presigner, DMS copied it, and
// procurement and finance were about to. The presigner lives in
// platform-go/objstore; this package is the storage abstraction over it.
//
// A nil Store means storage is unconfigured. Callers should treat that as
// "uploads unavailable" rather than crashing, and say so loudly in production -
// a configuration that appears to work while silently discarding files is worse
// than one that fails outright.
package objectstore

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alvor-technologies/iag-platform-go/objstore"
)

// Store persists and retrieves opaque objects keyed by a caller-supplied key.
type Store interface {
	// Put streams r to the object identified by key and returns bytes written.
	// contentType is advisory; richer backends persist it.
	Put(key, contentType string, r io.Reader) (int64, error)
	// Open returns a reader for the object; the caller must Close it. A missing
	// object returns an error satisfying errors.Is(err, fs.ErrNotExist).
	Open(key string) (io.ReadCloser, error)
	// Delete removes the object. A missing object is not an error.
	Delete(key string) error
}

// presignExpiry bounds how long a signed URL is valid. These URLs are used
// immediately by the calling process and never handed to a browser, so the
// window is short: long enough for a slow upload, short enough that one captured
// from a log is useless by the time anyone reads it.
const presignExpiry = 15 * time.Minute

// ---------- S3 ----------

// S3Store implements Store against an S3-compatible bucket (AWS S3, Cloudflare
// R2, MinIO). It signs a URL and performs the request itself rather than pulling
// in an AWS SDK - SigV4 is already implemented in objstore with the standard
// library, and a large dependency for three verbs is a poor trade.
type S3Store struct {
	pre    *objstore.Presigner
	client *http.Client
}

// NewS3Store returns a Store backed by the bucket, or nil if unconfigured.
// A nil return is the signal to fall back to disk.
func NewS3Store(endpoint, region, bucket, accessKey, secretKey string, useSSL bool) *S3Store {
	pre := objstore.New(endpoint, region, bucket, accessKey, secretKey, useSSL)
	if pre == nil {
		return nil
	}
	return &S3Store{
		pre: pre,
		// A timeout is deliberate: without one a hung bucket blocks the calling
		// goroutine indefinitely, which is how a storage outage becomes a
		// service outage.
		client: &http.Client{Timeout: 2 * time.Minute},
	}
}

func (s *S3Store) Put(key, contentType string, r io.Reader) (int64, error) {
	// Establish the length BEFORE wrapping. Go sets Content-Length itself for a
	// *bytes.Reader, *bytes.Buffer or *strings.Reader, but only when that type
	// is handed to it directly - wrapping in a counter hides it, Go falls back
	// to chunked transfer encoding, and an S3-compatible endpoint that requires
	// a length rejects the upload with "411 Length Required".
	size := int64(-1)
	if lr, ok := r.(interface{ Len() int }); ok {
		size = int64(lr.Len())
	}

	// Counted as it streams, so the result is what the bucket received rather
	// than what the caller claimed.
	counter := &countingReader{r: r}
	req, err := http.NewRequest(http.MethodPut, s.pre.PresignPut(key, presignExpiry), counter)
	if err != nil {
		return 0, fmt.Errorf("s3 put %s: %w", key, err)
	}
	if size >= 0 {
		req.ContentLength = size
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("s3 put %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, fmt.Errorf("s3 put %s: unexpected status %s", key, resp.Status)
	}
	return counter.n, nil
}

func (s *S3Store) Open(key string) (io.ReadCloser, error) {
	resp, err := s.client.Get(s.pre.PresignGet(key, presignExpiry))
	if err != nil {
		return nil, fmt.Errorf("s3 open %s: %w", key, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, fmt.Errorf("s3 open %s: %w", key, fs.ErrNotExist)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return nil, fmt.Errorf("s3 open %s: unexpected status %s", key, resp.Status)
	}
	return resp.Body, nil
}

func (s *S3Store) Delete(key string) error {
	req, err := http.NewRequest(http.MethodDelete, s.pre.PresignDelete(key, presignExpiry), nil)
	if err != nil {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("s3 delete %s: %w", key, err)
	}
	defer resp.Body.Close()
	// Most implementations return 204 whether or not the key existed; 404 is
	// tolerated for the ones that do not.
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("s3 delete %s: unexpected status %s", key, resp.Status)
	}
	return nil
}

// PresignGet and PresignPut expose browser-facing URLs. A handler can hand these
// to a client instead of proxying bytes through the service, which keeps large
// files off the request path entirely.
func (s *S3Store) PresignGet(key string, expiry time.Duration) string {
	return s.pre.PresignGet(key, expiry)
}

func (s *S3Store) PresignPut(key string, expiry time.Duration) string {
	return s.pre.PresignPut(key, expiry)
}

// ---------- disk ----------

// DiskStore keeps objects under a base directory. Keys may contain forward
// slashes to create sub-directories; path traversal is rejected.
//
// On a container platform without a mounted volume this is EPHEMERAL: objects
// survive until the next redeploy. It is a development convenience, not a
// production backend.
type DiskStore struct{ base string }

func NewDiskStore(dir string) (*DiskStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("objectstore: empty directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("objectstore: create %s: %w", dir, err)
	}
	return &DiskStore{base: dir}, nil
}

func (d *DiskStore) resolve(key string) (string, error) {
	clean := filepath.Clean("/" + filepath.FromSlash(key))
	full := filepath.Join(d.base, clean)
	absBase, err := filepath.Abs(d.base)
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absFull, absBase) {
		return "", fmt.Errorf("objectstore: key escapes base directory")
	}
	return absFull, nil
}

func (d *DiskStore) Put(key, _ string, r io.Reader) (int64, error) {
	full, err := d.resolve(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(full)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}

func (d *DiskStore) Open(key string) (io.ReadCloser, error) {
	full, err := d.resolve(key)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

func (d *DiskStore) Delete(key string) error {
	full, err := d.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
