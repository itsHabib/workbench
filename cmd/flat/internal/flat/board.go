package flat

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Result is what a builder writes when it claims to be done. head_sha is the
// commit the builder stood on before writing this file; the commit that
// holds the file must be the branch tip, and the tip must differ from
// head_sha by this file only. Everything else in the file is a claim.
type Result struct {
	HeadSHA string   `json:"head_sha"`
	Claims  []string `json:"claims,omitempty"`
	Tests   []string `json:"tests,omitempty"`
	Demo    string   `json:"demo,omitempty"`
	Blocked string   `json:"blocked,omitempty"`
}

// Row is one branch as the board sees it.
type Row struct {
	Branch     string    `json:"branch"`
	Tip        string    `json:"tip"`
	TipAt      time.Time `json:"tip_at"`
	AgeSeconds int64     `json:"age_s"`
	Files      []string  `json:"files"`
	State      string    `json:"state"` // working | silent | landed | blocked | pin_violation | pin_invalid
	ResultPath string    `json:"result_path,omitempty"`
	HeadSHA    string    `json:"head_sha,omitempty"`
	Extra      []string  `json:"extra,omitempty"` // files changed after the pinned head, beyond RESULT.json
	Unpushed   bool      `json:"unpushed,omitempty"`
	Overlaps   []Overlap `json:"overlaps,omitempty"`
	Result     *Result   `json:"result,omitempty"`
}

// Overlap is a file this row shares with another live branch.
type Overlap struct {
	With     string   `json:"with"`
	Files    []string `json:"files"`
	Ruled    bool     `json:"ruled"`
	Decision string   `json:"decision,omitempty"`
}

// Contention is one file touched by two or more branches.
type Contention struct {
	File     string   `json:"file"`
	Branches []string `json:"branches"`
	Ruled    bool     `json:"ruled"`
	Decision string   `json:"decision,omitempty"`
}

// Board is the whole fleet, derived from git and the ledger. Nobody reports
// into it.
type Board struct {
	At         time.Time      `json:"at"`
	Base       string         `json:"base"`
	BaseSHA    string         `json:"base_sha"`
	Rows       []Row          `json:"rows"`
	Contended  []Contention   `json:"contended"`
	Requests   RequestSummary `json:"requests"`
	Resources  []Resource     `json:"resources,omitempty"`
	FetchError string         `json:"fetch_error,omitempty"`
}

// BoardOptions tune the derivation.
type BoardOptions struct {
	Base  string        // base branch; default main
	Fetch bool          // git fetch --prune origin first
	Idle  time.Duration // a working branch whose tip is older than this is silent
}

func (o *BoardOptions) defaults() {
	if o.Base == "" {
		o.Base = "main"
	}
	if o.Idle == 0 {
		o.Idle = 20 * time.Minute
	}
}

// tipRef is a branch's local and remote tips.
type tipRef struct {
	local, remote     string
	localAt, remoteAt int64
}

// Board derives the fleet state from the repository at s.Repo.
func (s *State) Board(opts BoardOptions) (*Board, error) {
	opts.defaults()
	b := &Board{At: Now(), Base: opts.Base}
	if opts.Fetch {
		if _, err := Git(s.Repo, "fetch", "--prune", "--quiet", "origin"); err != nil {
			b.FetchError = err.Error()
		}
	}
	baseSHA, err := Git(s.Repo, "rev-parse", opts.Base)
	if err != nil {
		return nil, fmt.Errorf("base branch %s: %w", opts.Base, err)
	}
	b.BaseSHA = baseSHA
	refs, err := s.collectRefs()
	if err != nil {
		return nil, err
	}
	delete(refs, opts.Base)
	effective, err := s.Effective()
	if err != nil {
		return nil, err
	}
	names, tips, rows := pickTips(s.Repo, refs)
	byFile := map[string][]string{}
	for _, name := range names {
		row := rows[name]
		row.AgeSeconds = int64(b.At.Sub(row.TipAt).Seconds())
		fork := s.forkPoint(name, row.Tip, baseSHA, tips)
		if row.Tip == fork {
			continue // nothing of its own on this branch yet
		}
		row.Files, _ = gitLines(s.Repo, "diff", "--name-only", fork, row.Tip)
		s.classify(row, opts)
		for _, f := range row.Files {
			if !isResultFile(f) {
				byFile[f] = append(byFile[f], name)
			}
		}
		b.Rows = append(b.Rows, *row)
	}
	b.Contended = contentions(byFile, effective)
	for i := range b.Rows {
		b.Rows[i].Overlaps = overlapsFor(b.Rows[i].Branch, b.Contended)
	}
	b.Requests, _ = s.summarizeRequests()
	b.Resources, _ = s.Resources()
	return b, nil
}

// collectRefs reads every local and origin branch tip.
func (s *State) collectRefs() (map[string]*tipRef, error) {
	refs := map[string]*tipRef{}
	lines, err := gitLines(s.Repo, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(committerdate:unix)", "refs/heads", "refs/remotes/origin")
	if err != nil {
		return nil, err
	}
	get := func(name string) *tipRef {
		r := refs[name]
		if r == nil {
			r = &tipRef{}
			refs[name] = r
		}
		return r
	}
	for _, line := range lines {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		at, _ := strconv.ParseInt(parts[2], 10, 64)
		if name, ok := strings.CutPrefix(parts[0], "refs/heads/"); ok {
			r := get(name)
			r.local, r.localAt = parts[1], at
			continue
		}
		if name, ok := strings.CutPrefix(parts[0], "refs/remotes/origin/"); ok && name != "HEAD" {
			r := get(name)
			r.remote, r.remoteAt = parts[1], at
		}
	}
	return refs, nil
}

// pickTips chooses each branch's tip: the local one when it contains the
// remote, else the remote. Unpushed marks a local tip the remote lacks.
func pickTips(repo string, refs map[string]*tipRef) ([]string, map[string]string, map[string]*Row) {
	names := make([]string, 0, len(refs))
	for n := range refs {
		names = append(names, n)
	}
	sort.Strings(names)
	tips := map[string]string{}
	rows := map[string]*Row{}
	for _, name := range names {
		r := refs[name]
		tip, at := r.local, r.localAt
		if tip == "" || (r.remote != "" && !isAncestor(repo, r.remote, r.local)) {
			tip, at = r.remote, r.remoteAt
		}
		tips[name] = tip
		rows[name] = &Row{Branch: name, Tip: tip, TipAt: time.Unix(at, 0).UTC(), Unpushed: r.local != "" && r.local != r.remote}
	}
	return names, tips, rows
}

// contentions lists every file two or more branches changed, with the
// effective ruling that covers it if any.
func contentions(byFile map[string][]string, effective []Decision) []Contention {
	files := make([]string, 0, len(byFile))
	for f, bs := range byFile {
		if len(bs) > 1 {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	var out []Contention
	for _, f := range files {
		c := Contention{File: f, Branches: byFile[f]}
		if d := latestCovering(effective, f); d != nil {
			c.Ruled, c.Decision = true, d.ID
		}
		out = append(out, c)
	}
	return out
}

// forkPoint is where a branch's own work begins: the deepest merge-base
// with the base branch or with any other branch's tip. A branch that
// rebased onto a peer, as a ruling may tell it to, carries the peer's
// commits; those are inherited, not its own changes, and must not count
// as contention or as its landing.
func (s *State) forkPoint(name, tip, baseSHA string, tips map[string]string) string {
	best := baseSHA
	if mb, err := Git(s.Repo, "merge-base", baseSHA, tip); err == nil {
		best = mb
	}
	for other, otherTip := range tips {
		if other == name || otherTip == "" || otherTip == tip {
			continue
		}
		mb, err := Git(s.Repo, "merge-base", otherTip, tip)
		if err != nil || mb == best || mb == tip {
			continue
		}
		if isAncestor(s.Repo, best, mb) {
			best = mb
		}
	}
	return best
}

func (s *State) classify(row *Row, opts BoardOptions) {
	row.State = "working"
	own := "briefs/out/" + row.Branch + "/RESULT.json"
	for _, f := range row.Files {
		if f == own || (isResultFile(f) && strings.Contains(f, "/"+row.Branch+"/")) {
			row.ResultPath = f
			break
		}
	}
	if row.ResultPath == "" {
		if Now().Sub(row.TipAt) > opts.Idle {
			row.State = "silent"
		}
		return
	}
	raw, err := Git(s.Repo, "show", row.Tip+":"+row.ResultPath)
	if err != nil {
		row.State = "pin_invalid"
		return
	}
	var res Result
	if err := json.Unmarshal([]byte(raw), &res); err != nil || res.HeadSHA == "" {
		row.State = "pin_invalid"
		return
	}
	row.Result = &res
	row.HeadSHA = res.HeadSHA
	if _, err := Git(s.Repo, "cat-file", "-e", res.HeadSHA+"^{commit}"); err != nil || !isAncestor(s.Repo, res.HeadSHA, row.Tip) {
		row.State = "pin_invalid"
		return
	}
	changed, _ := gitLines(s.Repo, "diff", "--name-only", res.HeadSHA, row.Tip)
	for _, f := range changed {
		if f != row.ResultPath {
			row.Extra = append(row.Extra, f)
		}
	}
	switch {
	case len(row.Extra) > 0:
		row.State = "pin_violation"
	case res.Blocked != "":
		row.State = "blocked"
	default:
		row.State = "landed"
	}
}

func isAncestor(dir, ancestor, descendant string) bool {
	_, err := Git(dir, "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

func isResultFile(f string) bool {
	return path.Base(f) == "RESULT.json"
}

func overlapsFor(branch string, cs []Contention) []Overlap {
	byWith := map[string]*Overlap{}
	var order []string
	for _, c := range cs {
		mine := false
		for _, b := range c.Branches {
			if b == branch {
				mine = true
			}
		}
		if !mine {
			continue
		}
		for _, other := range c.Branches {
			if other == branch {
				continue
			}
			o := byWith[other]
			if o == nil {
				o = &Overlap{With: other, Ruled: true}
				byWith[other] = o
				order = append(order, other)
			}
			o.Files = append(o.Files, c.File)
			if !c.Ruled {
				o.Ruled = false
			} else if o.Decision == "" {
				o.Decision = c.Decision
			}
		}
	}
	out := make([]Overlap, 0, len(order))
	for _, w := range order {
		out = append(out, *byWith[w])
	}
	return out
}

// JSONL renders one row per line.
func (b *Board) JSONL() string {
	var sb strings.Builder
	for _, r := range b.Rows {
		data, _ := json.Marshal(r)
		sb.Write(data)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Markdown renders the operator's table.
func (b *Board) Markdown() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Board · %s · base %s @ %s\n\n", b.At.Format("2006-01-02 15:04 MST"), b.Base, short(b.BaseSHA))
	fmt.Fprintf(&sb, "| branch | state | tip age | files | overlaps | note |\n|---|---|---|---|---|---|\n")
	for _, r := range b.Rows {
		var ov []string
		for _, o := range r.Overlaps {
			mark := "ruled"
			if !o.Ruled {
				mark = "UNRULED"
			}
			ov = append(ov, fmt.Sprintf("%s (%s: %s)", o.With, mark, strings.Join(o.Files, ", ")))
		}
		note := ""
		switch r.State {
		case "pin_violation":
			note = "changed after RESULT: " + strings.Join(r.Extra, ", ")
		case "blocked":
			note = "blocked on " + r.Result.Blocked
		}
		if r.Unpushed {
			note = strings.TrimSpace(note + " unpushed")
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %d | %s | %s |\n", r.Branch, r.State, ageString(r.AgeSeconds), len(r.Files), strings.Join(ov, "; "), note)
	}
	if len(b.Contended) > 0 {
		sb.WriteString("\n## Contended files\n\n| file | branches | ruling |\n|---|---|---|\n")
		for _, c := range b.Contended {
			rul := "none"
			if c.Ruled {
				rul = c.Decision
			}
			fmt.Fprintf(&sb, "| %s | %s | %s |\n", c.File, strings.Join(c.Branches, ", "), rul)
		}
	}
	fmt.Fprintf(&sb, "\nRequests: %d open (%s); oldest open %s.\n", b.Requests.Open, b.Requests.byNeeds(), ageString(b.Requests.OldestOpenSeconds))
	if len(b.Resources) > 0 {
		sb.WriteString("Resources: ")
		var rs []string
		for _, r := range b.Resources {
			rs = append(rs, fmt.Sprintf("%s held by %s until %s", r.Name, r.Holder, r.Until.Format("15:04")))
		}
		sb.WriteString(strings.Join(rs, "; ") + "\n")
	}
	if b.FetchError != "" {
		fmt.Fprintf(&sb, "\nfetch failed: %s\n", b.FetchError)
	}
	return sb.String()
}

// Phone renders the smallest read: one short line per branch, then one for
// requests. Fits a phone notification.
func (b *Board) Phone() string {
	letter := map[string]string{"working": "W", "silent": "S", "landed": "L", "blocked": "B", "pin_violation": "X", "pin_invalid": "?"}
	var sb strings.Builder
	for _, r := range b.Rows {
		flag := " "
		for _, o := range r.Overlaps {
			if !o.Ruled {
				flag = "!"
			}
		}
		fmt.Fprintf(&sb, "%s%s %-18s %s\n", letter[r.State], flag, trunc(r.Branch, 18), ageString(r.AgeSeconds))
	}
	fmt.Fprintf(&sb, "req %d open, %d for operator\n", b.Requests.Open, b.Requests.Needs["operator"])
	return sb.String()
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func ageString(secs int64) string {
	if secs < 0 {
		secs = 0
	}
	d := time.Duration(secs) * time.Second
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", secs)
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
