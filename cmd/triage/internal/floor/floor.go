// Package floor computes the deterministic risk floor for a PR from its diff.
//
// This is the reproducible safety layer of triage: given the same diff it
// always returns the same floor. It implements only the cleanly-deterministic
// signals from RUBRIC.md (path globs, content regexes, manifest/lockfile
// detection). The genuinely-semantic signals — public-API renames,
// access-widening, concurrency-correctness — are left to the agent advisory
// pass, which may only escalate above this floor. See docs/features/pr-risk-engine/spec.md §6.
package floor

import (
	"regexp"
	"strings"
)

// Tier is a risk level. Higher = more human review required.
type Tier int

// The tiers, least to most human review; the floor never rounds down.
const (
	T0 Tier = iota // auto — no human
	T1             // peer
	T2             // owner
	T3             // critical
)

func (t Tier) String() string {
	switch t {
	case T3:
		return "T3"
	case T2:
		return "T2"
	case T1:
		return "T1"
	default:
		return "T0"
	}
}

// Signal is one fired rule contributing to the floor.
type Signal struct {
	Name  string `json:"signal"`
	Tier  Tier   `json:"-"`
	TierS string `json:"tier"`
	Why   string `json:"why"`
}

// LineOp classifies a line in the ordered diff stream.
type LineOp byte

// The diff-line operations a unified diff can carry.
const (
	OpCtx  LineOp = ' ' // unchanged context line
	OpAdd  LineOp = '+' // added line
	OpDel  LineOp = '-' // removed line
	OpHunk LineOp = '@' // hunk boundary — section state is unknown past it
)

// DiffLine is one line of a file's diff in original order, context included.
type DiffLine struct {
	Op   LineOp
	Body string
}

// FileChange is one file's contribution to a diff.
type FileChange struct {
	Path    string
	OldPath string   // pre-rename / a-side path; classified alongside Path (union)
	New     bool     // file is created by this diff (new file mode AND --- /dev/null)
	Added   []string // added line bodies (without the leading '+')
	Removed []string // removed line bodies (without the leading '-')
	Lines   []DiffLine
}

// paths returns the distinct paths a file's signals should be checked against —
// both the new path and any old/pre-rename path, so a rename to a benign name
// can't shed the signals its original path carried.
func (f FileChange) paths() []string {
	if f.OldPath == "" || f.OldPath == f.Path {
		return []string{f.Path}
	}
	return []string{f.Path, f.OldPath}
}

// matchAny reports whether re matches any of the file's paths.
func (f FileChange) matchAny(re *regexp.Regexp) bool {
	for _, p := range f.paths() {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// Diff is a parsed unified diff.
type Diff struct {
	Files []FileChange
}

// Result is the floor classification.
type Result struct {
	Floor   Tier     `json:"-"`
	FloorS  string   `json:"floor"`
	Signals []Signal `json:"signals"`
	Files   int      `json:"files"`
	Added   int      `json:"added"`
	Removed int      `json:"removed"`
}

// pathRule maps a path pattern to a floor tier.
type pathRule struct {
	re   *regexp.Regexp
	tier Tier
	name string
	why  string
}

// mustRule compiles a case-insensitive path rule.
func mustRule(pat string, tier Tier, name, why string) pathRule {
	return pathRule{regexp.MustCompile("(?i)" + pat), tier, name, why}
}

// Ordered high→low so the first match per file also carries the best "why".
// Floor is the max over all fired signals regardless of order.
var pathRules = []pathRule{
	// §5.4 control-plane — highest priority, exempt from docs→T0
	mustRule(`(^|/)RUBRIC\.md$|/skills/pr-risk/|(^|/)labels/|mismatches\.jsonl$|(^|/)CODEOWNERS$`, T3, "control-plane", "edits the classifier or its evidence"),
	// §5.1 T3 surfaces (migrations are graded by content — see classifyMigration)
	mustRule(`(^|/)(auth|authz|authn|session|oauth|jwt|crypto|secret|secrets|token|credential|password)([/._-]|$)`, T3, "auth-crypto-secrets", "auth/crypto/secret surface"),
	mustRule(`(^|/)(billing|payment|payments|invoice|ledger|payout|charge|stripe)([/._-]|$)`, T3, "money", "money/billing/ledger path"),
	// §5.3 registry/source override (path-based; content-based handled separately)
	mustRule(`(^|/)\.npmrc$|(^|/)go\.work$`, T3, "dep-override", "registry/source override file"),
	// §5.5 policy-as-data T3
	mustRule(`\.rego$|(^|/)(rbac|rolebinding|clusterrole|role_binding)([/._-]|$)|(^|/)policy/.*\.(json|ya?ml)$`, T3, "policy-as-data", "access-control policy data"),
	// §5.1 T2 surfaces. Real infra/deploy config only — a source file that merely NAMES an
	// isolation tool (firecracker.rs, jailer.rs) is caught by the isolation CONTENT signal when
	// it actually touches a boundary; a proptest added to firecracker.rs is not infra config.
	mustRule(`(^|/)\.github/workflows/|(^|/)Dockerfile|\.tf$|\.tfvars$|\.service$|(^|/)ci/`, T2, "infra-config", "infra/CI/deploy config"),
	// public API is only cleanly deterministic for declared contract files; exported-symbol
	// renames are semantic → left to the agent advisory pass (spec §6), not a path glob.
	mustRule(`\.proto$|openapi|swagger`, T2, "api-contract", "declared API/wire contract file"),
	// §5.5 policy-as-data T2
	mustRule(`(^|/)\.env|(^|/)cors|allowed_origins`, T2, "config-security", "behavior-controlling security config"),
	// docs / tests / generated → T0 (checked before generic-code default; NOT applied to control-plane, handled above)
	mustRule(`\.pb\.go$|_generated\.|\.gen\.|(^|/)generated/|\.snap$`, T0, "generated", "generated code"),
	mustRule(`_test\.go$|\.test\.[jt]s$|(^|/)tests?/|_spec\.|\.spec\.[jt]s$|proptest`, T0, "tests-only", "test code"),
	mustRule(`\.md$|(^|/)docs?/|(^|/)README|(^|/)LICENSE|\.txt$`, T0, "docs", "docs/copy"),
}

// §5.1 DB migrations, graded by statement (see classifyMigration). Grading is
// per-STATEMENT, not per-line: a destructive statement appended after a ';' on
// an otherwise-additive line, or hidden behind a block comment, must still be
// seen (adversarial: `CREATE TABLE x; DROP TABLE y;`).
var (
	reMigration = regexp.MustCompile(`(?i)\.sql$|(^|/)migrations?/|_migrate|schema_migrations`)
	// any SQL statement keyword, anywhere it can start a statement (line start or after
	// a ';'). Used to detect a statement the additive allowlist doesn't recognize.
	reSQLKeyword = regexp.MustCompile(`(?i)^(CREATE|ALTER|DROP|UPDATE|DELETE|INSERT|TRUNCATE|RENAME|REINDEX|VACUUM|WITH|SELECT|PRAGMA|GRANT|REVOKE|COPY|MERGE|CALL|LOCK|REPLACE|COMMENT)\b`)
	// purely-additive statements: new schema objects and index swaps, touching no existing rows
	reSQLAdditive = regexp.MustCompile(`(?i)^(CREATE\s+(UNIQUE\s+)?INDEX|CREATE\s+TABLE|ALTER\s+TABLE\s+\S+\s+ADD\s+COLUMN|DROP\s+INDEX|SET)\b`)
	// transaction wrappers — neither additive nor destructive
	reSQLNeutral = regexp.MustCompile(`(?i)^(BEGIN|COMMIT|END|START\s+TRANSACTION)\b`)
	// a volatile ADD COLUMN: a generated column or a DEFAULT that calls a function
	// rewrites every existing row under an exclusive lock — not "touches no rows".
	// `\(?` allows a parenthesized default: DEFAULT (gen_random_uuid()).
	reSQLVolatileAdd = regexp.MustCompile(`(?i)ADD\s+COLUMN.*(GENERATED\s+ALWAYS|DEFAULT\s+\(?[A-Za-z_][A-Za-z0-9_]*\s*\()`)
	// CREATE TABLE ... AS SELECT/WITH copies existing rows — a backfill, not additive.
	reSQLCTAS = regexp.MustCompile(`(?i)^CREATE\s+TABLE\s+\S+\s+AS\s+(SELECT|WITH)\b`)
	// a destructive sub-action in a multi-action ALTER TABLE. The additive prefix
	// (ADD COLUMN) matches reSQLAdditive first, so a comma-joined DROP/RENAME must be
	// caught within the statement (adversarial: `ADD COLUMN x, DROP COLUMN y`).
	reSQLDestructiveAction = regexp.MustCompile(`(?i)\bDROP\s+(COLUMN|CONSTRAINT)\b|\bRENAME\s+(COLUMN|CONSTRAINT|TO)\b`)
)

// dependency manifests / lockfiles (§5.3). Runtime vs dev decided by hunk content.
var (
	reManifest = regexp.MustCompile(`(?i)(^|/)(package\.json|Cargo\.toml|go\.mod|pyproject\.toml|mix\.exs|build\.gradle|pom\.xml)$`)
	reLockfile = regexp.MustCompile(`(?i)(^|/)(package-lock\.json|pnpm-lock\.yaml|yarn\.lock|Cargo\.lock|go\.sum|poetry\.lock|Gemfile\.lock)$`)
	reOverride = regexp.MustCompile(`(?i)(^|/)\.npmrc$|\[patch|"resolutions"|"overrides"|(^|/)go\.work$`)
	// a dependency whose SOURCE is a git repo or local path (RUBRIC §5.3 calls these
	// T3 overrides; the path-based reOverride missed them). Scoped to a dependency
	// value: TOML `git =`/`path =` in an inline (`{`/`,`) OR table-form dep (line
	// start, e.g. `[dependencies.foo]` then `git = …`), or a JSON/registry git/file
	// URL value. Each ln is a single line, so `^` = line start.
	reSourceDep = regexp.MustCompile(`(?i)(?:[{,]|^)\s*(git|path)\s*=\s*"|"\s*:\s*"(git\+|github:|gitlab:|file:|https?://[^"]*\.git)`)
	// a manifest section header, TOML table or JSON object key — trailing comments/content
	// allowed so `[dependencies] # keep sorted` still resets the section (adversarial).
	reSectionHeader = regexp.MustCompile(`^\[[^\]]+\]\]?|^"[A-Za-z_-]+"\s*:\s*\{`)
	// dev/test section markers (§5.3 fix from Experiment 01)
	reDevSection = regexp.MustCompile(`(?i)dev-dependencies|devDependencies|\[dev-|test-dependencies`)
	// a test-framework crate — matched only at the dependency-NAME position (line start),
	// so a runtime dep whose git URL merely contains "quickcheck" isn't laundered to dev.
	reTestDepName = regexp.MustCompile(`(?i)^"?(proptest|jest|vitest|pytest|testify|mockito|rspec|criterion|quickcheck)"?\s*[=:]`)
	reTestDep     = regexp.MustCompile(`(?i)\b(proptest|jest|vitest|pytest|testify|mockito|rspec|criterion|quickcheck)\b`)
	// a lockfile line altering an EXISTING package's provenance (removed resolved/
	// integrity/checksum/registry/source). Clean dep-adds are purely additive; a
	// provenance rewrite is a runtime-affecting change and never inherits dev.
	reLockProvenance = regexp.MustCompile(`(?i)^"?(resolved|integrity|checksum|registry|source|url)"?\s*[:=]`)
	// a lockfile provenance line naming a source URL/host. A dev-dep's legit
	// transitive additions all resolve to a canonical public registry; a package
	// resolved off-registry is a source override however it's added (adversarial:
	// cursor add-only provenance bypass).
	reLockSourceURL     = regexp.MustCompile(`(?i)^"?(resolved|source|registry|url)"?\s*[:=]\s*"?(\S+)`)
	reCanonicalRegistry = regexp.MustCompile(`(?i)^(registry\+)?https?://(registry\.npmjs\.org|registry\.yarnpkg\.com|static\.crates\.io|index\.crates\.io|crates\.io|github\.com/rust-lang/crates\.io-index|files\.pythonhosted\.org|pypi\.org|proxy\.golang\.org|sum\.golang\.org|rubygems\.org|jsr\.io)[/"]?`)
)

// §5.2 content signals — path-independent, on removed/added lines.
var (
	reRemovedControl = regexp.MustCompile(`(?i)\b(authorize|authenticate|authz|check_?permission|has_?permission|require_?(auth|login|role)|verify_|rate.?limit|ratelimit|acl|is_?admin|ensure_)\b`)
	reLoosenGuard    = regexp.MustCompile(`(?i)(==\s*)|&&`) // heuristic; paired with removal context by caller
	reUnbounded      = regexp.MustCompile(`(?i)\bselect\s+\*|limit\s+none|no.?limit|for\s*\{\s*\}|while\s*true`)
	// secret/credential handling in code, regardless of path (Experiment 01: dossier#64
	// handled AWS creds in s3store.rs — no "auth" in the path, floor missed the T3).
	reAddedSecret = regexp.MustCompile(`(?i)(secret_?access_?key|access_?key_?id|private_?key|client_?secret|aws_secret|StaticCredentials|SharedCredentials)`)
	// isolation / privilege / network-boundary commands in scripts (Experiment 01: rooms#42/44
	// put iptables + jailer privilege-drop in .sh scripts with no telltale path).
	// `--cap-drop` etc. live OUTSIDE the \b(...)\b group: a leading `\b` can't match before `-`
	// (no word char precedes it), so a flag at line start would never fire inside the group.
	reAddedIsolation = regexp.MustCompile(`(?i)\b(iptables|nftables|nft\s|ip\s+route|ip\s+link|setuid|setgid|chroot|unshare|seccomp|CAP_[A-Z]|jailer)\b|--cap-drop|--privileged|--security-opt`)
	// §5.2 concurrency/locking primitives (added code) → T2. Scoped to lock/atomic constructs,
	// NOT bare async/await (ubiquitous, not itself a risk). RUBRIC §5.2 promised this; the floor lacked it.
	reConcurrency = regexp.MustCompile(`(?i)\b(Mutex|RwLock|RWMutex|Semaphore|WaitGroup|CondVar|Condvar)\b|\.lock\(\)|\bAtomic[A-Z]\w*|\batomic\.(Add|Load|Store|Swap|CompareAndSwap)`)
)

// A content signal describes PRODUCTION risk, so it must not fire on lines that can't carry it:
// a comment ("/// Secret access key ...") is prose, and a whole test/doc file is a fixture or a
// mention, not the risk itself. reContentSkipFile matches files whose content is never a signal.
var reContentSkipFile = regexp.MustCompile(`(?i)_test\.go$|\.test\.[jt]s$|(^|/)tests?/|_spec\.|\.spec\.[jt]s$|proptest|\.md$|(^|/)docs?/|(^|/)README|\.txt$`)

// isCodePath reports whether a path is source code (so it gets the T1 default).
var reCodePath = regexp.MustCompile(`(?i)\.(go|rs|ts|tsx|js|jsx|py|rb|java|kt|ex|exs|c|cc|cpp|h|hpp|sh|sql)$`)

// isComment reports whether a diff line opens a comment, so a content signal shouldn't fire on it.
// Conservative — only unambiguous comment openers, so real code is never mistaken for a comment.
func isComment(line string) bool {
	t := strings.TrimSpace(line)
	switch {
	case t == "":
		return false
	case strings.HasPrefix(t, "//"), strings.HasPrefix(t, "/*"), strings.HasPrefix(t, "<!--"):
		return true
	case strings.HasPrefix(t, "*"):
		// block-comment continuation ("* text") or close ("*/") — NOT a pointer deref like
		// `*task = secret_key` or `*p++`, which are real code and must reach the content signals.
		return t == "*" || t[1] == ' ' || t[1] == '/'
	case strings.HasPrefix(t, "#"):
		// shell/python/yaml/toml comment — with or without a trailing space (#TODO is a comment) —
		// but NOT a Rust attribute (#[..]) or a shebang (#!). ("--" is deliberately NOT a comment
		// marker here: it would eat CLI flags like `--cap-drop=ALL`, killing the isolation signal.)
		return !strings.HasPrefix(t, "#[") && !strings.HasPrefix(t, "#!")
	}
	return false
}

// Classify computes the deterministic floor for a parsed diff.
// line-of-sight pass; decomposing it is owed to triage's own iteration with
// the labels corpus pinning behavior — not to a relocation diff.
//
//nolint:gocognit,cyclop // the classifier core carries the whole rubric in one
func Classify(d Diff) Result {
	var sigs []Signal
	floor := T0
	add := func(name string, tier Tier, why string) {
		sigs = append(sigs, Signal{Name: name, Tier: tier, TierS: tier.String(), Why: why})
		if tier > floor {
			floor = tier
		}
	}

	var added, removed int
	sawCode := false

	// §5.3 pre-pass: a lockfile change caused by a dev-only manifest change is itself
	// dev — the lockfile alone can't say (no dev marking in lock formats), but the
	// manifests in the same diff can. Lockfile-only diffs stay runtime, fail-closed.
	manifestSeen, manifestsAllDev := false, true
	for _, f := range d.Files {
		if !f.matchAny(reManifest) {
			continue
		}
		manifestSeen = true
		if !manifestIsDev(f) {
			manifestsAllDev = false
		}
	}
	lockInheritsDev := manifestSeen && manifestsAllDev

	for _, f := range d.Files {
		added += len(f.Added)
		removed += len(f.Removed)

		// path rules — record every match; control-plane wins by being T3. Sensitive
		// rules (T2+) match against the old path too, so a rename to a benign name
		// can't shed them; the T0 docs/tests/generated rules match the new path only
		// (a rename FROM docs into code must not stay T0).
		matchedPath := false
		for _, r := range pathRules {
			hit := r.re.MatchString(f.Path)
			if r.tier >= T2 {
				hit = f.matchAny(r.re)
			}
			if hit {
				add(r.name, r.tier, r.why+": "+f.Path)
				matchedPath = true
			}
		}

		// §5.1 DB migrations — graded by content, not blanket T3. Docs/tests in a
		// migrations dir (a README) are not migrations. Matched on either path.
		if f.matchAny(reMigration) && !reContentSkipFile.MatchString(f.Path) {
			name, tier, why := classifyMigration(f)
			add(name, tier, why+": "+f.Path)
			matchedPath = true
		}

		// §5.3 dependency signals
		isManifest, isLockfile := f.matchAny(reManifest), f.matchAny(reLockfile)
		if isManifest || isLockfile {
			dev := manifestIsDev(f)
			// a lockfile inherits its sibling manifests' dev classification only when it
			// purely adds packages; rewriting an existing package's provenance can repoint
			// a runtime dep, so it never inherits (adversarial: evil-registry swap).
			if isLockfile && !lockfileEditsProvenance(f) {
				dev = dev || lockInheritsDev
			}
			if f.matchAny(reOverride) {
				add("dep-override", T3, "registry/source override: "+f.Path)
			} else if dev {
				add("dep-dev", T1, "dev/test dependency change: "+f.Path)
			} else {
				add("dep-runtime", T2, "runtime dependency change: "+f.Path)
			}
			matchedPath = true
		}
		if isManifest || isLockfile {
			for _, ln := range f.Added {
				if reOverride.MatchString(ln) || reSourceDep.MatchString(ln) || offRegistrySource(ln) {
					add("dep-override", T3, "source/registry override in "+f.Path)
					break
				}
			}
		}

		// §5.2 content signals fire only on production lines — not whole test/doc files, not comments.
		contentBearing := !reContentSkipFile.MatchString(f.Path)

		for _, ln := range f.Removed {
			if !contentBearing || isComment(ln) {
				continue
			}
			if reRemovedControl.MatchString(ln) {
				add("removed-control", T3, "removes an authz/validation/rate-limit line in "+f.Path)
				break
			}
		}
		var sawUnbounded, sawSecret, sawIsolation, sawConcurrency bool
		for _, ln := range f.Added {
			if !contentBearing || isComment(ln) {
				continue
			}
			if !sawUnbounded && reUnbounded.MatchString(ln) {
				add("unbounded-resource", T2, "introduces an unbounded query/loop in "+f.Path)
				sawUnbounded = true
			}
			if !sawSecret && reAddedSecret.MatchString(ln) {
				add("secret-handling", T3, "handles secret/credential material in "+f.Path)
				sawSecret = true
			}
			if !sawIsolation && reAddedIsolation.MatchString(ln) {
				add("isolation-boundary", T2, "changes an isolation/privilege/network boundary in "+f.Path)
				sawIsolation = true
			}
			if !sawConcurrency && reConcurrency.MatchString(ln) {
				add("concurrency", T2, "changes concurrency/locking in "+f.Path)
				sawConcurrency = true
			}
		}

		// default: an unmatched code file with real (non-comment) changes is an internal change → T1
		// (§5.1). A code file touched only in comments/blank lines is docs-grade → stays T0.
		if reCodePath.MatchString(f.Path) {
			sawCode = true
			if !matchedPath && hasCodeChange(f) {
				add("internal-change", T1, "code change: "+f.Path)
			}
		} else if !matchedPath {
			// §5.5 unknown path, no signal → T1 (never auto-T0)
			add("unknown-path", T1, "unclassified path: "+f.Path)
		}
	}
	_ = sawCode
	_ = reLoosenGuard

	return Result{
		Floor: floor, FloorS: floor.String(), Signals: sigs,
		Files: len(d.Files), Added: added, Removed: removed,
	}
}

// hasCodeChange reports whether a code file has a non-comment, non-blank added or removed line.
// A change that only touches comments or blank lines in a code file is docs-grade, not behavior.
func hasCodeChange(f FileChange) bool {
	for _, ln := range append(append([]string{}, f.Added...), f.Removed...) {
		if strings.TrimSpace(ln) != "" && !isComment(ln) {
			return true
		}
	}
	return false
}

// section is which kind of manifest section a line sits in.
type section int

const (
	secUnknown section = iota // no header seen yet (e.g. go.mod, or past a hunk boundary)
	secDev                    // a dev/test dependency section
	secRuntime                // a runtime/build section
)

// manifestIsDev reports whether a manifest change is confined to dev/test deps.
// It walks the ordered diff stream tracking the section each changed line sits in
// (a dep added INTO an existing [dev-dependencies] section shows the header only
// as context). Fail-closed everywhere: at a hunk boundary the section is unknown;
// a changed line in a runtime section, or a changed line that itself OPENS a
// non-dev section (a compact JSON `"dependencies": {...}` or a newly-added
// `[dependencies]` table), makes the whole change runtime.
//
//nolint:gocognit,cyclop // see Classify — same deferral.
func manifestIsDev(f FileChange) bool {
	if len(f.Lines) == 0 {
		return legacyDevScan(f)
	}
	sec, sawChange, inString := secUnknown, false, false
	for _, ln := range f.Lines {
		if ln.Op == OpHunk {
			// reset BOTH section and string state — the elided lines between hunks
			// could open or close a string, so carrying inString across a boundary
			// would skip a runtime dep in the next hunk (adversarial review of PR #4:
			// a string opened after a dev change laundered a runtime dep to T1).
			sec, inString = secUnknown, false
			continue
		}
		// a line that opens/closes a TOML/Python multi-line string toggles string
		// state; a `[section]`-shaped line INSIDE a string is content, not a header
		// (adversarial: `[dev-dependencies]` buried in a docstring flips the section).
		// A comment can't open a multi-line string, so an odd `"""` in a comment
		// body is comment text, not a delimiter. Use the manifest-local comment
		// test — in a manifest a `#[…]` line IS a comment (no Rust attributes), which
		// the polyglot isComment excludes (adversarial re-review of PR #4).
		if !isManifestComment(ln.Body) {
			if n := strings.Count(ln.Body, `"""`) + strings.Count(ln.Body, "'''"); n%2 == 1 {
				inString = !inString
				continue
			}
		}
		if inString {
			continue
		}
		t := strings.TrimSpace(ln.Body)
		// a bare `}`/`},` closes a JSON object section (devDependencies); without this
		// a following top-level field (`"type": "module"`) would inherit secDev and a
		// runtime field change would launder to T1 (adversarial: JSON brace tracking).
		if sec != secUnknown && (t == "}" || t == "},") {
			sec = secUnknown
			continue
		}
		if reSectionHeader.MatchString(t) {
			dev := reDevSection.MatchString(t)
			sec = secRuntime
			if dev {
				sec = secDev
			}
			// a CHANGED line that opens a non-dev section is itself introducing
			// runtime deps (compact-JSON block, freshly-added [dependencies]).
			if ln.Op != OpCtx && !dev {
				return false
			}
			continue
		}
		if ln.Op == OpCtx || t == "" || isManifestComment(ln.Body) {
			continue
		}
		switch sec {
		case secDev:
			sawChange = true
		case secRuntime:
			return false
		default: // secUnknown — dev only if the dep NAME is a test framework (go.mod has no sections)
			if !reTestDepName.MatchString(t) {
				return false
			}
			sawChange = true
		}
	}
	return sawChange
}

// isManifestComment reports whether a line is a comment in a dependency manifest.
// Manifests have no Rust attributes, so any `#`-led line is a comment (unlike the
// polyglot isComment, which excludes `#[…]`); `//` covers go.mod-style comments.
func isManifestComment(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//")
}

// legacyDevScan is the marker-only fallback for a FileChange built without the
// ordered line stream (hand-assembled diffs): dev iff a dev marker appears among
// the changed lines themselves.
func legacyDevScan(f FileChange) bool {
	for _, ln := range append(append([]string{}, f.Added...), f.Removed...) {
		if reDevSection.MatchString(ln) || reTestDep.MatchString(ln) {
			return true
		}
	}
	return false
}

// offRegistrySource reports whether a lockfile provenance line names a source URL
// that is NOT a canonical public registry — a source override however it is added
// (a rewrite is caught by lockfileEditsProvenance; an add-only malicious package
// block is caught here). A dev-dep's legit transitive additions all resolve to a
// canonical registry, so this doesn't over-call on ordinary lock churn.
func offRegistrySource(line string) bool {
	m := reLockSourceURL.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return false
	}
	url := strings.Trim(m[2], `",`)
	// only URL-shaped sources are graded; a bare crate version / git-rev isn't a host.
	if !strings.Contains(url, "://") {
		return false
	}
	return !reCanonicalRegistry.MatchString(url)
}

// lockfileEditsProvenance reports whether a lockfile change rewrites an existing
// package's provenance (a removed resolved/integrity/checksum/registry/source
// line) rather than purely adding new packages. Such a change never inherits a
// sibling manifest's dev classification — it can repoint a runtime dep.
func lockfileEditsProvenance(f FileChange) bool {
	for _, ln := range f.Removed {
		if reLockProvenance.MatchString(strings.TrimSpace(ln)) {
			return true
		}
	}
	return false
}

// classifyMigration grades one migration file. A NEW migration whose every
// statement is purely additive (CREATE TABLE / CREATE INDEX / ADD COLUMN / index
// swap, no volatile column rewrite) floors at T2 — it touches no existing rows,
// and T2 still routes to an owner. Anything else — a destructive or unrecognized
// statement, a volatile ADD COLUMN, no additive statement at all, or an edit to
// an already-applied migration file — keeps T3. Grading is per-statement, so a
// destructive statement hidden after a ';' or behind a block comment is still seen.
func classifyMigration(f FileChange) (name string, tier Tier, why string) {
	if !f.New {
		return "db-migration", T3, "edits an existing migration"
	}
	additive := false
	for _, stmt := range migrationStatements(f.Added) {
		switch {
		case stmt == "" || reSQLNeutral.MatchString(stmt):
			continue
		case reSQLVolatileAdd.MatchString(stmt):
			return "db-migration", T3, "ADD COLUMN rewrites every row (generated/volatile default)"
		case reSQLCTAS.MatchString(stmt):
			return "db-migration", T3, "CREATE TABLE AS copies existing rows (backfill)"
		case reSQLAdditive.MatchString(stmt):
			// a multi-action ALTER TABLE whose additive prefix matched but which also
			// carries a comma-joined DROP/RENAME is destructive overall.
			if reSQLDestructiveAction.MatchString(stmt) {
				return "db-migration", T3, "ALTER TABLE mixes additive and destructive actions"
			}
			additive = true
		case reSQLKeyword.MatchString(stmt):
			return "db-migration", T3, "destructive/backfill migration statement"
		default:
			// a non-empty fragment that is neither neutral, additive, nor a
			// recognized keyword — an unknown statement shape. Fail closed.
			return "db-migration", T3, "unrecognized migration statement"
		}
	}
	if !additive {
		return "db-migration", T3, "migration with no recognized additive statement"
	}
	return "db-migration-additive", T2, "purely additive schema migration"
}

// unterminatedComment is a sentinel statement: a `/*` opened outside a string
// and never closed is malformed SQL, so classifyMigration fails it closed to T3
// rather than silently dropping everything after the opener.
const unterminatedComment = "\x00unterminated-block-comment"

// migrationStatements splits added migration lines into normalized statements,
// with a scanner that tracks SQL single-quote strings so comment and statement
// syntax INSIDE a string literal is inert. A `--` or `/*` inside `'...'` no
// longer destroys a `;` statement boundary (adversarial review of PR #4:
// `DEFAULT 'note -- v2');` on the closing line hid a following DROP). `;`
// splits, `--` line comments, and `/* */` block comments are honored only
// outside strings; `”` is an in-string escaped quote.
//
//nolint:gocognit,cyclop // see Classify — same deferral.
func migrationStatements(added []string) []string {
	s := strings.Join(added, "\n")
	var out []string
	var cur strings.Builder
	flush := func() {
		if t := strings.TrimSpace(collapseSpace(cur.String())); t != "" {
			out = append(out, t)
		}
		cur.Reset()
	}
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '$' && dollarTag(s[i:]) != "": // Postgres dollar-quoting $tag$...$tag$
			tag := dollarTag(s[i:])
			cur.WriteString(tag)
			i += len(tag)
			if end := strings.Index(s[i:], tag); end >= 0 { // copy body + closing tag
				cur.WriteString(s[i : i+end+len(tag)])
				i += end + len(tag)
			} else { // unterminated dollar-quote — fail closed
				out = append(out, unterminatedComment)
				flush()
				return out
			}
		case c == '\'': // single-quoted string; honor '' and (in E'') backslash escapes
			// backslash-escapes apply only to a STANDALONE E prefix — an identifier
			// ending in e/E (sequence'...') is a plain string where `\` is literal
			// (adversarial re-review of PR #4).
			escapes := i > 0 && (s[i-1] == 'E' || s[i-1] == 'e') &&
				(i < 2 || (!isAlphaNum(s[i-2]) && s[i-2] != '_'))
			cur.WriteByte(c)
			i++
			for i < len(s) {
				if escapes && s[i] == '\\' && i+1 < len(s) { // E-string: \x is a 2-char literal
					cur.WriteByte(s[i])
					cur.WriteByte(s[i+1])
					i += 2
					continue
				}
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' { // '' escaped quote, still inside
						cur.WriteString("''")
						i += 2
						continue
					}
					cur.WriteByte('\'')
					i++
					break
				}
				cur.WriteByte(s[i])
				i++
			}
		case c == '-' && i+1 < len(s) && s[i+1] == '-': // line comment to EOL
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*': // block comment to closing */
			end := strings.Index(s[i+2:], "*/")
			if end < 0 { // unterminated — fail closed, stop scanning
				out = append(out, unterminatedComment)
				flush()
				return out
			}
			cur.WriteByte(' ')
			i += 2 + end + 2
		case c == ';':
			flush()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

// dollarTag returns the leading Postgres dollar-quote tag ($$ or $name$) at the
// start of s, or "" if s doesn't open one. The tag delimits a string in which
// ' and ; are inert, so a DEFAULT $$value's info$$ can't swallow a following
// statement (adversarial re-review of PR #4).
func dollarTag(s string) string {
	if len(s) < 2 || s[0] != '$' {
		return ""
	}
	for j := 1; j < len(s); j++ {
		if s[j] == '$' {
			return s[:j+1]
		}
		if s[j] != '_' && !isAlphaNum(s[j]) {
			return ""
		}
	}
	return ""
}

func isAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// collapseSpace turns every run of whitespace (including newlines from a
// multi-line statement) into a single space so the anchored SQL regexes match.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
