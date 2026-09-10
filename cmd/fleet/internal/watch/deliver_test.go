package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// deliverEnv is a private store, a roles.map binding one role and one seat, and a
// delivery entry whose command is this test binary recording what it was given.
func deliverEnv(t *testing.T) (home string, sink string) {
	t.Helper()
	oldState, oldOrg := fleet.State, fleet.OrgState
	fleet.State, fleet.OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { fleet.State, fleet.OrgState = oldState, oldOrg })
	t.Setenv("FLEET_GITHUB", "off")
	home = t.TempDir()
	seat := filepath.Join(home, "seat")
	if err := os.MkdirAll(seat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.RolesMap(), []byte(home+" t1 hub:lead\n"+seat+" t1 hub:b seat-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink = filepath.Join(t.TempDir(), "launch.json")
	t.Setenv("FLEET_TEST_LAUNCH_PATH", sink)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"hub:lead": map[string]any{"cwd": home,
		"cmd": []any{exe, "-test.run=^TestDeliverRecorder$", "{{prompt}}"}}}
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), cfg); err != nil {
		t.Fatal(err)
	}
	return home, sink
}

// storeDirs is the store's directories for one address, or a failed test.
func storeDirs(t *testing.T, tenant, address string) []string {
	t.Helper()
	dirs, err := fleet.MailStoreDirs(tenant, address)
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

// putStoreMail writes one record straight into the typed mailbox, which is where the
// substrate's own reader looks.
func putStoreMail(t *testing.T, address, id string, at float64, fields fleet.Rec) {
	t.Helper()
	r := fleet.Rec{"id": id, "to": address, "tenant": "t1", "to_kind": "role", "from_role": "hub:b",
		"from_address": "hub:b", "from_kind": "role", "kind": "question", "subject": "unit?", "body": "ms or s", "at": at}
	for k, v := range fields {
		r[k] = v
	}
	if err := fleet.WriteJSON(filepath.Join(storeDirs(t, "t1", address)[0], id+".json"), r); err != nil {
		t.Fatal(err)
	}
}

// launched waits briefly for the detached stub to record its argv, since a fold does
// not wait for what it starts.
func launched(t *testing.T, sink string) map[string]any {
	t.Helper()
	for i := 0; i < 200; i++ {
		b, err := os.ReadFile(sink)
		if err == nil && len(b) > 0 {
			var rec map[string]any
			if err := json.Unmarshal(b, &rec); err == nil {
				return rec
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no launch recorded")
	return nil
}

func observedWhat(observed []fleet.Rec, what string) []fleet.Rec {
	var out []fleet.Rec
	for _, o := range observed {
		if fleet.S(o, "what") == what {
			out = append(out, o)
		}
	}
	return out
}

// The storm case: four unread messages for one absent address are one launch.
func TestDeliverStormIsOneLaunchCarryingEveryMessage(t *testing.T) {
	_, sink := deliverEnv(t)
	now := fleet.Now()
	for _, id := range []string{"q1", "q2", "q3", "q4"} {
		putStoreMail(t, "hub:lead", id, now-60, nil)
	}
	observed := deliver(now)
	if got := len(observedWhat(observed, "mail-delivery-started")); got != 1 {
		t.Fatalf("launches: %d, want 1 (%v)", got, observed)
	}
	rec := launched(t, sink)
	prompt, _ := rec["prompt"].(string)
	for _, id := range []string{"q1", "q2", "q3", "q4"} {
		if !strings.Contains(prompt, id) {
			t.Fatalf("%s missing from the launch prompt: %q", id, prompt)
		}
		r := fleet.ReadJSON(filepath.Join(storeDirs(t, "t1", "hub:lead")[0], id+".json"))
		if !fleet.Has(r, "delivered_at") || fleet.S(r, "delivered_by") != watchAddress {
			t.Fatalf("%s not stamped: %v", id, r)
		}
	}
}

// A message already handed over is never handed over again.
func TestDeliverNeverLaunchesAMessageTwice(t *testing.T) {
	_, sink := deliverEnv(t)
	now := fleet.Now()
	putStoreMail(t, "hub:lead", "q1", now-60, nil)
	if got := len(observedWhat(deliver(now), "mail-delivery-started")); got != 1 {
		t.Fatalf("first fold did not deliver: %d", got)
	}
	launched(t, sink)
	if observed := deliver(fleet.Now()); len(observed) != 0 {
		t.Fatalf("second fold delivered again: %v", observed)
	}
}

// Liveness: an idle session past the grace is absent; a fresh or working one is not.
func TestDeliverTreatsAnIdleHolderAsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session fleet.Rec
		want    int
	}{
		{"idle past the grace", fleet.Rec{"last_event_at": -600.0}, 1},
		{"recently touched", fleet.Rec{"last_event_at": -10.0}, 0},
		{"turn open", fleet.Rec{"last_event_at": -600.0, "turn_open": true}, 0},
		{"ended", fleet.Rec{"last_event_at": -10.0, "ended": true}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := deliverEnv(t)
			now := fleet.Now()
			rec := fleet.Rec{"session": "s1", "cwd": home}
			for k, v := range tc.session {
				if f, ok := v.(float64); ok {
					v = now + f
				}
				rec[k] = v
			}
			if err := fleet.WriteJSON(fleet.Path("sessions", "s1.json"), rec); err != nil {
				t.Fatal(err)
			}
			putStoreMail(t, "hub:lead", "q1", now-60, nil)
			if got := len(observedWhat(deliver(now), "mail-delivery-started")); got != tc.want {
				t.Fatalf("launches: %d, want %d", got, tc.want)
			}
		})
	}
}

// The grace holds a just-arrived message back; an acked one is never carried.
func TestDeliverHonoursGraceAndAcknowledgement(t *testing.T) {
	_, _ = deliverEnv(t)
	now := fleet.Now()
	putStoreMail(t, "hub:lead", "fresh", now-1, nil)
	putStoreMail(t, "hub:lead", "read", now-600, fleet.Rec{"acked_at": now - 500})
	if observed := deliver(now); len(observed) != 0 {
		t.Fatalf("delivered ineligible mail: %v", observed)
	}
}

// The store read: the reader finds the typed mailbox and rejects a foreign tenant.
func TestMailboxRecordsReadsTheStoreDirectly(t *testing.T) {
	_, _ = deliverEnv(t)
	now := fleet.Now()
	putStoreMail(t, "hub:lead", "q1", now-60, nil)
	putStoreMail(t, "hub:lead", "q2", now-30, fleet.Rec{"tenant": "other"})
	rows, err := fleet.MailboxRecords("t1", "hub:lead")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || fleet.S(rows[0], "id") != "q1" {
		t.Fatalf("rows: %v", rows)
	}
	if _, err := fleet.MailboxRecords("t1", "hub:unbound"); err != nil {
		t.Fatalf("an empty mailbox is not an error: %v", err)
	}
	tenant, err := fleet.MailAddressTenant("seat-1")
	if err != nil || tenant != "t1" {
		t.Fatalf("seat tenant: %q %v", tenant, err)
	}
}

// A configured command that cannot run is recorded, and its mail stays undelivered.
func TestDeliverRecordsAFailedLaunch(t *testing.T) {
	home, _ := deliverEnv(t)
	cfg := map[string]any{"hub:lead": map[string]any{"cwd": home, "cmd": []any{filepath.Join(home, "no-such-command"), "{{prompt}}"}}}
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), cfg); err != nil {
		t.Fatal(err)
	}
	now := fleet.Now()
	putStoreMail(t, "hub:lead", "q1", now-60, nil)
	observed := deliver(now)
	if len(observedWhat(observed, "mail-delivery-failed")) != 1 {
		t.Fatalf("failure not recorded: %v", observed)
	}
	if r := fleet.ReadJSON(filepath.Join(storeDirs(t, "t1", "hub:lead")[0], "q1.json")); fleet.Has(r, "delivered_at") {
		t.Fatal("a failed launch consumed the message")
	}
}

// Two folds that overlap — the scheduled watcher and a supported `fleet watch --once`
// — reach launch with the same unstamped rows. Only one of them may start a session.
func TestConcurrentFoldsLaunchOnce(t *testing.T) {
	_, sink := deliverEnv(t)
	now := fleet.Now()
	putStoreMail(t, "hub:lead", "q1", now-60, nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	starts := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n := len(observedWhat(deliver(now), "mail-delivery-started"))
			mu.Lock()
			starts += n
			mu.Unlock()
		}()
	}
	wg.Wait()
	if starts != 1 {
		t.Fatalf("overlapping folds launched %d times, want 1", starts)
	}
	launched(t, sink)
}

// A flat mailbox rebound to another tenant is not this tenant's mail. Its records
// predate the tenant field, so the pin is the only evidence there is.
func TestFlatMailboxIsAdmittedOnlyByItsOwnPin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pin   fleet.Rec
		rows  int
		fails bool
	}{
		{"pinned to this tenant", fleet.Rec{"role": "hub:lead", "tenant": "t1"}, 1, false},
		{"rebound from another tenant", fleet.Rec{"role": "hub:lead", "tenant": "t0"}, 0, false},
		{"pinned to another address", fleet.Rec{"role": "hub:other", "tenant": "t1"}, 0, false},
		{"unpinned", nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = deliverEnv(t)
			now := putFlatMail(t, "hub:lead", tc.pin)
			rows, err := fleet.MailboxRecords("t1", "hub:lead")
			if tc.fails {
				assertUnreadable(t, rows, err, now)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != tc.rows {
				t.Fatalf("rows: %v, want %d", rows, tc.rows)
			}
			if tc.rows == 0 {
				assertNotThisTenants(t, now)
			}
		})
	}
}

// putFlatMail writes one pre-tenant record, and the pin when there is one, into the
// retained flat mailbox. It returns the fold time the record is old enough for.
func putFlatMail(t *testing.T, address string, pin fleet.Rec) float64 {
	t.Helper()
	now := fleet.Now()
	flat := fleet.Path("mail", fleet.Safe(address))
	// A record written before tenants were stored carries none of its own.
	legacy := fleet.Rec{"id": "old1", "to": address, "from_role": "hub:b", "kind": "question",
		"subject": "unit?", "body": "ms or s", "at": now - 600}
	if err := fleet.WriteJSON(filepath.Join(flat, "old1.json"), legacy); err != nil {
		t.Fatal(err)
	}
	if pin == nil {
		return now
	}
	if err := fleet.WriteJSON(filepath.Join(flat, ".address.json"), pin); err != nil {
		t.Fatal(err)
	}
	return now
}

func assertUnreadable(t *testing.T, rows []fleet.Rec, err error, now float64) {
	t.Helper()
	if err == nil {
		t.Fatalf("an unpinned flat mailbox was read anyway: %v", rows)
	}
	if observed := deliver(now); len(observedWhat(observed, "mail-delivery-started")) != 0 {
		t.Fatalf("launched on an unpinned mailbox: %v", observed)
	}
}

func assertNotThisTenants(t *testing.T, now float64) {
	t.Helper()
	if observed := deliver(now); len(observedWhat(observed, "mail-delivery-started")) != 0 {
		t.Fatalf("launched with another tenant's mail: %v", observed)
	}
	if _, err := fleet.StampMail("t1", "hub:lead", "old1", fleet.Rec{"delivered_at": now}); err == nil {
		t.Fatal("stamped a foreign tenant's record as delivered")
	}
}

// TestDeliverRecorder is the stub delivery command: it records the prompt it was
// given and exits, standing in for a harness.
func TestDeliverRecorder(_ *testing.T) {
	p := os.Getenv("FLEET_TEST_LAUNCH_PATH")
	if p == "" {
		return
	}
	cwd, _ := os.Getwd()
	rec := map[string]any{"cwd": cwd, "prompt": os.Args[len(os.Args)-1]}
	b, err := json.Marshal(rec)
	if err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

// The reservation is durable before anything starts. A mailbox that cannot be
// stamped is a fold that does not launch — the alternative is a process holding the
// message with nothing on disk to say so, and a second process next fold holding it
// too, because the record it was handed never stopped being eligible.
func TestDeliverReservesBeforeStartingAndNeverLaunchesUnstampableMail(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory mode this test relies on")
	}
	_, sink := deliverEnv(t)
	now := fleet.Now()
	putStoreMail(t, "hub:lead", "q1", now-60, nil)
	box := storeDirs(t, "t1", "hub:lead")[0]
	if err := os.Chmod(box, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(box, 0o755) })
	observed := deliver(now)
	if got := len(observedWhat(observed, "mail-delivery-started")); got != 0 {
		t.Fatalf("launched with an unreserved message: %v", observed)
	}
	if len(observedWhat(observed, "mail-delivery-failed")) != 1 {
		t.Fatalf("the reservation failure was not recorded: %v", observed)
	}
	if _, err := os.Stat(sink); err == nil {
		t.Fatal("a process was started for a message that could not be reserved")
	}
	// Nothing was consumed: the next fold, with the mailbox writable again, delivers it.
	if err := os.Chmod(box, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := len(observedWhat(deliver(fleet.Now()), "mail-delivery-started")); got != 1 {
		t.Fatalf("the retained message was not delivered on the next fold: %d", got)
	}
	launched(t, sink)
}

// A start that never happened gives the reservation back, so the message is neither
// consumed nor stranded: the next fold carries it.
func TestDeliverReleasesTheReservationWhenTheStartFails(t *testing.T) {
	home, sink := deliverEnv(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	broken := map[string]any{"hub:lead": map[string]any{"cwd": home, "cmd": []any{filepath.Join(home, "no-such-command"), "{{prompt}}"}}}
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), broken); err != nil {
		t.Fatal(err)
	}
	now := fleet.Now()
	putStoreMail(t, "hub:lead", "q1", now-60, nil)
	observed := deliver(now)
	if len(observedWhat(observed, "mail-delivery-failed")) != 1 || len(observedWhat(observed, "mail-reservation-stranded")) != 0 {
		t.Fatalf("failed launch: %v", observed)
	}
	r := fleet.ReadJSON(filepath.Join(storeDirs(t, "t1", "hub:lead")[0], "q1.json"))
	if fleet.Has(r, "delivered_at") || fleet.Has(r, "delivered_by") {
		t.Fatalf("the reservation was not released: %v", r)
	}
	working := map[string]any{"hub:lead": map[string]any{"cwd": home,
		"cmd": []any{exe, "-test.run=^TestDeliverRecorder$", "{{prompt}}"}}}
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), working); err != nil {
		t.Fatal(err)
	}
	if got := len(observedWhat(deliver(fleet.Now()), "mail-delivery-started")); got != 1 {
		t.Fatalf("the released message was not delivered on the next fold: %d", got)
	}
	prompt, _ := launched(t, sink)["prompt"].(string)
	if !strings.Contains(prompt, "q1") {
		t.Fatalf("the next fold carried something else: %q", prompt)
	}
}

// The launch directory must be the address's own checkout, in the same tenant. Mail
// identity comes from the exact directory, so a stale cwd starts a process that
// cannot read what the fold would otherwise have stamped as delivered.
func TestDeliverRefusesALaunchDirectoryThatIsNotTheAddressOwn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pick  func(home, seat, foreign string) string
		start int
	}{
		{"the address's own checkout", func(home, _, _ string) string { return home }, 1},
		{"another address in the same tenant", func(_, seat, _ string) string { return seat }, 0},
		{"a directory bound in another tenant", func(_, _, foreign string) string { return foreign }, 0},
		{"a directory bound to nothing", func(home, _, _ string) string { return filepath.Join(home, "stale") }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, sink := deliverEnv(t)
			seat, foreign := filepath.Join(home, "seat"), t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, "stale"), 0o755); err != nil {
				t.Fatal(err)
			}
			rows := home + " t1 hub:lead\n" + seat + " t1 hub:b seat-1\n" + foreign + " t2 other:lead\n"
			if err := os.WriteFile(fleet.RolesMap(), []byte(rows), 0o644); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cfg := map[string]any{"hub:lead": map[string]any{"cwd": tc.pick(home, seat, foreign),
				"cmd": []any{exe, "-test.run=^TestDeliverRecorder$", "{{prompt}}"}}}
			if err := fleet.WriteJSON(fleet.Path("deliver.json"), cfg); err != nil {
				t.Fatal(err)
			}
			now := fleet.Now()
			putStoreMail(t, "hub:lead", "q1", now-60, nil)
			observed := deliver(now)
			if got := len(observedWhat(observed, "mail-delivery-started")); got != tc.start {
				t.Fatalf("launches: %d, want %d (%v)", got, tc.start, observed)
			}
			if tc.start == 1 {
				launched(t, sink)
				return
			}
			if len(observedWhat(observed, "mail-delivery-unbound")) != 1 {
				t.Fatalf("the refusal was not recorded: %v", observed)
			}
			if r := fleet.ReadJSON(filepath.Join(storeDirs(t, "t1", "hub:lead")[0], "q1.json")); fleet.Has(r, "delivered_at") {
				t.Fatalf("a refused launch consumed the message: %v", r)
			}
		})
	}
}
