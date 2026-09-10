package fleet

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const roleHandoffBodyBytes = 16384
const roleHandoffContextBytes = 1024

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

// RoleHandoffLine supplies bounded authored context across branches and sessions.
// Identity in the record is checked as well as in its filename.
func RoleHandoffLine(rec Rec) string {
	role, tenant, slot := roleHandoffIdentity(rec)
	if role == "" || tenant == "" || slot != "" {
		return ""
	}
	r := ReadJSON(roleHandoffPath(tenant, role))
	if S(r, "tenant") != tenant || S(r, "role") != role || S(r, "conclusion") == "" {
		return ""
	}
	text := strings.Join(strings.Fields(S(r, "conclusion")), " ")
	if next := strings.Join(strings.Fields(S(r, "next")), " "); next != "" {
		text += " · next: " + next
	}
	line := fmt.Sprintf("[fleet] authored role handoff for %s/%s (%s ago, session %s; advisory): %q", tenant, role, FmtAge(Now()-F(r, "at")), Short(S(r, "session")), text)
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
