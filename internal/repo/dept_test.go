package repo

import "testing"

func TestCanonicalDeptCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"fleet", "MAINT"},
		{"Production", "PROD"},
		{"PROD", "PROD"},
		{"quality control", "QC"},
	}
	for _, tc := range cases {
		if got := CanonicalDeptCode(tc.in); got != tc.want {
			t.Errorf("CanonicalDeptCode(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestDeptMatchCandidatesIncludesLegacy(t *testing.T) {
	got := deptMatchCandidates("production")
	found := false
	for _, c := range got {
		if c == "Production" || c == "PROD" {
			found = true
		}
	}
	if !found {
		t.Fatalf("candidates %v missing Production/PROD", got)
	}
}
