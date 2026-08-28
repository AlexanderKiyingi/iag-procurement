package objstore

import (
	"strings"
	"testing"
	"time"
)

// TestSigV4_AWSS3Example checks the full presign signature against AWS's
// published S3 "GET object" presigned-URL example (Signature Version 4) — an
// authoritative end-to-end vector for the canonical request + signing chain.
func TestSigV4_AWSS3Example(t *testing.T) {
	_, sig := sigV4PresignQuery(
		"GET", "examplebucket.s3.amazonaws.com", "/test.txt",
		"AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"us-east-1", "s3", "20130524T000000Z", "20130524", 86400,
	)
	const want = "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if sig != want {
		t.Fatalf("signature mismatch:\n got %s\nwant %s", sig, want)
	}
}

func TestAWSURIEncode(t *testing.T) {
	cases := []struct {
		in    string
		slash bool
		want  string
	}{
		{"abc", true, "abc"},
		{"a b", true, "a%20b"},
		{"a/b", false, "a/b"},
		{"a/b", true, "a%2Fb"},
		{"a+b=c", true, "a%2Bb%3Dc"},
		{"~-_.", true, "~-_."},
	}
	for _, c := range cases {
		if got := awsURIEncode(c.in, c.slash); got != c.want {
			t.Errorf("awsURIEncode(%q, %v) = %q, want %q", c.in, c.slash, got, c.want)
		}
	}
}

func TestPresignStructure(t *testing.T) {
	p := New("s3.example.com", "us-east-1", "bucket", "AKIA", "secret", true)
	if p == nil {
		t.Fatal("expected a presigner")
	}
	u := p.PresignGet("governance/contracts/c1/file name.pdf", 15*time.Minute)
	for _, want := range []string{
		"https://s3.example.com/bucket/governance/contracts/c1/file%20name.pdf?",
		"X-Amz-Algorithm=AWS4-HMAC-SHA256",
		"X-Amz-Credential=AKIA%2F",
		"X-Amz-SignedHeaders=host",
		"&X-Amz-Signature=",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("presigned URL missing %q:\n%s", want, u)
		}
	}
}

func TestNew_DisabledWhenUnconfigured(t *testing.T) {
	if New("", "r", "b", "k", "s", true) != nil {
		t.Error("expected nil presigner when endpoint is empty")
	}
	if New("h", "r", "", "k", "s", true) != nil {
		t.Error("expected nil presigner when bucket is empty")
	}
	if p := New("h", "r", "b", "k", "s", true); !p.IsEnabled() {
		t.Error("expected IsEnabled() true when fully configured")
	}
	var nilP *Presigner
	if nilP.IsEnabled() {
		t.Error("expected nil-safe IsEnabled() == false")
	}
}
