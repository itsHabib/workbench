package preflight

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

func live(g Grant) Grant {
	if g.ExpiresAt.IsZero() {
		g.ExpiresAt = now.Add(24 * time.Hour)
	}
	if g.MaxTier == "" {
		g.MaxTier = "T2"
	}
	if g.Repo == "" {
		g.Repo = "itsHabib/workbench"
	}
	if g.ID == "" {
		g.ID = "grt_test"
	}
	return g
}

// TestCheck_WorkbenchTierCeiling replays the production failure this package
// exists for: workbench#242 parked under grant grt_3285c4a1efbf37db (ceiling
// T1) on verdict vrd_e2bcc66923b93736 (tier T3). The Approve tap recorded a
// judgment and then died on grant_tier_exceeded, burning a one-shot approval on
// an outcome that was already determined. The check must call it before the
// button is ever painted.
func TestCheck_WorkbenchTierCeiling(t *testing.T) {
	got := Check(Park{
		Grant:       live(Grant{ID: "grt_3285c4a1efbf37db", MaxTier: "T1", MaxCycles: 3}),
		Found:       true,
		VerdictTier: "T3",
		Cycles:      1,
		CyclesKnown: true,
	}, now)
	if !got.Known || got.Approve {
		t.Fatalf("T3 verdict under a T1 ceiling must be a known refusal, got %+v", got)
	}
	if !strings.Contains(got.Blocker, "T3") || !strings.Contains(got.Blocker, "T1") {
		t.Errorf("blocker must name both tiers, got %q", got.Blocker)
	}
	if got.MintTier != "T3" {
		t.Errorf("MintTier = %q, want T3 — the mint must clear the verdict's tier", got.MintTier)
	}
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name    string
		park    Park
		known   bool
		approve bool
		blocker string
	}{
		{
			name:    "within every ceiling",
			park:    Park{Grant: live(Grant{MaxTier: "T2", MaxCycles: 3}), Found: true, VerdictTier: "T1", Cycles: 1, CyclesKnown: true},
			known:   true,
			approve: true,
		},
		{
			name:    "tier at the ceiling still approves",
			park:    Park{Grant: live(Grant{MaxTier: "T2", MaxCycles: 3}), Found: true, VerdictTier: "T2", Cycles: 0, CyclesKnown: true},
			known:   true,
			approve: true,
		},
		{
			name:    "expired grant",
			park:    Park{Grant: live(Grant{ExpiresAt: now.Add(-time.Hour)}), Found: true, VerdictTier: "T0", CyclesKnown: true},
			known:   true,
			blocker: "expired",
		},
		{
			name:    "expiry is reported before the tier ceiling",
			park:    Park{Grant: live(Grant{MaxTier: "T1", ExpiresAt: now.Add(-time.Hour)}), Found: true, VerdictTier: "T3", CyclesKnown: true},
			known:   true,
			blocker: "expired",
		},
		{
			name:    "cycle ceiling spent",
			park:    Park{Grant: live(Grant{MaxCycles: 3}), Found: true, VerdictTier: "T1", Cycles: 3, CyclesKnown: true},
			known:   true,
			blocker: "cycle 4 exceeds grant ceiling 3",
		},
		{
			name:    "last cycle in budget approves",
			park:    Park{Grant: live(Grant{MaxCycles: 3}), Found: true, VerdictTier: "T1", Cycles: 2, CyclesKnown: true},
			known:   true,
			approve: true,
		},
		{
			name:    "unbounded cycle ceiling never blocks",
			park:    Park{Grant: live(Grant{MaxCycles: 0}), Found: true, VerdictTier: "T1", Cycles: 99, CyclesKnown: true},
			known:   true,
			approve: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Check(c.park, now)
			if got.Known != c.known || got.Approve != c.approve {
				t.Fatalf("Check = %+v, want known=%t approve=%t", got, c.known, c.approve)
			}
			if c.blocker != "" && !strings.Contains(got.Blocker, c.blocker) {
				t.Errorf("blocker = %q, want it to contain %q", got.Blocker, c.blocker)
			}
			if c.approve && got.Blocker != "" {
				t.Errorf("an approvable park must carry no blocker, got %q", got.Blocker)
			}
		})
	}
}

// TestCheck_FailsOpen is the invariant that keeps flare a sink: every gap in
// the recorded facts must resolve to Unknown, so the card renders exactly as it
// did before this check existed. flare may withhold the operator's remote path
// only on a proof — never because it could not read something.
func TestCheck_FailsOpen(t *testing.T) {
	cases := map[string]Park{
		"grant not in the log":     {Found: false, VerdictTier: "T3", CyclesKnown: true},
		"verdict not in the log":   {Grant: live(Grant{MaxTier: "T0"}), Found: true, VerdictTier: ""},
		"unknown verdict tier":     {Grant: live(Grant{MaxTier: "T0"}), Found: true, VerdictTier: "T9", CyclesKnown: true},
		"unknown grant ceiling":    {Grant: live(Grant{MaxTier: "banana"}), Found: true, VerdictTier: "T3", CyclesKnown: true},
		"cycle count underivable":  {Grant: live(Grant{MaxCycles: 1}), Found: true, VerdictTier: "T0", Cycles: 0, CyclesKnown: false},
		"grant with no expiry set": {Grant: Grant{ID: "g", Repo: "r", MaxTier: "T3"}, Found: true, VerdictTier: "T3", CyclesKnown: true},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			got := Check(p, now)
			if got.Known && !got.Approve {
				t.Fatalf("an unreadable fact must never withhold a button, got %+v", got)
			}
		})
	}
}

// TestMintCommand pins the paste-ready remedy: the operator mints at a
// keyboard, so the card owes them one line they can copy without editing.
func TestMintCommand(t *testing.T) {
	res := Check(Park{
		Grant:       live(Grant{MaxTier: "T1", MaxCycles: 3}),
		Found:       true,
		VerdictTier: "T3",
		CyclesKnown: true,
	}, now)
	got := MintCommand("itsHabib/workbench", "/Users/mh/dev/gate/state", res)
	for _, want := range []string{
		"gate grant",
		"-repo itsHabib/workbench",
		"-max-tier T3",
		"-max-cycles 3",
		"-ttl 24h",
		"-state /Users/mh/dev/gate/state",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("mint command %q missing %q", got, want)
		}
	}
}

// TestMintCommand_NoneWhenApprovable keeps the card quiet when there is nothing
// to mint: a mint line beside a working Approve button would read as a blocker.
func TestMintCommand_NoneWhenApprovable(t *testing.T) {
	res := Check(Park{Grant: live(Grant{MaxTier: "T3"}), Found: true, VerdictTier: "T1", CyclesKnown: true}, now)
	if got := MintCommand("itsHabib/workbench", "/state", res); got != "" {
		t.Errorf("MintCommand on an approvable park = %q, want empty", got)
	}
}

// TestTierVocabulary pins the tiers this package compares against to gate's
// own ordering. The values are a wire contract re-stated here rather than
// imported (boundary law), so a drift must fail a test, not a phone tap.
func TestTierVocabulary(t *testing.T) {
	want := []string{"T0", "T1", "T2", "T3"}
	if len(tiers) != len(want) {
		t.Fatalf("tiers = %v, want exactly %v", tiers, want)
	}
	for i, tr := range want {
		rank, ok := tiers[tr]
		if !ok {
			t.Fatalf("tier %q missing", tr)
		}
		if rank != i {
			t.Errorf("tier %q ranks %d, want %d", tr, rank, i)
		}
	}
}
