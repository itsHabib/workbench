package fleet

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Mail is a message addressed to a ROLE, not to a session. Sessions are disposable;
// a role is the stable address, and whichever session wears the role next reads the
// mailbox at its prompt. Nothing here delivers: the hook injects the unread lines,
// and the watcher launches a session for a role that has none.
//
//	mail/<role-safe>/<id>.json   one message; replaced by ack and by the delivery stamp
//
// The id is caller-chosen and stable, so a retried send is a no-op: the same id with
// the same payload returns the record that exists, and a different payload under the
// same id is refused rather than overwritten.

// MailKinds is every kind a message may carry.
var MailKinds = []string{"question", "answer", "escalation", "report", "order"}

// MailLineCap is how many unread messages the hook names before it counts the rest.
const MailLineCap = 5

// MailDir is the mailbox of one role.
func MailDir(role string) string { return Path("mail", Safe(role)) }

// MailFile is one message's record.
func MailFile(role, id string) string { return filepath.Join(MailDir(role), Safe(id)+".json") }

// MailLockKey is the lock every mutation of a role's mailbox takes.
func MailLockKey(role string) string { return "mail:" + role }

// IsMailKind reports a known kind.
func IsMailKind(kind string) bool { return contains(MailKinds, kind) }

// Mailbox is every message addressed to role, oldest first. Unreadable files are
// skipped: a mailbox is evidence, and a damaged message is not a reason to hide the
// readable ones.
func Mailbox(role string) []Rec {
	var out []Rec
	for _, name := range listDir(MailDir(role)) {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		r := ReadJSON(filepath.Join(MailDir(role), name))
		if r != nil && S(r, "id") != "" {
			out = append(out, r)
		}
	}
	stableSort(out, func(a, b Rec) bool { return F(a, "at") < F(b, "at") })
	return out
}

// MailRoles is every role with a readable mailbox on this machine. The directory
// name is the role made filename-safe, which does not reverse; the role is read from
// the messages themselves.
func MailRoles() []string {
	var out []string
	for _, name := range listDir(Path("mail")) {
		for _, entry := range listDir(Path("mail", name)) {
			if to := S(ReadJSON(Path("mail", name, entry)), "to"); to != "" {
				out = append(out, to)
				break
			}
		}
	}
	return out
}

// Unacked is the messages in a mailbox nobody has acknowledged yet.
func Unacked(role string) []Rec {
	var out []Rec
	for _, m := range Mailbox(role) {
		if S(m, "acked_by") == "" {
			out = append(out, m)
		}
	}
	return out
}

// MailLine is one message as the hook names it.
func MailLine(m Rec) string {
	return fmt.Sprintf("[fleet] mail %s from %s (%s): %s", S(m, "id"), S(m, "from_role"), S(m, "kind"), S(m, "subject"))
}

// MailLines is what a session in role reads at its prompt: one line per unread
// message, capped, then a count of the rest. Reading is not acknowledging: the lines
// repeat every prompt until `fleet ack`.
func MailLines(role string) []string {
	if role == "" {
		return nil
	}
	unacked := Unacked(role)
	var lines []string
	for i, m := range unacked {
		if i == MailLineCap {
			lines = append(lines, fmt.Sprintf("[fleet] and %d more; fleet mail", len(unacked)-MailLineCap))
			break
		}
		lines = append(lines, MailLine(m))
	}
	return lines
}

// ---------- the contact relation ----------
//
// Who may write to whom is derived, never declared here. The hierarchy already
// exists in the org charters under $ORG_STATE: a role's charter names its
// `terms.supervisors`, and a role listed there is its parent. Parent, children and
// siblings follow from that one relation. A charter is one line of JSONL, so the read
// is cheap; the fallback for roles chartered nowhere is contacts.json in the state
// root, `{"<role>": ["<role>", ...]}`, written by the operator and read symmetrically.

// ContactsFile is the operator's declared contact list, an addition to the charters.
func ContactsFile() string { return Path("contacts.json") }

// Parents is the roles a charter names as supervising role, within tenant. The
// last charter or recharter line wins, as the org fold reads it.
func Parents(tenant, role string) []string {
	if tenant == "" || role == "" {
		return nil
	}
	text, ok := readText(filepath.Join(OrgState, tenant, strings.ReplaceAll(role, ":", "--"), "chain.jsonl"))
	if !ok {
		return nil
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		r := ReadJSONBytes([]byte(line))
		if r == nil || S(r, "role") != role {
			continue
		}
		if k := S(r, "kind"); k != "charter" && k != "recharter" {
			continue
		}
		out = nil
		for _, s := range Strs(M(r, "terms"), "supervisors") {
			if strings.HasPrefix(s, "human:") {
				continue
			}
			out = append(out, s)
		}
	}
	return out
}

// CharteredRoles is every role with a chain under tenant.
func CharteredRoles(tenant string) []string {
	var out []string
	for _, name := range listDir(filepath.Join(OrgState, tenant)) {
		if name == "blobs" || !isDir(filepath.Join(OrgState, tenant, name)) {
			continue
		}
		out = append(out, strings.ReplaceAll(name, "--", ":"))
	}
	return out
}

// Contacts is the set of roles that role may write to within tenant: its parents,
// its children, its siblings, and whatever contacts.json adds in either direction.
// Sorted, without role itself.
func Contacts(tenant, role string) []string {
	set := map[string]bool{}
	parents := Parents(tenant, role)
	for _, p := range parents {
		set[p] = true
	}
	for _, other := range CharteredRoles(tenant) {
		if other == role {
			continue
		}
		for _, p := range Parents(tenant, other) {
			if p == role || contains(parents, p) {
				set[other] = true
			}
		}
	}
	declared := readAny(ContactsFile())
	if m, ok := declared.(map[string]any); ok {
		for _, c := range Strs(m, role) {
			set[c] = true
		}
		for other, v := range m {
			if list, ok := v.([]any); ok && contains(anyStrs(list), role) {
				set[other] = true
			}
		}
	}
	delete(set, role)
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func anyStrs(xs []any) []string {
	var out []string
	for _, x := range xs {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
