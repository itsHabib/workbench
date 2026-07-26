package rooms

import (
	"strings"
	"testing"

	"github.com/itsHabib/workbench/contracts/execution"
)

// custodyWork is a WorkSpec carrying one custody ref and the given repo URL —
// the shape that must run behind the enforced egress wall.
func custodyWork(url string) execution.WorkSpec {
	return execution.WorkSpec{
		Workspace: execution.Workspace{URL: url},
		Secrets:   []execution.Secret{{Name: "CUSTODY_GRANT_TRACKER", Ref: "custody:tracker/read"}},
	}
}

func TestEgressAllowlistOnlyWhenCustodyRefPresent(t *testing.T) {
	b := &Backend{config: Config{
		TapGateway:  "http://172.30.0.1:8127",
		EgressExtra: []string{"140.82.112.0/20", "1.1.1.1"},
	}}

	// No custody ref: the room keeps the open network non-custody placements
	// rely on — no wall, so nil.
	plain := execution.WorkSpec{
		Workspace: execution.Workspace{URL: "https://github.com/itsHabib/rooms"},
		Secrets:   []execution.Secret{{Name: "CURSOR_API_KEY", Ref: "env:CURSOR_API_KEY"}},
	}
	if got := b.egressAllowlist(plain); got != nil {
		t.Fatalf("no custody ref must yield no allowlist, got %v", got)
	}

	// Custody ref present: proxy host first, then the operator-supplied git
	// egress. The git host is NOT auto-derived from the URL — hostname pins are
	// unreliable for multi-IP hosts, so git egress comes only from EgressExtra.
	got := b.egressAllowlist(custodyWork("https://github.com/itsHabib/rooms"))
	want := []string{"172.30.0.1", "140.82.112.0/20", "1.1.1.1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("allowlist = %v, want %v", got, want)
	}
}

func TestEgressAllowlistDedupesAndDropsEmpties(t *testing.T) {
	// A dup of the gateway host among the extras: the gateway wins its slot and
	// the dup is dropped, order preserved.
	b := &Backend{config: Config{
		TapGateway:  "http://172.30.0.1:8127",
		EgressExtra: []string{"172.30.0.1", "1.1.1.1"},
	}}
	got := b.egressAllowlist(custodyWork("https://github.com/itsHabib/rooms"))
	want := []string{"172.30.0.1", "1.1.1.1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("allowlist = %v, want %v (dedup gateway, keep order)", got, want)
	}

	// An unset gateway drops that entry rather than allowlisting an empty dest.
	b.config.TapGateway = ""
	b.config.EgressExtra = []string{"140.82.112.0/20"}
	got = b.egressAllowlist(custodyWork("https://github.com/itsHabib/rooms"))
	if len(got) != 1 || got[0] != "140.82.112.0/20" {
		t.Fatalf("unset gateway must be omitted, got %v", got)
	}
}

func TestRequireEgressConfigFailsClosed(t *testing.T) {
	// Custody ref present but no git egress configured: admission must refuse —
	// the wall would sever the in-guest clone with no operator-facing reason.
	b := &Backend{config: Config{TapGateway: "http://172.30.0.1:8127"}}
	err := b.requireEgressConfig(custodyWork("https://github.com/itsHabib/rooms"))
	if err == nil {
		t.Fatal("custody ref + empty EgressExtra must fail admission")
	}
	if !strings.Contains(err.Error(), envEgressExtra) {
		t.Fatalf("error must name the remedy env var %s: %v", envEgressExtra, err)
	}

	// With git egress configured, admission passes.
	b.config.EgressExtra = []string{"140.82.112.0/20", "1.1.1.1"}
	if err := b.requireEgressConfig(custodyWork("https://github.com/itsHabib/rooms")); err != nil {
		t.Fatalf("configured EgressExtra must pass: %v", err)
	}

	// No custody ref: no wall, so EgressExtra is irrelevant — must pass.
	plain := execution.WorkSpec{Secrets: []execution.Secret{{Name: "CURSOR_API_KEY", Ref: "env:CURSOR_API_KEY"}}}
	b.config.EgressExtra = nil
	if err := b.requireEgressConfig(plain); err != nil {
		t.Fatalf("non-custody placement must not require egress config: %v", err)
	}
}

func TestRunArgsEgressFlagTracksAllowlist(t *testing.T) {
	be, prep := helperBackend(t, "success")
	task, err := taskPath(prep)
	if err != nil {
		t.Fatal(err)
	}
	lc := "lifecycle.ndjson"

	// Non-empty allowlist -> a single --egress allowlist:<joined> flag.
	args := be.runArgs(prep, task, lc, []string{"172.30.0.1", "140.82.112.0/20"})
	if got := flagValue(args, "--egress"); got != "allowlist:172.30.0.1,140.82.112.0/20" {
		t.Fatalf("--egress = %q, want allowlist:172.30.0.1,140.82.112.0/20; argv=%v", got, args)
	}

	// Empty allowlist -> no flag at all (the non-custody, open-network case).
	if hasFlag(be.runArgs(prep, task, lc, nil), "--egress") {
		t.Fatalf("nil allowlist must not add --egress")
	}
}

func TestNetworkPostureReportsEnforcement(t *testing.T) {
	cases := []struct {
		egress, witness bool
		want            string
	}{
		{true, true, "egress"},   // wall installed — egress wins over observe
		{true, false, "egress"},  // wall installed even without witness
		{false, true, "observe"}, // no wall, recorded
		{false, false, "open"},   // no wall, unobserved
	}
	for _, c := range cases {
		if got := networkPosture(c.egress, c.witness); got != c.want {
			t.Fatalf("networkPosture(%v,%v) = %q, want %q", c.egress, c.witness, got, c.want)
		}
	}
}

func TestTapGatewayHostStripsSchemeAndPort(t *testing.T) {
	cases := map[string]string{
		"http://172.30.0.1:8127": "172.30.0.1",
		"http://gw:8127":         "gw",
		"":                       "",
		"::not a url::":          "",
	}
	for in, want := range cases {
		if got := (Config{TapGateway: in}).tapGatewayHost(); got != want {
			t.Fatalf("tapGatewayHost(%q) = %q, want %q", in, got, want)
		}
	}
}
