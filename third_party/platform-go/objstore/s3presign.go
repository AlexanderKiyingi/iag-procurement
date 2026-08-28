// Package objstore issues AWS SigV4 query-string presigned URLs for an
// S3-compatible bucket (AWS S3 / Cloudflare R2 / MinIO) using only the standard
// library. The service never handles file bytes — the browser PUTs/GETs the
// object directly against the returned URL. Path-style addressing
// (https://<endpoint>/<bucket>/<key>) is used for broad compatibility.
package objstore

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Presigner generates presigned URLs. A nil *Presigner means storage is
// unconfigured; callers should check IsEnabled (nil-safe).
type Presigner struct {
	endpoint  string
	region    string
	bucket    string
	accessKey string
	secretKey string
	useSSL    bool
}

// New returns a Presigner, or nil if any required field is empty.
func New(endpoint, region, bucket, accessKey, secretKey string, useSSL bool) *Presigner {
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return nil
	}
	if region == "" {
		region = "auto"
	}
	return &Presigner{
		endpoint:  strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"),
		region:    region,
		bucket:    bucket,
		accessKey: accessKey,
		secretKey: secretKey,
		useSSL:    useSSL,
	}
}

// IsEnabled is nil-safe so handlers can guard with one check.
func (p *Presigner) IsEnabled() bool { return p != nil }

// PresignPut returns a presigned URL the client can PUT the object to.
func (p *Presigner) PresignPut(key string, expiry time.Duration) string {
	return p.presign("PUT", key, expiry)
}

// PresignGet returns a presigned URL for downloading the object.
func (p *Presigner) PresignGet(key string, expiry time.Duration) string {
	return p.presign("GET", key, expiry)
}

// PresignDelete returns a presigned URL for deleting the object.
func (p *Presigner) PresignDelete(key string, expiry time.Duration) string {
	return p.presign("DELETE", key, expiry)
}

func (p *Presigner) scheme() string {
	if p.useSSL {
		return "https"
	}
	return "http"
}

func (p *Presigner) presign(method, key string, expiry time.Duration) string {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	canonicalURI := "/" + p.bucket + "/" + encodePath(key)

	canonicalQuery, signature := sigV4PresignQuery(
		method, p.endpoint, canonicalURI, p.accessKey, p.secretKey,
		p.region, "s3", amzDate, dateStamp, int(expiry.Seconds()),
	)
	return p.scheme() + "://" + p.endpoint + canonicalURI + "?" + canonicalQuery +
		"&X-Amz-Signature=" + signature
}

// sigV4PresignQuery computes the canonical query string and the SigV4 signature
// for a presigned request. Pure (clock supplied via amzDate/dateStamp) so it
// can be checked against AWS's published example vectors.
func sigV4PresignQuery(
	method, host, canonicalURI, accessKey, secretKey, region, service, amzDate, dateStamp string,
	expires int,
) (canonicalQuery, signature string) {
	credentialScope := dateStamp + "/" + region + "/" + service + "/aws4_request"

	q := url.Values{}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", accessKey+"/"+credentialScope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(expires))
	q.Set("X-Amz-SignedHeaders", "host")
	canonicalQuery = canonicalizeQuery(q)

	canonicalHeaders := "host:" + host + "\n"
	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex(canonicalRequest),
	}, "\n")

	signingKey := deriveSigningKey(secretKey, dateStamp, region, service)
	signature = hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	return canonicalQuery, signature
}

// encodePath URI-encodes an object key, preserving "/" between segments.
func encodePath(key string) string { return awsURIEncode(key, false) }

// canonicalizeQuery builds the AWS canonical query string: keys sorted, both
// keys and values RFC3986-encoded (slash encoded).
func canonicalizeQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, awsURIEncode(k, true)+"="+awsURIEncode(q.Get(k), true))
	}
	return strings.Join(parts, "&")
}

// awsURIEncode matches AWS SigV4 percent-encoding (RFC3986, uppercase hex).
func awsURIEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return b.String()
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func deriveSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}
