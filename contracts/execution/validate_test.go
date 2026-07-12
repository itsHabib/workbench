package execution

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func decodeWorkSpecFixture(t *testing.T, name string) WorkSpec {
	t.Helper()
	w, err := DecodeWorkSpec(readFixture(t, name))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return w
}

func decodeRequestFixture(t *testing.T, name string) Request {
	t.Helper()
	r, err := DecodeRequest(readFixture(t, name))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return r
}

func decodeResultFixture(t *testing.T, name string) Result {
	t.Helper()
	r, err := DecodeResult(readFixture(t, name))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return r
}

func strp(s string) *string { return &s }

func TestValidateWorkSpec_Valid(t *testing.T) {
	for _, f := range []string{"work-spec-name.json", "work-spec-path.json"} {
		if err := ValidateWorkSpec(decodeWorkSpecFixture(t, f)); err != nil {
			t.Errorf("%s: golden fixture must admit: %v", f, err)
		}
	}
}

// TestValidateWorkSpec_Rejections is the Gate A failing-case suite for the
// work-spec laws: path/traversal over every position, union exclusivity,
// workspace immutability, secret grammar, digest shape. Each case mutates the
// golden fixture in exactly one way and must reject.
func TestValidateWorkSpec_Rejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*WorkSpec)
	}{
		{"absolute cwd", func(w *WorkSpec) { w.Cwd.Value = "/etc/passwd" }},
		{"windows absolute cwd", func(w *WorkSpec) { w.Cwd.Value = `C:\evil` }},
		{"backslash absolute cwd", func(w *WorkSpec) { w.Cwd.Value = `\\share\x` }},
		{"traversing cwd", func(w *WorkSpec) { w.Cwd.Value = "../above" }},
		{"embedded traversal in cwd", func(w *WorkSpec) { w.Cwd.Value = "ok/../../above" }},
		{"unknown cwd root", func(w *WorkSpec) { w.Cwd.Root = "home" }},
		{"empty cwd value", func(w *WorkSpec) { w.Cwd.Value = "" }},
		{"traversing path arg", func(w *WorkSpec) { w.Command.Args[0].Path.Value = "../../etc/shadow" }},
		{"absolute path arg", func(w *WorkSpec) { w.Command.Args[0].Path.Value = "/etc/shadow" }},
		{"unknown path arg root", func(w *WorkSpec) { w.Command.Args[0].Path.Root = "host" }},
		{"arg with both variants", func(w *WorkSpec) {
			w.Command.Args[0] = Arg{Literal: strp("x"), Path: &PathRef{Root: RootInputs, Value: "y"}}
		}},
		{"arg with neither variant", func(w *WorkSpec) { w.Command.Args[0] = Arg{} }},
		{"executable with both variants", func(w *WorkSpec) {
			w.Command.Executable = Executable{Name: strp("node"), Path: &PathRef{Root: RootInputs, Value: "bin/x"}}
		}},
		{"executable with neither variant", func(w *WorkSpec) { w.Command.Executable = Executable{} }},
		{"executable name smuggling a path", func(w *WorkSpec) { w.Command.Executable = Executable{Name: strp("bin/node")} }},
		{"executable name smuggling a drive", func(w *WorkSpec) { w.Command.Executable = Executable{Name: strp(`C:node.exe`)} }},
		{"empty executable name", func(w *WorkSpec) { w.Command.Executable = Executable{Name: strp("")} }},
		{"traversing executable path", func(w *WorkSpec) {
			w.Command.Executable = Executable{Path: &PathRef{Root: RootInputs, Value: "../host-bin"}}
		}},
		{"symbolic workspace revision", func(w *WorkSpec) { w.Workspace.Revision = "main" }},
		{"short workspace revision", func(w *WorkSpec) { w.Workspace.Revision = "57aa8b2" }},
		{"uppercase workspace revision", func(w *WorkSpec) { w.Workspace.Revision = strings.ToUpper(w.Workspace.Revision) }},
		{"unknown workspace kind", func(w *WorkSpec) { w.Workspace.Kind = "svn" }},
		{"empty workspace url", func(w *WorkSpec) { w.Workspace.URL = "" }},
		{"absolute input source", func(w *WorkSpec) { w.Inputs[0].Source = "/etc/passwd" }},
		{"traversing input source", func(w *WorkSpec) { w.Inputs[0].Source = "../outside-bundle" }},
		{"traversing input target", func(w *WorkSpec) { w.Inputs[0].Target = "../outside-inputs" }},
		{"absolute input target", func(w *WorkSpec) { w.Inputs[0].Target = `D:\x` }},
		{"input digest not 64-hex", func(w *WorkSpec) { w.Inputs[0].SHA256 = "hex" }},
		{"input digest uppercase", func(w *WorkSpec) { w.Inputs[0].SHA256 = strings.ToUpper(w.Inputs[0].SHA256) }},
		{"inline secret value", func(w *WorkSpec) { w.Secrets[0].Ref = "hunter2" }},
		{"non-env secret scheme", func(w *WorkSpec) { w.Secrets[0].Ref = "file:/etc/secret" }},
		{"secret ref leading digit", func(w *WorkSpec) { w.Secrets[0].Ref = "env:1BAD" }},
		{"secret ref empty env name", func(w *WorkSpec) { w.Secrets[0].Ref = "env:" }},
		{"empty secret name", func(w *WorkSpec) { w.Secrets[0].Name = "" }},
		{"traversing output path", func(w *WorkSpec) { w.Outputs[0].Path = "../outside-out" }},
		{"absolute output path", func(w *WorkSpec) { w.Outputs[0].Path = "/tmp/x" }},
		{"unrecognized version", func(w *WorkSpec) { w.SchemaVersion = "0.2.0" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := decodeWorkSpecFixture(t, "work-spec-name.json")
			c.mutate(&w)
			if ValidateWorkSpec(w) == nil {
				t.Fatal("mutation must reject")
			}
		})
	}
}

func TestValidateRequest_Valid(t *testing.T) {
	for _, f := range []string{"request.json", "request-local.json", "request-rooms.json"} {
		if err := ValidateRequest(decodeRequestFixture(t, f)); err != nil {
			t.Errorf("%s: golden fixture must admit: %v", f, err)
		}
	}
}

func TestValidateRequest_Rejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Request)
	}{
		{"empty request_id", func(r *Request) { r.RequestID = "" }},
		{"traversing manifest", func(r *Request) { r.Work.Manifest = "../work.json" }},
		{"absolute manifest", func(r *Request) { r.Work.Manifest = "/work.json" }},
		{"work digest not 64-hex", func(r *Request) { r.Work.SHA256 = "not-a-digest" }},
		{"backend with path separator", func(r *Request) { r.Placement.Backend = "adapters/rooms" }},
		{"backend host-path shape", func(r *Request) { r.Placement.Backend = `C:\rooms` }},
		{"backend with traversal", func(r *Request) { r.Placement.Backend = ".." }},
		{"empty backend", func(r *Request) { r.Placement.Backend = "" }},
		{"profile with path separator", func(r *Request) { r.Placement.Profile = `profiles\agent` }},
		{"empty profile", func(r *Request) { r.Placement.Profile = "" }},
		{"zero deadline", func(r *Request) { r.Policy.DeadlineMS = 0 }},
		{"negative deadline", func(r *Request) { r.Policy.DeadlineMS = -1 }},
		{"negative cancel grace", func(r *Request) { r.Policy.CancelGraceMS = -1 }},
		{"unrecognized version", func(r *Request) { r.SchemaVersion = "9.9.9" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := decodeRequestFixture(t, "request.json")
			c.mutate(&r)
			if ValidateRequest(r) == nil {
				t.Fatal("mutation must reject")
			}
		})
	}
}

func TestValidateResult_Valid(t *testing.T) {
	if err := ValidateResult(decodeResultFixture(t, "result.json")); err != nil {
		t.Errorf("golden fixture must admit: %v", err)
	}
}

// TestValidateResult_TerminalLaws pins the legal (status, terminal_phase,
// reason_code) set derived from TDD §7 Flows A–F. For deadline_exceeded and
// cancel_requested the TDD does not pin a terminal phase — the run ends in
// whichever phase the interruption landed — so any canonical phase is legal
// for those two reasons.
func TestValidateResult_TerminalLaws(t *testing.T) {
	legal := []struct{ status, phase, reason string }{
		{StatusSucceeded, PhaseTerminal, ReasonCompleted},         // Flow A
		{StatusFailed, PhasePreparation, ReasonPreparationFailed}, // §5
		{StatusFailed, PhaseStartup, ReasonStartupFailed},         // §5
		{StatusFailed, PhaseStartup, ReasonPlacementUnavailable},  // Flow B
		{StatusFailed, PhaseWorkload, ReasonWorkloadFailed},       // D7
		{StatusFailed, PhaseCollection, ReasonCollectionFailed},   // Flow E
		{StatusFailed, PhaseCleanup, ReasonCleanupFailed},         // Flow C escalation
		{StatusFailed, PhaseTerminal, ReasonControllerLost},       // Flow F
		{StatusTimedOut, PhaseWorkload, ReasonDeadlineExceeded},   // Flow C
		{StatusTimedOut, PhaseCleanup, ReasonDeadlineExceeded},    // Flow C, late expiry
		{StatusCancelled, PhaseStartup, ReasonCancelRequested},    // Flow D
		{StatusCancelled, PhaseWorkload, ReasonCancelRequested},   // Flow D
	}
	for _, c := range legal {
		t.Run(fmt.Sprintf("legal %s %s %s", c.status, c.phase, c.reason), func(t *testing.T) {
			r := decodeResultFixture(t, "result.json")
			r.Status, r.TerminalPhase, r.ReasonCode = c.status, c.phase, c.reason
			if err := ValidateResult(r); err != nil {
				t.Fatalf("legal combination must admit: %v", err)
			}
		})
	}

	illegal := []struct{ status, phase, reason string }{
		{StatusSucceeded, PhaseWorkload, ReasonCompleted},         // success only at terminal
		{StatusSucceeded, PhaseTerminal, ReasonWorkloadFailed},    // failure reason, success status
		{StatusFailed, PhaseTerminal, ReasonCompleted},            // completed cannot fail
		{StatusFailed, PhaseWorkload, ReasonStartupFailed},        // phase/reason mismatch
		{StatusFailed, PhaseWorkload, ReasonPlacementUnavailable}, // Flow B pins startup
		{StatusFailed, PhaseCleanup, ReasonControllerLost},        // Flow F pins terminal
		{StatusTimedOut, PhaseTerminal, ReasonCompleted},          // timed_out requires deadline_exceeded
		{StatusTimedOut, PhaseWorkload, ReasonCancelRequested},    // cancel is a cancelled status
		{StatusCancelled, PhaseWorkload, ReasonDeadlineExceeded},  // deadline is a timed_out status
		{"exploded", PhaseTerminal, ReasonCompleted},              // unknown status
		{StatusFailed, "warmup", ReasonWorkloadFailed},            // unknown phase
		{StatusFailed, PhaseWorkload, "gremlins"},                 // unknown reason
	}
	for _, c := range illegal {
		t.Run(fmt.Sprintf("illegal %s %s %s", c.status, c.phase, c.reason), func(t *testing.T) {
			r := decodeResultFixture(t, "result.json")
			r.Status, r.TerminalPhase, r.ReasonCode = c.status, c.phase, c.reason
			if ValidateResult(r) == nil {
				t.Fatal("illegal combination must reject")
			}
		})
	}
}

func TestValidateResult_Rejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Result)
	}{
		{"request digest not 64-hex", func(r *Result) { r.RequestSHA256 = "hex" }},
		{"work digest not 64-hex", func(r *Result) { r.WorkSHA256 = "hex" }},
		{"cause with unknown phase", func(r *Result) { r.Causes = []Cause{{Phase: "warmup", ReasonCode: ReasonDeadlineExceeded}} }},
		{"cause with unknown reason", func(r *Result) { r.Causes = []Cause{{Phase: PhaseWorkload, ReasonCode: "gremlins"}} }},
		{"reserved live stream delivery", func(r *Result) { r.Placement.StreamDelivery = "live" }},
		{"empty stream delivery", func(r *Result) { r.Placement.StreamDelivery = "" }},
		{"image digest not 64-hex", func(r *Result) { r.Placement.ImageSHA256 = "sha256:abc" }},
		{"receipt backend with separator", func(r *Result) { r.Placement.Backend = "adapters/rooms" }},
		{"receipt profile host-path shape", func(r *Result) { r.Placement.Profile = `C:\profiles\x` }},
		{"traversing artifact path", func(r *Result) { r.Artifacts[0].Path = "../escape" }},
		{"absolute artifact path", func(r *Result) { r.Artifacts[0].Path = "/etc/passwd" }},
		{"artifact digest not 64-hex", func(r *Result) { r.Artifacts[0].SHA256 = "hex" }},
		{"negative artifact size", func(r *Result) { r.Artifacts[0].Size = -1 }},
		{"unrecognized version", func(r *Result) { r.SchemaVersion = "0.2.0" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := decodeResultFixture(t, "result.json")
			c.mutate(&r)
			if ValidateResult(r) == nil {
				t.Fatal("mutation must reject")
			}
		})
	}
}

// TestValidateResult_CausesAndAbsentImage covers the admitting side of the
// optional fields: a Flow C escalation (cleanup_failed primary with a prior
// deadline_exceeded cause) and a local receipt with no image identity.
func TestValidateResult_CausesAndAbsentImage(t *testing.T) {
	r := decodeResultFixture(t, "result.json")
	r.Status, r.TerminalPhase, r.ReasonCode = StatusFailed, PhaseCleanup, ReasonCleanupFailed
	r.Causes = []Cause{{Phase: PhaseWorkload, ReasonCode: ReasonDeadlineExceeded, Message: "deadline hit during workload"}}
	r.Placement.ImageSHA256 = ""
	if err := ValidateResult(r); err != nil {
		t.Fatalf("escalated result with prior cause and no image identity must admit: %v", err)
	}
}

// TestWorkDigestPlacementInvariant is the Gate A cross-placement law (D5,
// G8): the same exact submitted work.json bytes produce the same work digest
// regardless of placement, while the request digests differ because the
// placement bytes differ. The package computes no digests — this test
// computes them to prove the law over a fixture pair differing only in
// placement.
func TestWorkDigestPlacementInvariant(t *testing.T) {
	work := []byte(`{"schema_version":"0.1.0","command":{"executable":{"name":"node"},"args":[{"path":{"root":"inputs","value":"runner.js"}}]},"cwd":{"root":"workspace","value":"."},"workspace":{"kind":"git","url":"https://github.com/itsHabib/agent-sandbox","revision":"57aa8b2c7a9531d5d6ba060a77247f9bfca0470f"}}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(work))

	localRaw := readFixture(t, "request-local.json")
	roomsRaw := readFixture(t, "request-rooms.json")
	local, err := DecodeRequest(localRaw)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := DecodeRequest(roomsRaw)
	if err != nil {
		t.Fatal(err)
	}

	if local.Placement == rooms.Placement {
		t.Fatal("the fixture pair must differ in placement")
	}
	if local.Work != rooms.Work || local.Policy != rooms.Policy || local.RequestID != rooms.RequestID {
		t.Fatal("the fixture pair must differ ONLY in placement")
	}
	if local.Work.SHA256 != digest {
		t.Fatalf("local request work digest %q != digest of the shared work bytes %q", local.Work.SHA256, digest)
	}
	if rooms.Work.SHA256 != digest {
		t.Fatalf("rooms request work digest %q != digest of the shared work bytes %q", rooms.Work.SHA256, digest)
	}
	if sha256.Sum256(localRaw) == sha256.Sum256(roomsRaw) {
		t.Fatal("request digests must differ when the placement bytes differ")
	}
}

// TestRequestDigestChangesWithAnyByte pins the exact-bytes digest basis (D5):
// the request digest is over the submitted bytes with no canonical re-encode,
// so flipping any single byte changes it.
func TestRequestDigestChangesWithAnyByte(t *testing.T) {
	raw := readFixture(t, "request-local.json")
	base := sha256.Sum256(raw)
	for _, i := range []int{0, len(raw) / 2, len(raw) - 1} {
		mutated := bytes.Clone(raw)
		mutated[i] ^= 0x01
		if sha256.Sum256(mutated) == base {
			t.Fatalf("digest unchanged after mutating byte %d", i)
		}
	}
}
