package survey_test

import (
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/org/internal/home"
	"github.com/itsHabib/workbench/cmd/org/internal/survey"
	"github.com/itsHabib/workbench/contracts/org"
)

const (
	tenant = "acme"
	role   = "lead:platform"
	work   = "github:acme/api#88"
)

var now = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

// chain builds a real chain through the home, so the survey is exercised
// against records a writer actually produced rather than hand-forged ones.
type chain struct {
	t *testing.T
	h *home.Home
}

func newChain(t *testing.T) *chain {
	t.Helper()
	h, err := home.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open home: %v", err)
	}
	c := &chain{t: t, h: h}
	c.add(home.Draft{
		Kind:  org.KindCharter,
		Terms: &org.Terms{Scope: []string{work}, Supervisors: []string{"human:op"}, MinReader: 1},
	})
	return c
}

func (c *chain) add(d home.Draft) {
	c.t.Helper()
	if _, _, err := c.h.Append(tenant, role, d); err != nil {
		c.t.Fatalf("append %s: %v", d.Kind, err)
	}
}

func (c *chain) survey() survey.Role {
	c.t.Helper()
	records, _, err := c.h.Load(tenant, role)
	if err != nil {
		c.t.Fatalf("load: %v", err)
	}
	return survey.Of(tenant, role, records, now)
}

// work fixtures shared by the lifecycle tests.
func (c *chain) attach() { c.add(home.Draft{Kind: org.KindAttach}) }
func (c *chain) assign(w string) {
	c.add(home.Draft{Kind: org.KindAssign, Subject: org.Subject{Work: w, Digest: org.DigestBytes([]byte(w))}})
}
func (c *chain) claim(w string) {
	c.add(home.Draft{Kind: org.KindClaim, Subject: org.Subject{Work: w}})
}
func (c *chain) yield(w string) {
	c.add(home.Draft{Kind: org.KindYield, Subject: org.Subject{Work: w}})
}

// TestOrphanIsCountedFromTheFold is the property the replay exists for: a
// takeover's own record does not name the claim it stranded, so an orphan is
// only visible as a state change. A scan over kinds would count zero here.
func TestOrphanIsCountedFromTheFold(t *testing.T) {
	c := newChain(t)
	c.attach()
	c.assign(work)
	c.claim(work)
	c.add(home.Draft{Kind: org.KindTakeover, Subject: org.Subject{Party: "human:op"}})

	r := c.survey()
	if r.Orphaned != 1 || r.Discharged != 0 {
		t.Fatalf("after a takeover mid-claim: orphaned=%d discharged=%d, want 1/0", r.Orphaned, r.Discharged)
	}
	if r.Dangling != work {
		t.Fatalf("dangling = %q, want %q", r.Dangling, work)
	}
	if r.Incarnations != 2 {
		t.Fatalf("incarnations = %d, want 2 (attach + takeover)", r.Incarnations)
	}

	// The successor closes the inherited obligation.
	c.yield(work)
	r = c.survey()
	if r.Orphaned != 1 || r.Discharged != 1 {
		t.Fatalf("after the successor yields: orphaned=%d discharged=%d, want 1/1", r.Orphaned, r.Discharged)
	}
	if r.Dangling != "" {
		t.Fatalf("dangling = %q, want empty", r.Dangling)
	}
}

// TestDistillCountsSessionEnds pins the ratio the whole bet rests on: a
// checkpoint is a conclusion, a mark is only the fact that a session ended.
func TestDistillCountsSessionEnds(t *testing.T) {
	c := newChain(t)
	c.attach()
	c.add(home.Draft{Kind: org.KindMark, Body: []byte("session ended")})
	c.add(home.Draft{Kind: org.KindCheckpoint, Body: []byte("what I concluded")})
	c.add(home.Draft{Kind: org.KindMark, Body: []byte("session ended")})

	r := c.survey()
	if r.Checkpoints != 1 || r.Marks != 2 {
		t.Fatalf("checkpoints=%d marks=%d, want 1/2", r.Checkpoints, r.Marks)
	}
	if !r.Degraded {
		t.Fatal("a mark at the tip must render degraded")
	}

	tot := survey.Sum([]survey.Role{r})
	if got := tot.DistillRate; got < 0.32 || got > 0.34 {
		t.Fatalf("distill rate = %v, want ~1/3", got)
	}
}

// TestNoDataIsNotZero guards the finding that matters most: "no session has
// ended yet" and "every session ended undistilled" must never share a value.
func TestNoDataIsNotZero(t *testing.T) {
	c := newChain(t)
	c.attach()

	tot := survey.Sum([]survey.Role{c.survey()})
	if tot.DistillRate != -1 || tot.DischargeRate != -1 {
		t.Fatalf("with no ends and no orphans: distill=%v discharge=%v, want -1/-1",
			tot.DistillRate, tot.DischargeRate)
	}

	c.add(home.Draft{Kind: org.KindMark, Body: []byte("ended")})
	tot = survey.Sum([]survey.Role{c.survey()})
	if tot.DistillRate != 0 {
		t.Fatalf("with one undistilled end: distill=%v, want 0", tot.DistillRate)
	}
}

// TestClaimsAndTerminals pins the plain lifecycle counts.
func TestClaimsAndTerminals(t *testing.T) {
	c := newChain(t)
	c.attach()
	c.assign(work)
	c.claim(work)
	c.yield(work)
	c.claim(work)

	r := c.survey()
	if r.Claims != 2 || r.Terminals != 1 {
		t.Fatalf("claims=%d terminals=%d, want 2/1", r.Claims, r.Terminals)
	}
	if r.Phase != org.PhaseActive {
		t.Fatalf("phase = %s, want active (the second claim is still open)", r.Phase)
	}
}

// TestBrokenChainIsAFindingNotAFailure proves a corrupt tail does not erase
// the evidence in the head — a sweep that refuses to report anything about a
// broken chain is the sweep nobody runs twice.
func TestBrokenChainIsAFindingNotAFailure(t *testing.T) {
	c := newChain(t)
	c.attach()
	c.add(home.Draft{Kind: org.KindCheckpoint, Body: []byte("real work happened")})
	records, _, err := c.h.Load(tenant, role)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Forge a tail the kernel must refuse: a claim on unheld work.
	broken := append(records, org.Record{
		V: org.Version, Scheme: org.Scheme, Seq: int64(len(records) + 1),
		Tenant: tenant, Role: role, Kind: org.KindClaim, KindClass: org.ClassStructural,
		Subject: org.Subject{Work: "jira:NOPE-1"},
	})

	r := survey.Of(tenant, role, broken, now)
	if r.Err == "" {
		t.Fatal("a chain that does not fold must be reported")
	}
	if r.Checkpoints != 1 {
		t.Fatalf("checkpoints = %d, want 1 — counts before the break must survive", r.Checkpoints)
	}
	if tot := survey.Sum([]survey.Role{r}); tot.Broken != 1 {
		t.Fatalf("broken = %d, want 1", tot.Broken)
	}
}

// TestLatenessFromDeclaredDeadline proves liveness is derived from what the
// writer declared, and that a garbled deadline reads as late.
func TestLatenessFromDeclaredDeadline(t *testing.T) {
	c := newChain(t)
	c.add(home.Draft{Kind: org.KindAttach, NextDue: now.Add(-time.Hour)})
	if r := c.survey(); !r.Late {
		t.Fatal("a writer past its own declared deadline must read as late")
	}

	fresh := newChain(t)
	fresh.add(home.Draft{Kind: org.KindAttach, NextDue: now.Add(time.Hour)})
	if r := fresh.survey(); r.Late {
		t.Fatal("a writer inside its own deadline must not read as late")
	}
}

// TestRevokeOrphansToo pins the second displacement path. Revoke reaches the
// same orphan() in the kernel as takeover, but through a different transition
// — and "high confidence by inspection" is how the untested arm of a pair
// eventually diverges.
func TestRevokeOrphansToo(t *testing.T) {
	c := newChain(t)
	c.attach()
	c.assign(work)
	c.claim(work)
	c.add(home.Draft{Kind: org.KindRevoke, Subject: org.Subject{Party: "human:op"}})

	r := c.survey()
	if r.Orphaned != 1 || r.Dangling != work {
		t.Fatalf("after a revoke mid-claim: orphaned=%d dangling=%q, want 1/%q", r.Orphaned, r.Dangling, work)
	}
	// A revoke returns the role to Chartered rather than minting a holder, so
	// it must not be counted as an incarnation.
	if r.Incarnations != 1 {
		t.Fatalf("incarnations = %d, want 1 (attach only — a revoke mints nobody)", r.Incarnations)
	}
}

// TestBrokenChainKeepsItsObligation is the fix for the review's P2: a chain
// that stops folding must still report the obligation outstanding at the
// break. BROKEN says the state is uncertain; it must not say the stranded
// work is absent.
func TestBrokenChainKeepsItsObligation(t *testing.T) {
	c := newChain(t)
	c.attach()
	c.assign(work)
	c.claim(work)
	c.add(home.Draft{Kind: org.KindTakeover, Subject: org.Subject{Party: "human:op"}})
	records, _, err := c.h.Load(tenant, role)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// A tail the kernel refuses: a claim while an inherited obligation is open.
	broken := append(records, org.Record{
		V: org.Version, Scheme: org.Scheme, Seq: int64(len(records) + 1),
		Tenant: tenant, Role: role, Kind: org.KindClaim, KindClass: org.ClassStructural,
		Subject: org.Subject{Work: work},
	})

	r := survey.Of(tenant, role, broken, now)
	if r.Err == "" {
		t.Fatal("the forged tail must be reported as broken")
	}
	if r.Dangling != work {
		t.Fatalf("dangling = %q, want %q — a broken chain must not hide stranded work", r.Dangling, work)
	}
	if r.Orphaned != 1 {
		t.Fatalf("orphaned = %d, want 1", r.Orphaned)
	}
	if tot := survey.Sum([]survey.Role{r}); tot.Dangling != 1 {
		t.Fatalf("totals dangling = %d, want 1 — the aggregate must not undercount", tot.Dangling)
	}
}

// TestBrokenChainReportsWhenItLastHeld pins the round-2 finding: a BROKEN row
// exists to be diagnosed from, and a reader cannot diagnose a chain that will
// not say when it was last valid.
func TestBrokenChainReportsWhenItLastHeld(t *testing.T) {
	c := newChain(t)
	c.attach()
	records, _, err := c.h.Load(tenant, role)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	lastGood := records[len(records)-1].At

	broken := append(records, org.Record{
		V: org.Version, Scheme: org.Scheme, Seq: int64(len(records) + 1),
		Tenant: tenant, Role: role, Kind: org.KindClaim, KindClass: org.ClassStructural,
		Subject: org.Subject{Work: "jira:NOPE-1"},
	})

	r := survey.Of(tenant, role, broken, now)
	if r.Err == "" {
		t.Fatal("the forged tail must be reported as broken")
	}
	if r.LastAt != lastGood {
		t.Fatalf("last_at = %q, want %q — the last record that folded", r.LastAt, lastGood)
	}
}
