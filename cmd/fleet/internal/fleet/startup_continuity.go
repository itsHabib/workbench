package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const roleHandoffBodyBytes = 16384
const roleHandoffContextBytes = 1024

// Allow JSON escaping of the entire accepted body plus identity metadata.
const roleHandoffFileBytes = 128 * 1024

// currentStartupAssignment reads and stamps under the same lock as placement.
// The slot name alone cannot identify work after the slot has been reused.
func currentStartupAssignment(slot, sid string) Rec {
	if slot == "" || sid == "" {
		return nil
	}
	var assignment Rec
	err := KeyLock("slot:"+slot, func() error {
		occupant := Lease("slot:" + slot)
		if IsMalformed(occupant) || !B(occupant, "occupancy") || S(occupant, "session") != sid {
			return nil
		}
		rec := SessionRecord(sid)
		launch := S(rec, "launch_dir")
		if launch == "" {
			launch = S(rec, "cwd")
		}
		role, tenant, boundSlot := MapRowsFor(launch)
		if S(rec, "slot") != slot || boundSlot != slot {
			return nil
		}
		path := Path("assign", Safe(slot)+".json")
		a := ReadJSON(path)
		if !assignmentMatchesLaunch(a, rec, launch, slot) {
			return nil
		}
		if notice := assignmentIdentityNotice(a, role, tenant); notice != "" {
			assignment = Rec{"startup_notice": notice}
			return nil
		}
		assignment = a
		if S(a, "delivered_to") == "" {
			a["delivered_to"], a["delivered_at"] = sid, Now()
			if err := WriteJSON(path, a); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return startupAssignmentError(slot, sid, assignment, err)
	}
	return assignment
}

func startupAssignmentError(slot, sid string, assignment Rec, err error) Rec {
	logError(Rec{"session": sid, "slot": slot, "error": "startup assignment error: " + err.Error()})
	if assignment != nil {
		assignment["delivery_notice"] = "[fleet] assignment delivery receipt was not saved; inspect fleet work --json"
		return assignment
	}
	reason := "storage error while reading assignment"
	if errors.Is(err, ErrKeyBusy) {
		reason = "seat lock is busy"
	}
	return Rec{"startup_notice": "[fleet] assignment temporarily unavailable: " + reason + "; inspect fleet work --json"}
}

func assignmentIdentityNotice(a Rec, role, tenant string) string {
	if S(a, "role") == "" || S(a, "tenant") == "" {
		return "[fleet] assignment identity is unknown; ask the lead to reassign when the seat is free"
	}
	if role == "" || tenant == "" || S(a, "role") != role || S(a, "tenant") != tenant {
		return "[fleet] assignment belongs to a different role or tenant; ask the lead to reassign when the seat is free"
	}
	return ""
}

func assignmentMatchesLaunch(a, rec Rec, launch, slot string) bool {
	if a == nil || S(a, "slot") != slot || S(a, "path") == "" || canonPath(S(a, "path")) != canonPath(launch) {
		return false
	}
	branch, repo := BranchOf(launch), RepoID(launch)
	return branch != "" && repo != "" && S(a, "branch") == branch && S(a, "repo") == repo &&
		S(rec, "branch") == branch && S(rec, "repo") == repo
}

func roleHandoffIdentity(rec Rec) (string, string, string) {
	launch := S(rec, "launch_dir")
	if launch == "" {
		launch = S(rec, "cwd")
	}
	return MapRowsFor(launch)
}

func roleHandoffPath(tenant, role string) string {
	return Path("role-handoff", sha1hex(tenant+"\x00"+role)+".json")
}

// RoleHandoffValidationError distinguishes expected policy or usage refusals
// from persistence failures. Usage is true only for invalid handoff text.
type RoleHandoffValidationError struct {
	Usage   bool
	Message string
}

func (e *RoleHandoffValidationError) Error() string { return e.Message }

// WriteRoleHandoff stores an authored conclusion for the caller's launch-bound
// role. It neither assigns work nor claims that the conclusion was verified.
func WriteRoleHandoff(rec Rec, conclusion, next string) error {
	role, tenant, slot := roleHandoffIdentity(rec)
	if slot != "" {
		return &RoleHandoffValidationError{Message: "fleet handoff: pooled seats use branch handoffs; run fleet handoff <branch> \"<conclusion>\""}
	}
	if role == "" || tenant == "" || S(rec, "session") == "" {
		return &RoleHandoffValidationError{Message: "fleet handoff: no identified role in the session's launch directory; use fleet role to bind it"}
	}
	conclusion, next = strings.TrimSpace(conclusion), strings.TrimSpace(next)
	if conclusion == "" || len(conclusion)+len(next) > roleHandoffBodyBytes || !utf8.ValidString(conclusion+next) {
		return &RoleHandoffValidationError{Usage: true, Message: fmt.Sprintf("fleet handoff: provide a nonempty UTF-8 conclusion with at most %d bytes including next steps", roleHandoffBodyBytes)}
	}
	return WriteJSON(roleHandoffPath(tenant, role), Rec{"tenant": tenant, "role": role,
		"session": S(rec, "session"), "conclusion": conclusion, "next": next, "at": Now()})
}

// ReadRoleHandoff returns the complete checkpoint for an already resolved
// identity, without resolving a mutable role map again during the read.
// Missing context is nil; unreadable or mismatched context is an error.
// Authored context is advisory, never evidence of completion or authority.
func ReadRoleHandoff(tenant, role string) (Rec, error) {
	if role == "" || tenant == "" {
		return nil, nil
	}
	path := roleHandoffPath(tenant, role)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read role handoff: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > roleHandoffFileBytes {
		return nil, fmt.Errorf("role handoff must be a regular file of at most %d bytes", roleHandoffFileBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read role handoff: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, roleHandoffFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read role handoff: %w", err)
	}
	var r Rec
	if len(data) > roleHandoffFileBytes || !utf8.Valid(data) || json.Unmarshal(data, &r) != nil || r == nil || !validHandoffSurrogates(data) {
		return nil, fmt.Errorf("role handoff is oversized or invalid JSON")
	}
	if !validRoleHandoff(r, tenant, role) {
		return nil, fmt.Errorf("role handoff has invalid identity, provenance or body")
	}
	return r, nil
}

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD. On otherwise
// valid JSON, reject those escapes instead of silently changing authored text.
func validHandoffSurrogates(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if data[i] != 'u' {
			continue // includes a literal escaped backslash, not a Unicode escape
		}
		value, _ := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		i += 4
		if value < 0xd800 || value > 0xdfff {
			continue
		}
		if value > 0xdbff || i+6 >= len(data) || string(data[i+1:i+3]) != `\u` {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func validRoleHandoff(r Rec, tenant, role string) bool {
	conclusion, cok := r["conclusion"].(string)
	next, nok := r["next"].(string)
	return S(r, "tenant") == tenant && S(r, "role") == role && S(r, "session") != "" && F(r, "at") > 0 &&
		cok && nok && strings.TrimSpace(conclusion) != "" && len(conclusion)+len(next) <= roleHandoffBodyBytes
}

// RoleHandoffLine supplies the short startup hint. Inspect exposes read errors
// and the full record; startup retains its optional, bounded context behavior.
func RoleHandoffLine(rec Rec) string {
	role, tenant, slot := roleHandoffIdentity(rec)
	if slot != "" {
		return ""
	}
	r, _ := ReadRoleHandoff(tenant, role)
	return RoleHandoffSummary(r)
}

// RoleHandoffSummary formats a previously read checkpoint for existing displays.
func RoleHandoffSummary(r Rec) string {
	if r == nil {
		return ""
	}
	text := strings.Join(strings.Fields(S(r, "conclusion")), " ")
	if next := strings.Join(strings.Fields(S(r, "next")), " "); next != "" {
		text += " · next: " + next
	}
	line := fmt.Sprintf("[fleet] authored role handoff for %s/%s (%s ago, session %s; advisory): %q", S(r, "tenant"), S(r, "role"), FmtAge(Now()-F(r, "at")), Short(S(r, "session")), text)
	return continuityExcerpt(line, roleHandoffContextBytes)
}

func continuityExcerpt(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit - len("…")
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + "…"
}
