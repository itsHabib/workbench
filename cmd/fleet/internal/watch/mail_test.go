package watch

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func deliveryFixture(t *testing.T) (string, string) {
	t.Helper()
	oldState, oldOrg := fleet.State, fleet.OrgState
	fleet.State, fleet.OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { fleet.State, fleet.OrgState = oldState, oldOrg })
	t.Setenv("FLEET_GITHUB", "off")
	t.Setenv("FLEET_MAIL_GRACE", "0s")
	cwd := t.TempDir()
	_ = os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s tenant hub:a\n", cwd)), 0600)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "launches")
	t.Setenv("FLEET_TEST_MAIL_CHILD", "yes")
	_ = fleet.WriteJSON(fleet.Path("deliver.json"), map[string]mailCommand{"hub:a": {Cwd: cwd, Cmd: []string{exe, "-test.run=^TestMailDeliveryChild$", "--", marker, "{{prompt}}"}}})
	_, err = fleet.PutMail(fleet.Rec{"id": "m1", "to": "hub:a", "from_role": "hub:b", "from_session": "sender", "kind": "order", "subject": "literal $(touch nope)", "body": "body"})
	if err != nil {
		t.Fatal(err)
	}
	return cwd, marker
}

func TestMailDeliveryChild(_ *testing.T) {
	if os.Getenv("FLEET_TEST_MAIL_CHILD") != "yes" {
		return
	}
	marker, prompt := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(3)
	}
	_, err = fmt.Fprintln(f, prompt)
	_ = f.Close()
	if err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestMailDeliveryStampLiveGraceAndReplay(t *testing.T) {
	cwd, marker := deliveryFixture(t)
	t.Setenv("FLEET_MAIL_GRACE", "1h")
	if err := DeliverMail(); err != nil {
		t.Fatal(err)
	}
	r, _ := fleet.ReadMail("hub:a", "m1")
	if fleet.Has(r, "delivered_at") {
		t.Fatal("ignored grace")
	}
	t.Setenv("FLEET_MAIL_GRACE", "0s")
	session := fleet.Rec{"session": "s", "role": "hub:a", "cwd": cwd, "pid_kind": "harness", "pid": os.Getpid(), "last_event_at": fleet.Now()}
	_ = fleet.WriteJSON(fleet.Path("sessions", "s.json"), session)
	if err := DeliverMail(); err != nil {
		t.Fatal(err)
	}
	r, _ = fleet.ReadMail("hub:a", "m1")
	if fleet.Has(r, "delivered_at") {
		t.Fatal("launched for live role")
	}
	session["ended"] = true
	_ = fleet.WriteJSON(fleet.Path("sessions", "s.json"), session)
	before, _ := os.ReadFile(fleet.Path("sessions", "s.json"))
	if _, err := Tick(time.Minute); err != nil {
		t.Fatal(err)
	}
	r, _ = fleet.ReadMail("hub:a", "m1")
	if !fleet.Has(r, "delivered_at") || fleet.S(r, "delivered_by") == "" || fleet.Has(r, "acked_at") {
		t.Fatal(r)
	}
	for i := 0; i < 3; i++ {
		if err := DeliverMail(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	var out []byte
	for time.Now().Before(deadline) {
		out, _ = os.ReadFile(marker)
		if len(out) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Count(string(out), "Mail for hub:a") != 1 || !strings.Contains(string(out), "[fleet] mail m1 from hub:b (order): literal $(touch nope)") {
		t.Fatalf("launch prompt: %s", out)
	}
	after, _ := os.ReadFile(fleet.Path("sessions", "s.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("watch changed session")
	}
	if _, err := fleet.AckMail("hub:a", "m1", "reader"); err != nil {
		t.Fatal(err)
	}
	r, _ = fleet.ReadMail("hub:a", "m1")
	if !fleet.Has(r, "delivered_at") || !fleet.Has(r, "acked_at") {
		t.Fatal("ack lost stamp")
	}
}

func TestMailDeliveryUnknownAndFailedStart(t *testing.T) {
	cwd, _ := deliveryFixture(t)
	_ = fleet.WriteJSON(fleet.Path("sessions", "bad.json"), fleet.Rec{})
	if err := DeliverMail(); err == nil {
		t.Fatal("unknown session treated as dead")
	}
	r, _ := fleet.ReadMail("hub:a", "m1")
	if fleet.Has(r, "delivered_at") {
		t.Fatal("stamped unknown")
	}
	_ = os.Remove(fleet.Path("sessions", "bad.json"))
	_ = fleet.WriteJSON(fleet.Path("deliver.json"), map[string]mailCommand{"hub:a": {Cwd: cwd, Cmd: []string{filepath.Join(cwd, "missing-command")}}})
	if err := DeliverMail(); err == nil {
		t.Fatal("failed start ignored")
	}
	r, _ = fleet.ReadMail("hub:a", "m1")
	if !fleet.Has(r, "delivered_at") {
		t.Fatal("failed attempt not retained")
	}
	before, _ := os.ReadFile(fleet.Path("watch", "observed.jsonl"))
	if err := DeliverMail(); err != nil {
		t.Fatal("failed command retried", err)
	}
	after, _ := os.ReadFile(fleet.Path("watch", "observed.jsonl"))
	if !bytes.Equal(before, after) || !strings.Contains(string(after), "mail-delivery-failed") {
		t.Fatal(string(after))
	}
}

func TestMailDeliveryAckAndWrongDirectory(t *testing.T) {
	_, _ = deliveryFixture(t)
	if _, err := fleet.AckMail("hub:a", "m1", "reader"); err != nil {
		t.Fatal(err)
	}
	if err := DeliverMail(); err != nil {
		t.Fatal(err)
	}
	r, _ := fleet.ReadMail("hub:a", "m1")
	if fleet.Has(r, "delivered_at") {
		t.Fatal("delivered acknowledged mail")
	}
	r["id"] = "m2"
	delete(r, "acked_at")
	delete(r, "acked_by")
	_ = fleet.WriteJSON(fleet.MailPath("hub:a", "m2"), r)
	_ = fleet.WriteJSON(fleet.Path("deliver.json"), map[string]mailCommand{"hub:a": {Cwd: t.TempDir(), Cmd: []string{"true"}}})
	if err := DeliverMail(); err == nil {
		t.Fatal("unroled cwd accepted")
	}
	r, _ = fleet.ReadMail("hub:a", "m2")
	if fleet.Has(r, "delivered_at") {
		t.Fatal("stamped invalid config")
	}
}

func TestMailDeliveryRefusesReboundTenant(t *testing.T) {
	cwd, _ := deliveryFixture(t)
	_ = os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s changed hub:a\n", cwd)), 0600)
	if err := DeliverMail(); err == nil {
		t.Fatal("delivered old tenant mail")
	}
	r := fleet.ReadJSON(fleet.MailPath("hub:a", "m1"))
	if fleet.Has(r, "delivered_at") {
		t.Fatal("stamped old tenant mail")
	}
}

func TestMailDeliveryLaterStampFailureDoesNotStrandEarlierMail(t *testing.T) {
	_, _ = deliveryFixture(t)
	_, err := fleet.PutMail(fleet.Rec{"id": "m2", "to": "hub:a", "from_role": "hub:b", "from_session": "sender", "kind": "order", "subject": "second", "body": "body"})
	if err != nil {
		t.Fatal(err)
	}
	// Fail only m2's publication; m1's reservation must already have its own launch.
	temp := fmt.Sprintf("%s.%d.tmp", fleet.MailPath("hub:a", "m2"), os.Getpid())
	if err := os.Mkdir(temp, 0700); err != nil {
		t.Fatal(err)
	}
	if err := DeliverMail(); err == nil {
		t.Fatal("failed publication ignored")
	}
	r1, _ := fleet.ReadMail("hub:a", "m1")
	r2, _ := fleet.ReadMail("hub:a", "m2")
	if !fleet.Has(r1, "delivered_at") || fleet.Has(r2, "delivered_at") {
		t.Fatal(r1, r2)
	}
	log, _ := os.ReadFile(fleet.Path("watch", "observed.jsonl"))
	if !strings.Contains(string(log), "mail-delivery-started") || !strings.Contains(string(log), "m1") {
		t.Fatal("first stamp stranded without launch", string(log))
	}
}

func TestMailConcurrentDeliveryAndAckPreserveBothStamps(t *testing.T) {
	_, _ = deliveryFixture(t)
	const n = 6
	errs := make(chan error, n+1)
	for i := 0; i < n; i++ {
		go func() { errs <- DeliverMail() }()
	}
	go func() { _, err := fleet.AckMail("hub:a", "m1", "reader"); errs <- err }()
	for i := 0; i < n+1; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	r, _ := fleet.ReadMail("hub:a", "m1")
	if fleet.S(r, "acked_by") != "reader" {
		t.Fatal("lost ack", r)
	}
	log, _ := os.ReadFile(fleet.Path("watch", "observed.jsonl"))
	starts := strings.Count(string(log), "mail-delivery-started")
	if starts > 1 {
		t.Fatal("duplicate launch", string(log))
	}
	if (starts == 1) != fleet.Has(r, "delivered_at") {
		t.Fatal("lost delivery stamp", r, string(log))
	}
}

func TestMailDeliveryUsesResolvedRoleAfterRebinding(t *testing.T) {
	for _, binding := range []string{"hub:b", ""} {
		t.Run("new-role-"+binding, func(t *testing.T) {
			oldCwd, _ := deliveryFixture(t)
			nextCwd := t.TempDir()
			bindings := fmt.Sprintf("%s tenant hub:a\n", nextCwd)
			if binding != "" {
				bindings += fmt.Sprintf("%s tenant %s\n", oldCwd, binding)
			}
			_ = os.WriteFile(fleet.RolesMap(), []byte(bindings), 0600)
			_ = fleet.WriteJSON(fleet.Path("sessions", "s.json"), fleet.Rec{"session": "s", "cwd": oldCwd, "launch_dir": oldCwd, "role": "hub:a", "last_event_at": fleet.Now()})
			live, err := mailRoleLive("hub:a")
			if err != nil || live {
				t.Fatal("cached role suppressed old role delivery", live, err)
			}
			if binding != "" {
				live, err = mailRoleLive(binding)
				if err != nil || !live {
					t.Fatal("resolved role lost live session", live, err)
				}
			}
			config := fleet.ReadJSON(fleet.Path("deliver.json"))
			fleet.M(config, "hub:a")["cwd"] = nextCwd
			_ = fleet.WriteJSON(fleet.Path("deliver.json"), config)
			if err := DeliverMail(); err != nil {
				t.Fatal(err)
			}
			r, _ := fleet.ReadMail("hub:a", "m1")
			if !fleet.Has(r, "delivered_at") {
				t.Fatal("absent role not delivered", r)
			}
		})
	}
}

func TestMailDeliveryMissingDirectoryIdentityIsUnknown(t *testing.T) {
	_, _ = deliveryFixture(t)
	_ = fleet.WriteJSON(fleet.Path("sessions", "s.json"), fleet.Rec{"session": "s", "role": "hub:a", "last_event_at": fleet.Now()})
	if err := DeliverMail(); err == nil {
		t.Fatal("missing identity became permission to launch")
	}
	r, _ := fleet.ReadMail("hub:a", "m1")
	if fleet.Has(r, "delivered_at") {
		t.Fatal("unknown session stamped mail", r)
	}
}
