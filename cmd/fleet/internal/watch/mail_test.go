package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// deliverFixture: a fresh state root, a deliver command for lead:a that writes its
// prompt and cwd to `out`, and two messages for lead:a — m1 past the grace, m2 fresh.
func deliverFixture(t *testing.T) (out, cwd string, now float64) {
	t.Helper()
	oldState, oldOrg := fleet.State, fleet.OrgState
	fleet.State, fleet.OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { fleet.State, fleet.OrgState = oldState, oldOrg })
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	out, cwd = filepath.Join(t.TempDir(), "prompt.txt"), t.TempDir()
	if err := fleet.WriteJSON(DeliverFile(), fleet.Rec{"lead:a": fleet.Rec{"cwd": cwd, "cmd": []string{"sh", "-c", `printf '%s' "$1" > "$2"; pwd >> "$2"`, "x", "{{prompt}}", out}}}); err != nil {
		t.Fatal(err)
	}
	now = fleet.Now()
	old := fleet.Rec{"id": "m1", "to": "lead:a", "from_role": "lead:top", "kind": "order", "subject": "ship it", "body": "x", "at": now - 60}
	fresh := fleet.Rec{"id": "m2", "to": "lead:a", "from_role": "lead:top", "kind": "report", "subject": "fyi", "body": "x", "at": now}
	for _, m := range []fleet.Rec{old, fresh} {
		if err := fleet.WriteJSON(fleet.MailFile("lead:a", fleet.S(m, "id")), m); err != nil {
			t.Fatal(err)
		}
	}
	return out, cwd, now
}

func waitFor(t *testing.T, p string) string {
	t.Helper()
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", p)
	return ""
}

func launched(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func observedDeliveries(t *testing.T) []fleet.Rec {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir(), "observed.jsonl"))
	var out []fleet.Rec
	for _, l := range strings.Split(string(b), "\n") {
		if r := fleet.ReadJSONBytes([]byte(l)); fleet.S(r, "what") == "deliver" {
			out = append(out, r)
		}
	}
	return out
}

func deliveredBy(role, id string) string {
	return fleet.S(fleet.ReadJSON(fleet.MailFile(role, id)), "delivered_by")
}

func TestDeliverMailLeavesLiveRolesAndFreshMailAlone(t *testing.T) {
	out, _, now := deliverFixture(t)
	// A live session in the role: nothing is launched; the hook line reaches it.
	live := fleet.Path("sessions", "live.json")
	_ = fleet.WriteJSON(live, fleet.Rec{"session": "live", "role": "lead:a", "last_event_at": now, "pid_kind": "harness", "pid": os.Getpid()})
	deliverMail(now)
	if launched(out) || len(observedDeliveries(t)) != 0 || deliveredBy("lead:a", "m1") != "" {
		t.Fatal("launched for a role with a live session")
	}
	_ = os.Remove(live)
	// A role with no deliver entry is left alone, without error.
	_ = fleet.WriteJSON(fleet.MailFile("lead:b", "m3"), fleet.Rec{"id": "m3", "to": "lead:b", "from_role": "lead:top", "kind": "order", "subject": "x", "body": "x", "at": now - 60})
	deliverMail(now)
	if deliveredBy("lead:b", "m3") != "" {
		t.Fatal("delivered with no command configured")
	}
	// The fresh message waited out the grace; only the old one went.
	got := waitFor(t, out)
	if strings.Contains(got, "m2") || deliveredBy("lead:a", "m2") != "" {
		t.Fatalf("fresh message delivered inside the grace: %q", got)
	}
}

func TestDeliverMailLaunchesOncePerMessage(t *testing.T) {
	out, cwd, now := deliverFixture(t)
	deliverMail(now)
	got := waitFor(t, out)
	if !strings.Contains(got, "[fleet] mail m1 from lead:top (order): ship it") || !strings.Contains(got, cwd) {
		t.Fatalf("prompt/cwd: %q", got)
	}
	m1 := fleet.ReadJSON(fleet.MailFile("lead:a", "m1"))
	if !strings.HasPrefix(fleet.S(m1, "delivered_by"), "watch:") || fleet.F(m1, "delivered_at") != now {
		t.Fatal(m1)
	}
	obs := observedDeliveries(t)
	if len(obs) != 1 || fleet.S(obs[0], "role") != "lead:a" || strings.Join(fleet.Strs(obs[0], "mail"), ",") != "m1" || fleet.F(obs[0], "pid") <= 0 {
		t.Fatalf("observed: %v", obs)
	}
	// Never twice for the same message: a later fold re-delivers nothing for m1, and
	// m2 goes once, after the grace.
	_ = os.Remove(out)
	deliverMail(now + 1)
	if launched(out) || len(observedDeliveries(t)) != 1 {
		t.Fatal("launched twice for m1")
	}
	deliverMail(now + MailGrace.Seconds() + 1)
	got = waitFor(t, out)
	if strings.Contains(got, "m1") || !strings.Contains(got, "[fleet] mail m2 from lead:top (report): fyi") || len(observedDeliveries(t)) != 2 {
		t.Fatalf("second delivery: %q, %d launches", got, len(observedDeliveries(t)))
	}
}
