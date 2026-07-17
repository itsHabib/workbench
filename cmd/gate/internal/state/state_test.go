package state

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir(), func() time.Time { return time.Unix(1000, 0) })
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// A single artifact larger than the old 16 MiB scanner ceiling must round-trip
// and not brick later scans — this is what an oversized-PR diff evidence line
// can reach.
func TestLargeArtifactRoundTrips(t *testing.T) {
	st := open(t)
	run := NewRunID()
	big := strings.Repeat("x", 20*1024*1024) // 20 MiB, over the old ceiling
	evd, err := st.Append(KindEvidence, run, nil, map[string]string{"diff": big})
	if err != nil {
		t.Fatalf("append large: %v", err)
	}
	// A following append must still be able to scan the log (count + chain).
	if _, err := st.Append(KindVerdict, run, nil, map[string]string{"d": "pass"}); err != nil {
		t.Fatalf("append after large (store bricked?): %v", err)
	}
	got, err := st.Get(evd.ID)
	if err != nil {
		t.Fatalf("get large: %v", err)
	}
	var body struct {
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(got.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Diff) != len(big) {
		t.Fatalf("round-trip truncated: got %d bytes, want %d", len(body.Diff), len(big))
	}
	if res, err := st.Audit(); err != nil || !res.OK {
		t.Fatalf("audit after large artifact: ok=%v err=%v reason=%s", res.OK, err, res.Reason)
	}
}

func TestAuditCatchesTamper(t *testing.T) {
	st := open(t)
	run := NewRunID()
	if _, err := st.Append(KindEvidence, run, nil, map[string]string{"k": "v1"}); err != nil {
		t.Fatal(err)
	}
	target, err := st.Append(KindVerdict, run, nil, map[string]string{"decision": "block"})
	if err != nil {
		t.Fatal(err)
	}

	if res, _ := st.Audit(); !res.OK {
		t.Fatalf("clean chain reported tampered at %s: %s", res.Artifact, res.Reason)
	}

	// Rewrite the verdict body in place — the audit must catch it.
	raw, err := os.ReadFile(st.logPath())
	if err != nil {
		t.Fatal(err)
	}
	tamper := strings.Replace(string(raw), `\"decision\":\"block\"`, `\"decision\":\"pass\"`, 1)
	if tamper == string(raw) {
		tamper = strings.Replace(string(raw), `"decision":"block"`, `"decision":"pass"`, 1)
	}
	if tamper == string(raw) {
		t.Fatal("test setup: tamper had no effect")
	}
	if err := os.WriteFile(st.logPath(), []byte(tamper), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Artifact != target.ID {
		t.Fatalf("audit missed the tamper: got %+v want artifact %q", res, target.ID)
	}
}

func TestAuditReturnsVerifiedSnapshot(t *testing.T) {
	st := open(t)
	run := NewRunID()
	first, err := st.Append(KindEvidence, run, nil, map[string]string{"k": "v1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.Append(KindVerdict, run, nil, map[string]string{"decision": "pass"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("clean chain reported tampered: %s", res.Reason)
	}
	if len(res.All) != 2 || res.All[0].ID != first.ID || res.All[1].ID != second.ID {
		t.Fatalf("audit snapshot does not match the appended log: %+v", res.All)
	}

	// A tampered log must not hand back a snapshot a caller could count from.
	raw, err := os.ReadFile(st.logPath())
	if err != nil {
		t.Fatal(err)
	}
	tamper := strings.Replace(string(raw), `"decision":"pass"`, `"decision":"XXXX"`, 1)
	if tamper == string(raw) {
		t.Fatal("test setup: tamper had no effect")
	}
	if err := os.WriteFile(st.logPath(), []byte(tamper), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = st.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.All != nil {
		t.Fatalf("tampered log returned a snapshot: OK=%v len(All)=%d", res.OK, len(res.All))
	}
}

func TestProvenanceRoundTrip(t *testing.T) {
	st := open(t)
	run := NewRunID()
	evd, err := st.Append(KindEvidence, run, nil, map[string]int{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	vrd, err := st.Append(KindVerdict, run, []string{evd.ID}, map[string]string{"d": "pass"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(vrd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Parents) != 1 || got.Parents[0] != evd.ID {
		t.Fatalf("provenance lost: %v", got.Parents)
	}
	var body map[string]string
	if err := json.Unmarshal(got.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["d"] != "pass" {
		t.Fatalf("body lost: %v", body)
	}
}
