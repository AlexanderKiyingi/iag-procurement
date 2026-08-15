package repo

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/alvor-technologies/iag-platform-go/approvalchain"
)

// The desk matrix ships as data in migration 020, so a typo in it is a
// production routing bug that no compiler catches. These tests read the
// migration itself and prove the matrix it installs is routable and escalates
// where it claims to — the same job parity_test does for the chain definitions
// in the ERP this model came from.

const deskMigration = "020_requisition_desk_chain.sql"

// 020 seeds the matrix and cannot be edited once deployed, so a later migration
// corrects the two money desks that claimed to pay. The tests load the effective
// shipped configuration — the seed with that correction applied — because that
// is what a running database holds.
const deskLabelMigration = "021_requisition_terminal_is_authorization.sql"

var (
	deskLabelSetRe = regexp.MustCompile(
		`SET action_label = '([^']*)',\s*\n?\s*status_label = '([^']*)'`)
	deskLabelPairRe = regexp.MustCompile(`\('([a-z.]+)',\s*'([a-z_]+)'\)`)
)

// applyShippedLabelUpdates rewrites the labels 021 corrects. It fails loudly
// rather than silently skipping: a matrix test that quietly stops reflecting the
// migrations is worse than no test.
func applyShippedLabelUpdates(t *testing.T, byChain map[string][]approvalchain.Desk) {
	t.Helper()
	path := filepath.Join("..", "..", "migrations", deskLabelMigration)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	set := deskLabelSetRe.FindStringSubmatch(string(raw))
	if set == nil {
		t.Fatalf("no label UPDATE parsed from %s — has the statement changed?", deskLabelMigration)
	}
	pairs := deskLabelPairRe.FindAllStringSubmatch(string(raw), -1)
	if len(pairs) == 0 {
		t.Fatalf("no (chain, desk) targets parsed from %s", deskLabelMigration)
	}
	for _, p := range pairs {
		chainKey, deskKey := p[1], p[2]
		desks, ok := byChain[chainKey]
		if !ok {
			t.Fatalf("%s targets chain %q, which %s does not define", deskLabelMigration, chainKey, deskMigration)
		}
		found := false
		for i := range desks {
			if string(desks[i].Key) == deskKey {
				desks[i].ActionLabel, desks[i].StatusLabel = set[1], set[2]
				found = true
			}
		}
		if !found {
			t.Fatalf("%s targets desk %s/%s, which %s does not define",
				deskLabelMigration, chainKey, deskKey, deskMigration)
		}
	}
}

var deskRowRe = regexp.MustCompile(
	`\('([a-z.]+)',\s*(\d+),\s*'([a-z_]+)',\s*'([^']*)',\s*\n?\s*ARRAY\[([^\]]*)\],\s*(\d+),\s*` +
		`'([^']*)',\s*'([^']*)',\s*'([^']*)',\s*'([^']*)',\s*(TRUE|FALSE)\)`)

func loadShippedChains(t *testing.T) []approvalchain.Chain {
	t.Helper()
	path := filepath.Join("..", "..", "migrations", deskMigration)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	matches := deskRowRe.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatalf("no desk rows parsed from %s — has the INSERT format changed?", deskMigration)
	}

	byChain := map[string][]approvalchain.Desk{}
	noRepeat := map[string]bool{}
	var order []string
	for _, m := range matches {
		chainKey, desk, label := m[1], m[3], m[4]
		minAmount, err := strconv.ParseFloat(m[6], 64)
		if err != nil {
			t.Fatalf("desk %s/%s: bad min_amount %q", chainKey, desk, m[6])
		}
		var patterns []string
		for _, p := range strings.Split(m[5], ",") {
			if p = strings.Trim(strings.TrimSpace(p), "'"); p != "" {
				patterns = append(patterns, p)
			}
		}
		if _, seen := byChain[chainKey]; !seen {
			order = append(order, chainKey)
		}
		byChain[chainKey] = append(byChain[chainKey], approvalchain.Desk{
			Key:          approvalchain.DeskKey(desk),
			Label:        label,
			RolePatterns: patterns,
			MinAmount:    minAmount,
			RequiredPerm: m[7],
			ActionLabel:  m[8],
			StatusLabel:  m[9],
			ScopeBy:      m[10],
		})
		if m[11] == "TRUE" {
			noRepeat[chainKey] = true
		}
	}

	// Later migrations edit the seed in place, so they are replayed in order.
	// The tests then describe the configuration a running database actually
	// holds rather than the first migration that touched it.
	applyShippedLabelUpdates(t, byChain)
	terminals := applyShippedDeskRemovals(t, byChain)

	out := make([]approvalchain.Chain, 0, len(order))
	for _, key := range order {
		out = append(out, approvalchain.Chain{
			Key:              key,
			Label:            deskChainLabel(byChain[key], terminals[key]),
			TerminalLabel:    chainTerminal(byChain[key], terminals[key]),
			Desks:            byChain[key],
			NoRepeatApprover: noRepeat[key],
		})
	}
	return out
}

const deskRemovalMigration = "022_commitment_chain_ends_at_commitment.sql"

var (
	deskDeleteRe   = regexp.MustCompile(`(?s)DELETE FROM requisition_approval_desks\s+WHERE \(chain_key, desk\) IN \((.*?)\);`)
	deskTerminalRe = regexp.MustCompile(`SET terminal_label = '([^']*)'\s*\n?\s*WHERE chain_key = '([a-z.]+)'`)
)

// applyShippedDeskRemovals replays 022: the money desks it drops, and the
// chain-level terminals it sets. Returns the terminal per chain.
func applyShippedDeskRemovals(t *testing.T, byChain map[string][]approvalchain.Desk) map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "migrations", deskRemovalMigration)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)

	del := deskDeleteRe.FindStringSubmatch(src)
	if del == nil {
		t.Fatalf("no desk DELETE parsed from %s — has the statement changed?", deskRemovalMigration)
	}
	for _, p := range deskLabelPairRe.FindAllStringSubmatch(del[1], -1) {
		chainKey, deskKey := p[1], p[2]
		desks, ok := byChain[chainKey]
		if !ok {
			t.Fatalf("%s deletes from chain %q, which %s does not define",
				deskRemovalMigration, chainKey, deskMigration)
		}
		kept := desks[:0]
		removed := false
		for _, d := range desks {
			if string(d.Key) == deskKey {
				removed = true
				continue
			}
			kept = append(kept, d)
		}
		if !removed {
			t.Fatalf("%s deletes desk %s/%s, which is not in the seed",
				deskRemovalMigration, chainKey, deskKey)
		}
		byChain[chainKey] = kept
	}

	terminals := map[string]string{}
	for _, m := range deskTerminalRe.FindAllStringSubmatch(src, -1) {
		terminals[m[2]] = m[1]
	}
	if len(terminals) == 0 {
		t.Fatalf("no terminal_label UPDATE parsed from %s", deskRemovalMigration)
	}
	return terminals
}

func shippedEngine(t *testing.T) *approvalchain.Engine {
	t.Helper()
	reg, err := approvalchain.NewRegistry(loadShippedChains(t)...)
	if err != nil {
		t.Fatalf("the shipped desk matrix does not form a valid registry: %v", err)
	}
	return approvalchain.NewEngine(reg)
}

func TestShippedMatrixDefinesTheExpectedChains(t *testing.T) {
	eng := shippedEngine(t)
	for _, key := range []string{ChainRequisition, ChainMaterialStores, ChainMaterialProcurement} {
		if _, ok := eng.Registry().Get(key); !ok {
			t.Errorf("migration %s does not define chain %q", deskMigration, key)
		}
	}
}

func TestShippedRequisitionChainEscalatesOnAmount(t *testing.T) {
	eng := shippedEngine(t)
	chain, _ := eng.Registry().Get(ChainRequisition)

	cases := []struct {
		name   string
		amount float64
		want   []approvalchain.DeskKey
	}{
		// Bands mirror migration 018: supervisor below 5M, manager to 20M,
		// director above. The chain ends at the commitment — disbursement is
		// authorized in finance, against a matched invoice (migration 022).
		{"below 5M", 4_999_999, []approvalchain.DeskKey{"pm", "accounts"}},
		{"at 5M brings in GM", 5_000_000, []approvalchain.DeskKey{"pm", "accounts", "gm"}},
		{"at 20M brings in CEO", 20_000_000, []approvalchain.DeskKey{"pm", "accounts", "gm", "ceo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := chain.Engaged(approvalchain.Options{Amount: tc.amount})
			if len(got) != len(tc.want) {
				t.Fatalf("engaged %d desks, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i].Key != tc.want[i] {
					t.Fatalf("desk %d = %q, want %q", i, got[i].Key, tc.want[i])
				}
			}
		})
	}
}

func TestShippedMaterialChainsForkToDifferentTerminals(t *testing.T) {
	eng := shippedEngine(t)

	stores, _ := eng.Registry().Get(ChainMaterialStores)
	if got := stores.Terminal(); got != "Issued" {
		t.Errorf("stores path ends at %q, want Issued — no money moves on this path", got)
	}
	proc, _ := eng.Registry().Get(ChainMaterialProcurement)
	if got := proc.Terminal(); got != "Follow-up Complete" {
		t.Errorf("procurement path ends at %q, want Follow-up Complete", got)
	}

	// The stores path never touches GM or CEO regardless of value: issuing
	// stock already owned is not a spending decision.
	for _, d := range stores.Engaged(approvalchain.Options{Amount: 500_000_000}) {
		if d.Key == "gm" || d.Key == "ceo" {
			t.Errorf("stores path engaged %q; issuing owned stock should not escalate", d.Key)
		}
	}
}

func TestShippedRolePatternsMatchTheNamesPeopleActuallyUse(t *testing.T) {
	eng := shippedEngine(t)
	chain, _ := eng.Registry().Get(ChainRequisition)

	cases := []struct {
		desk approvalchain.DeskKey
		role string
	}{
		{"pm", "Project Manager"}, {"pm", "PM"},
		{"accounts", "Accounts Assistant"}, {"accounts", "Accountant"}, {"accounts", "accounts asst"},
		{"gm", "General Manager"}, {"gm", "GM"}, {"gm", "Gen. Manager"},
		{"ceo", "CEO"}, {"ceo", "Chief Executive Officer"},
	}
	for _, tc := range cases {
		d, ok := chain.Desk(tc.desk)
		if !ok {
			t.Fatalf("desk %q missing from the shipped matrix", tc.desk)
		}
		if !d.Matches(tc.role) {
			t.Errorf("desk %q does not recognise role %q", tc.desk, tc.role)
		}
	}
}

func TestShippedMatrixKeepsDesksDistinct(t *testing.T) {
	eng := shippedEngine(t)
	chain, _ := eng.Registry().Get(ChainRequisition)

	// A role that matched two desks would let one person clear both, quietly
	// collapsing the chain.
	for _, role := range []string{"Project Manager", "General Manager", "CEO", "Accounts Assistant"} {
		if got := chain.DesksForRole(role); len(got) != 1 {
			t.Errorf("role %q holds %v; each of these should hold exactly one desk", role, got)
		}
	}
}

func TestShippedMatrixScopesThePMDeskToTheProjectOwner(t *testing.T) {
	eng := shippedEngine(t)

	for _, chainKey := range []string{ChainRequisition, ChainMaterialStores} {
		chain, ok := eng.Registry().Get(chainKey)
		if !ok {
			t.Fatalf("chain %q missing", chainKey)
		}
		pm, ok := chain.Desk("pm")
		if !ok {
			t.Fatalf("chain %q has no pm desk", chainKey)
		}
		if pm.ScopeBy != "project_owner" {
			t.Errorf("%s pm desk scopeBy = %q, want project_owner — any PM could otherwise clear any request",
				chainKey, pm.ScopeBy)
		}

		owned := map[string]string{"project_owner": "alice@iag.local"}
		alice := approvalchain.ActorWithRole("alice@iag.local", "Project Manager")
		bob := approvalchain.ActorWithRole("bob@iag.local", "Project Manager")
		if !pm.HoldsFor(alice, owned) {
			t.Errorf("%s: the assigned PM should hold their own request", chainKey)
		}
		if pm.HoldsFor(bob, owned) {
			t.Errorf("%s: a PM who does not own the project must not hold it", chainKey)
		}
		// No owner recorded: the desk stays role-wide rather than stranding it.
		if !pm.HoldsFor(bob, nil) {
			t.Errorf("%s: an unowned request must fall back to the role", chainKey)
		}
	}

	// The senior desks approve on behalf of the company, not a project.
	chain, _ := eng.Registry().Get(ChainRequisition)
	for _, key := range []approvalchain.DeskKey{"gm", "ceo"} {
		d, ok := chain.Desk(key)
		if !ok {
			t.Fatalf("desk %q missing", key)
		}
		if d.ScopeBy != "" {
			t.Errorf("desk %q is scoped by %q; senior desks should stay role-wide", key, d.ScopeBy)
		}
	}
}

// The tiered path in migration 018 refuses an approver who has already signed
// any tier on a requisition. A requisition routed through desks instead must
// not lose that guarantee — otherwise one person holding two roles could clear
// two desks, and moving between mechanisms would quietly weaken the control.
func TestShippedSpendingChainsRefuseARepeatApprover(t *testing.T) {
	eng := shippedEngine(t)

	for _, key := range []string{ChainRequisition, ChainMaterialProcurement} {
		chain, ok := eng.Registry().Get(key)
		if !ok {
			t.Fatalf("chain %q missing", key)
		}
		if !chain.NoRepeatApprover {
			t.Errorf("chain %q allows one person to sign twice; the tiered path it replaces does not", key)
		}
	}

	// Walk it: one person holding both the PM and Accounts roles clears the
	// first desk and is then refused the second.
	both := approvalchain.Actor{
		ID:    "alice@iag.local",
		Roles: []string{"Project Manager", "Accounts Assistant"},
	}
	s := approvalchain.New(ChainRequisition, "requester@iag.local", approvalchain.Options{
		Amount: 1_000_000,
		Scope:  map[string]string{"project_owner": both.ID},
	})
	s, err := eng.Submit(s, approvalchain.ActorWithRole("requester@iag.local", "Clerk"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if s, err = eng.Advance(s, both, ""); err != nil {
		t.Fatalf("pm desk: %v", err)
	}
	if _, err := eng.Advance(s, both, ""); !errors.Is(err, approvalchain.ErrRepeatApprover) {
		t.Fatalf("same person on a second desk: %v, want ErrRepeatApprover", err)
	}
}

func TestShippedChainWalksEndToEnd(t *testing.T) {
	eng := shippedEngine(t)
	requester := approvalchain.ActorWithRole("req@iag.local", "Clerk")
	s := approvalchain.New(ChainRequisition, requester.ID, approvalchain.Options{Amount: 25_000_000})

	var err error
	if s, err = eng.Submit(s, requester); err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, r := range []string{"Project Manager", "Accounts Assistant", "General Manager", "CEO"} {
		if s, err = eng.Advance(s, approvalchain.ActorWithRole(r+"@iag.local", r), ""); err != nil {
			t.Fatalf("advance as %s: %v", r, err)
		}
	}
	if s.Status != approvalchain.StatusApproved {
		t.Fatalf("status = %s, want approved", s.Status)
	}

	prog, err := eng.Progress(s)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	// One outcome, one string. The last desk to sign varies with the amount, so
	// deriving the terminal would make the same state read three different ways
	// — who signed is in the step history, not in this label.
	if prog.StatusLabel != "Approved for Procurement" {
		t.Errorf("terminal label = %q, want Approved for Procurement", prog.StatusLabel)
	}
}

func TestShippedSkipDropsThePMDeskForNonProjectRequests(t *testing.T) {
	eng := shippedEngine(t)
	requester := approvalchain.ActorWithRole("req@iag.local", "Clerk")
	// Fleet and general requests skip the PM desk — the same Skip option the
	// handler passes through from the request body.
	s := approvalchain.New(ChainRequisition, requester.ID, approvalchain.Options{
		Amount: 1_000_000, Skip: []approvalchain.DeskKey{"pm"},
	})

	var err error
	if s, err = eng.Submit(s, requester); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if s.Desk != "accounts" {
		t.Fatalf("first desk = %q, want accounts", s.Desk)
	}
}

func TestChainErrorsMapOntoRepoErrorVocabulary(t *testing.T) {
	cases := []struct {
		in   error
		want error
	}{
		{approvalchain.ErrForbidden, ErrForbidden},
		{approvalchain.ErrSelfApproval, ErrForbidden},
		{approvalchain.ErrNotRequester, ErrForbidden},
		{approvalchain.ErrReasonRequired, ErrInvalidArgument},
		{approvalchain.ErrUnknownChain, ErrInvalidArgument},
		{approvalchain.ErrNoEngagedDesks, ErrInvalidArgument},
		{approvalchain.ErrNotWaiting, ErrConflict},
		{approvalchain.ErrWrongDesk, ErrConflict},
	}
	for _, tc := range cases {
		if got := mapChainErr(tc.in); !errors.Is(got, tc.want) {
			t.Errorf("mapChainErr(%v) = %v, want it to wrap %v", tc.in, got, tc.want)
		}
	}
	if mapChainErr(nil) != nil {
		t.Error("mapChainErr(nil) should stay nil")
	}
}

func TestMigrationSplitsIntoSingleStatements(t *testing.T) {
	// The migrator splits on ";\n\n", so a statement not followed by a blank
	// line silently merges with the next one and the whole migration fails at
	// deploy time rather than here.
	path := filepath.Join("..", "..", "migrations", deskMigration)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sql := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for i, chunk := range strings.Split(sql, ";\n\n") {
		body := stripSQLComments(chunk)
		if strings.TrimSpace(body) == "" {
			continue
		}
		if n := strings.Count(body, ";"); n > 1 {
			t.Errorf("chunk %d holds %d statements; each needs a blank line after its semicolon:\n%.120s", i, n+1, body)
		}
	}
}

func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
