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
	b := &Backend{config: Config{TapGateway: "http://172.30.0.1:8127"}}

	// No custody ref: the room keeps the open network non-custody placements
	// rely on — no wall, so nil.
	plain := execution.WorkSpec{
		Workspace: execution.Workspace{URL: "https://github.com/itsHabib/rooms"},
		Secrets:   []execution.Secret{{Name: "CURSOR_API_KEY", Ref: "env:CURSOR_API_KEY"}},
	}
	if got := b.egressAllowlist(plain); got != nil {
		t.Fatalf("no custody ref must yield no allowlist, got %v", got)
	}

	// Custody ref present: proxy host + git host, in that order.
	got := b.egressAllowlist(custodyWork("https://github.com/itsHabib/rooms"))
	want := []string{"172.30.0.1", "github.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("allowlist = %v, want %v", got, want)
	}
}

func TestEgressAllowlistAppendsExtrasDedupedDroppingEmpties(t *testing.T) {
	b := &Backend{config: Config{
		TapGateway:  "http://172.30.0.1:8127",
		EgressExtra: []string{"10.0.0.53", "github.com"}, // resolver CIDR + a dup of the git host
	}}
	got := b.egressAllowlist(custodyWork("https://github.com/itsHabib/rooms"))
	want := []string{"172.30.0.1", "github.com", "10.0.0.53"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("allowlist = %v, want %v (dedup github.com, keep order)", got, want)
	}

	// An unset gateway drops that entry rather than allowlisting an empty dest.
	b.config.TapGateway = ""
	got = b.egressAllowlist(custodyWork("https://github.com/itsHabib/rooms"))
	for _, d := range got {
		if d == "" {
			t.Fatalf("empty destination leaked into allowlist: %v", got)
		}
	}
	if got[0] != "github.com" {
		t.Fatalf("unset gateway must be omitted, got %v", got)
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
	args := be.runArgs(prep, task, lc, []string{"172.30.0.1", "github.com"})
	if got := flagValue(args, "--egress"); got != "allowlist:172.30.0.1,github.com" {
		t.Fatalf("--egress = %q, want allowlist:172.30.0.1,github.com; argv=%v", got, args)
	}

	// Empty allowlist -> no flag at all (the non-custody, open-network case).
	if hasFlag(be.runArgs(prep, task, lc, nil), "--egress") {
		t.Fatalf("nil allowlist must not add --egress")
	}
}

func TestGitHostParsesHTTPSAndSCPForms(t *testing.T) {
	cases := map[string]string{
		"https://github.com/itsHabib/rooms":     "github.com",
		"https://github.com/itsHabib/rooms.git": "github.com",
		"git@github.com:itsHabib/rooms.git":     "github.com",
		"http://gitea.internal:3000/o/r.git":    "gitea.internal",
		"":                                      "",
	}
	for in, want := range cases {
		if got := gitHost(in); got != want {
			t.Fatalf("gitHost(%q) = %q, want %q", in, got, want)
		}
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
