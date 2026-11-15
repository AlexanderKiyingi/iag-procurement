package repo

import "strings"

// deptAliases map free-text / ERP labels onto canonical finance cost-centre
// codes so ResolveBudgetForDept finds FIN-mirrored envelopes (dept=PROD).
var deptAliases = map[string]string{
	"hr":                "HR",
	"human resources":   "HR",
	"prod":              "PROD",
	"production":        "PROD",
	"maint":             "MAINT",
	"maintenance":       "MAINT",
	"fleet":             "MAINT",
	"fleet-maintenance": "MAINT",
	"fleet_maintenance": "MAINT",
	"engineering":       "MAINT",
	"qc":                "QC",
	"quality":           "QC",
	"quality control":   "QC",
	"log":               "LOG",
	"logistics":         "LOG",
	"operations":        "LOG",
}

// CanonicalDeptCode returns the finance cost-centre code for a dept string,
// or the trimmed uppercased input when no alias matches.
func CanonicalDeptCode(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if code, ok := deptAliases[strings.ToLower(s)]; ok {
		return code
	}
	upper := strings.ToUpper(s)
	switch upper {
	case "HR", "PROD", "MAINT", "QC", "LOG":
		return upper
	}
	return s
}

// deptMatchCandidates are values to try against budgets.dept (ILIKE).
func deptMatchCandidates(raw string) []string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	canon := CanonicalDeptCode(s)
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[strings.ToLower(v)] {
			return
		}
		seen[strings.ToLower(v)] = true
		out = append(out, v)
	}
	add(s)
	add(canon)
	// Legacy seed labels (002_data) still used by older envelopes.
	switch canon {
	case "PROD":
		add("Production")
	case "QC":
		add("Quality")
	case "MAINT":
		add("Engineering")
		add("Maintenance")
	case "LOG":
		add("Operations")
		add("Logistics")
	case "HR":
		add("Human Resources")
	}
	return out
}
