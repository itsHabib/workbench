package verbs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

var (
	roleBlockRe  = regexp.MustCompile(`(?ms)^# BEGIN fleet role\n.*?^# END fleet role\n?`)
	denyPrefixRe = regexp.MustCompile(`\ABash\(([A-Za-z0-9_.-]+(?: [A-Za-z0-9_.-]+)*):\*\)\z`)
	// A TOML key may be bare or quoted; `"developer_instructions" = "keep me"` is the same key.
	devInstrTopRe  = regexp.MustCompile(`(?m)^\s*(?:developer_instructions|"developer_instructions"|'developer_instructions')\s*=`)
	tomlTableRe    = regexp.MustCompile(`(?m)^\s*\[`)
	whitespaceRe   = regexp.MustCompile(`\s`)
	fleetHookMark  = "fleet governance"
	manifestKeys   = []string{"kind", "card", "denies", "requires", "produces", "slots"}
	localArtifacts = []string{"/CLAUDE.local.md", "/.claude/settings.local.json", "/.codex/config.toml", "/.codex/rules/fleet-role.rules"}
)

// strictJSON is a hand-maintained JSON file as an object, or a refusal naming it.
// Not ReadJSON: that returns nil for a malformed file, and a settings write would
// then replace a hand-maintained file with only our denies and report success.
func strictJSON(p string) (map[string]any, error) {
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, refuse("fleet role: %s is invalid JSON; next action: repair it, then rerun `fleet role`", p)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, refuse("fleet role: %s is invalid JSON; next action: repair it, then rerun `fleet role`", p)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, refuse("fleet role: %s is not a JSON object; next action: repair it, then rerun `fleet role`", p)
	}
	return m, nil
}

// codexConfig is (target, text) for .codex/config.toml with the fleet role block.
//
// The Python parsed the TOML to check `developer_instructions` was not already set.
// Without a TOML parser the check is a line-anchored regex over the top-level
// section (before the first table header), which is exactly the scope the Python's
// top-level key check had. Invalid TOML is not detected here; that is the port's one
// known approximation.
func codexConfig(checkout, role, card string) (string, string, error) {
	target := filepath.Join(checkout, ".codex", "config.toml")
	old := ""
	if b, err := os.ReadFile(target); err == nil {
		old = string(b)
	}
	old = roleBlockRe.ReplaceAllString(old, "")
	top := old
	if loc := tomlTableRe.FindStringIndex(old); loc != nil {
		top = old[:loc[0]]
	}
	if devInstrTopRe.MatchString(top) {
		return "", "", refuse("fleet role: %s already sets developer_instructions; next action: reconcile it, then rerun `fleet role`", target)
	}
	cardText, _ := os.ReadFile(card)
	roleText := fmt.Sprintf("# Session role: %s\n\n%s\n", role, strings.TrimSpace(string(cardText)))
	enc, _ := json.Marshal(roleText)
	block := fmt.Sprintf("# BEGIN fleet role\ndeveloper_instructions = %s\n# END fleet role\n", enc)
	if old != "" {
		block += "\n" + strings.TrimLeft(old, "\n")
	}
	return target, block, nil
}

func withoutFleetHandlers(groups any, target, event string) ([]any, error) {
	list, ok := groups.([]any)
	if groups != nil && !ok {
		return nil, refuse("fleet role: %s hooks.%s is not a list; next action: repair it, then rerun `fleet role`", target, event)
	}
	var kept []any
	for _, g := range list {
		group, ok := g.(map[string]any)
		var handlers []any
		if ok {
			handlers, _ = group["hooks"].([]any)
		}
		if !ok || handlers == nil {
			return nil, refuse("fleet role: %s has an invalid %s group; next action: repair it, then rerun `fleet role`", target, event)
		}
		var remaining []any
		for _, h := range handlers {
			hm, ok := h.(map[string]any)
			if !ok || fleet.S(hm, "statusMessage") != fleetHookMark {
				remaining = append(remaining, h)
			}
		}
		if len(remaining) > 0 {
			cp := map[string]any{}
			for k, v := range group {
				cp[k] = v
			}
			cp["hooks"] = remaining
			kept = append(kept, cp)
		}
	}
	return kept, nil
}

// codexHooks is (target, data) for $CODEX_HOME/hooks.json with the six fleet hooks
// pointing at this binary's codex face.
func codexHooks(command string) (string, map[string]any, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = "~/.codex"
	}
	if strings.HasPrefix(home, "~") {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, home[1:])
		}
	}
	home, _ = filepath.Abs(home)
	target := filepath.Join(home, "hooks.json")
	data, err := strictJSON(target)
	if err != nil {
		return "", nil, err
	}
	hooks, ok := data["hooks"].(map[string]any)
	if data["hooks"] != nil && !ok {
		return "", nil, refuse("fleet role: %s hooks is not an object; next action: repair it, then rerun `fleet role`", target)
	}
	if hooks == nil {
		hooks = map[string]any{}
		data["hooks"] = hooks
	}
	specs := [][2]string{{"SessionStart", ""}, {"UserPromptSubmit", ""}, {"PreToolUse", "^(Bash|Edit|Write)$"}, {"PostToolUse", "^Bash$"}, {"Stop", ""}, {"SessionEnd", ""}}
	for _, spec := range specs {
		event, matcher := spec[0], spec[1]
		groups, err := withoutFleetHandlers(hooks[event], target, event)
		if err != nil {
			return "", nil, err
		}
		group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "statusMessage": fleetHookMark}}}
		if matcher != "" {
			group["matcher"] = matcher
		}
		hooks[event] = append(groups, group)
	}
	return target, data, nil
}

// codexRules is the Codex projection of a lane's denies: every `Bash(<words>:*)`
// deny becomes an execpolicy prefix rule. Denies with a wildcard mid-pattern have no
// prefix form and stay Claude-only.
func codexRules(denies []string) string {
	var blocks []string
	for _, d := range denies {
		m := denyPrefixRe.FindStringSubmatch(d)
		if m == nil {
			continue
		}
		// Python-style separators (`["gh", "pr", "merge"]`): the rules file is read by
		// people and grepped by the suite, and json.Marshal would drop the spaces.
		pattern := fleet.DumpJSON(strings.Fields(m[1]))
		reason := fleet.DumpJSON(fmt.Sprintf("Denied by this lane's manifest (%s). Next action: hand it up the tree.", d))
		blocks = append(blocks, fmt.Sprintf("prefix_rule(\n    pattern = %s,\n    decision = \"forbidden\",\n    justification = %s,\n)", pattern, reason))
	}
	if len(blocks) == 0 {
		return ""
	}
	return strings.Join(blocks, "\n\n") + "\n"
}

func mapPath(p string) string { return fleet.LongPath(p) }

// excludeLocalArtifacts keeps the per-checkout files `fleet role` writes out of the
// repo's history via `.git/info/exclude` — the local, untracked ignore list, in the
// common dir so every worktree gets it from one write.
func excludeLocalArtifacts(checkout string) string {
	_, common := fleet.GitDirs(checkout)
	if common == "" {
		return ""
	}
	p := filepath.Join(common, "info", "exclude")
	current := ""
	if b, err := os.ReadFile(p); err == nil {
		current = string(b)
	}
	have := strings.Fields(current)
	var missing []string
	for _, a := range localArtifacts {
		if !contains(have, a) {
			missing = append(missing, a)
		}
	}
	if len(missing) == 0 {
		return p
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return ""
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return ""
	}
	defer f.Close()
	if current != "" && !strings.HasSuffix(current, "\n") {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString("# written by `fleet role`: per-checkout role binding, never committed\n")
	for _, a := range missing {
		_, _ = f.WriteString(a + "\n")
	}
	return p
}

// fleetKinds is the lane kinds this tool owns — one per manifest under lanes/.
func fleetKinds() map[string]bool {
	out := map[string]bool{}
	d := fleet.LanesDir()
	ents, _ := os.ReadDir(d)
	for _, e := range ents {
		if st, err := os.Stat(filepath.Join(d, e.Name(), "manifest.json")); err == nil && !st.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// loadManifest is the six-key manifest for a lane kind, or a refusal naming the path
// it looked in. The substrate reads exactly these keys and nothing else about a lane.
func loadManifest(kind string) (map[string]any, string, error) {
	p := fleet.LongPath(filepath.Join(fleet.LanesDir(), kind, "manifest.json"))
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		kinds := strings.Join(sortedKeys(fleetKinds()), ", ")
		if kinds == "" {
			kinds = "none found"
		}
		return nil, "", refuse("fleet role: no manifest at %s (kinds: %s)", p, kinds)
	}
	m, err := strictJSON(p)
	if err != nil {
		return nil, "", err
	}
	var missing []string
	for _, k := range manifestKeys {
		if _, ok := m[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 || fleet.S(m, "kind") != kind {
		miss := "nothing"
		if len(missing) > 0 {
			miss = "['" + strings.Join(missing, "', '") + "']"
		}
		return nil, "", refuse("fleet role: %s is not a lane manifest: missing %s, kind=%s; next action: give it the six keys ('kind', 'card', 'denies', 'requires', 'produces', 'slots') with kind=%s", p, miss, pyReprOrNone(m["kind"]), fleet.PyRepr(kind))
	}
	if bad := manifestShapeErrors(m); len(bad) > 0 {
		return nil, "", refuse("fleet role: %s has the six keys but wrong shapes: %s; next action: fix the manifest", p, strings.Join(bad, "; "))
	}
	card := fleet.LongPath(filepath.Join(filepath.Dir(p), fleet.S(m, "card")))
	if st, err := os.Stat(card); err != nil || st.IsDir() {
		return nil, "", refuse("fleet role: %s names card %s but %s does not exist", p, fleet.PyRepr(fleet.S(m, "card")), card)
	}
	return m, card, nil
}

// manifestShapeErrors is every wrong-typed value in a manifest, described.
func manifestShapeErrors(m map[string]any) []string {
	strs := func(v any) bool {
		list, ok := v.([]any)
		if !ok {
			return false
		}
		for _, x := range list {
			if s, ok := x.(string); !ok || s == "" {
				return false
			}
		}
		return true
	}
	var errs []string
	if s, ok := m["card"].(string); !ok || s == "" {
		errs = append(errs, "card must be a non-empty string")
	}
	if !strs(m["denies"]) {
		errs = append(errs, "denies must be a list of strings")
	}
	if !strs(m["requires"]) {
		errs = append(errs, "requires must be a list of strings")
	}
	if p := m["produces"]; p != nil {
		if _, ok := p.(string); !ok {
			errs = append(errs, "produces must be a string or null")
		}
	}
	if f, ok := m["slots"].(float64); !ok || f < 0 || f != float64(int64(f)) {
		errs = append(errs, "slots must be a non-negative integer")
	}
	if c := m["cadence"]; c != nil && fleet.ParseDuration(c) == 0 {
		errs = append(errs, "cadence must be a duration like 45m, 2h or 900 (seconds), or absent")
	}
	if w := m["watch"]; w != nil {
		if _, ok := w.(bool); !ok {
			errs = append(errs, "watch must be true or false, or absent")
		}
	}
	return errs
}

// resolveTenant is the tenant column for a new or rewritten roles.map line. Never a
// literal default: the operator's --tenant; the tenant the path already inherits;
// $ORG_TENANT; then refuse and name the flag.
func resolveTenant(checkout string, same []fleet.MapRow, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if len(same) > 0 {
		return same[0].Tenant, nil
	}
	if t := fleet.TenantOf(checkout); t != "" {
		return t, nil
	}
	if t := os.Getenv("ORG_TENANT"); t != "" {
		return t, nil
	}
	return "", refuse("fleet role: no tenant for %s: no roles.map line above it to inherit from and ORG_TENANT is unset. There is no default tenant. Next action: fleet role %s <role> --tenant <tenant>", checkout, checkout)
}

// hookCommand is what the Codex hooks.json runs: this binary's codex face.
func hookCommand() string {
	exe, err := os.Executable()
	if err != nil {
		exe = "fleet"
	}
	if strings.ContainsAny(exe, " \t") {
		return fmt.Sprintf("%q hook codex", exe)
	}
	return exe + " hook codex"
}

// cmdRole roles one checkout. `slot` is the map's optional fourth column and is
// written only by `fleet pool`; a hand-roled checkout has none, and an existing slot
// column survives a re-role.
func cmdRole(checkout, role string, force bool, tenant, slot string) error {
	checkout = mapPath(checkout)
	if !isDir(checkout) {
		return refuse("fleet role: %s is not a directory", checkout)
	}
	if whitespaceRe.MatchString(checkout) || (slot != "" && whitespaceRe.MatchString(slot)) {
		return refuse("fleet role: %s contains whitespace, which roles.map cannot encode; move or symlink the checkout", checkout)
	}
	kind := strings.SplitN(role, ":", 2)[0]
	manifest, card, err := loadManifest(kind)
	if err != nil {
		return err
	}
	cfgTarget, cfgText, err := codexConfig(checkout, role, card)
	if err != nil {
		return err
	}
	hooksTarget, hooksData, err := codexHooks(hookCommand())
	if err != nil {
		return err
	}
	rulesTarget := filepath.Join(checkout, ".codex", "rules", "fleet-role.rules")
	settingsTarget := filepath.Join(checkout, ".claude", "settings.local.json")
	existing, err := strictJSON(settingsTarget)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(fleet.OrgState, 0o755); err != nil {
		return err
	}
	mapfile := fleet.RolesMap()
	var out error
	// One writer at a time, and a reader never sees a truncated file.
	lerr := fleet.KeyLock("map:roles", func() error {
		out = roleUnderLock(checkout, role, force, tenant, slot, kind, manifest, card, cfgTarget, cfgText, hooksTarget, hooksData, rulesTarget, settingsTarget, existing, mapfile)
		return nil
	})
	if lerr != nil {
		return lerr
	}
	return out
}

func roleUnderLock(checkout, role string, force bool, tenant, slot, kind string, manifest map[string]any, card, cfgTarget, cfgText, hooksTarget string, hooksData map[string]any, rulesTarget, settingsTarget string, existing map[string]any, mapfile string) error {
	lines, rows := fleet.MapRows(mapfile)
	var same []fleet.MapRow
	for _, r := range rows {
		if fleet.NormCase(mapPath(r.Path)) == fleet.NormCase(checkout) {
			same = append(same, r)
		}
	}
	tenant, err := resolveTenant(checkout, same, tenant)
	if err != nil {
		return err
	}
	// A path bound to a role with no card is still a role in the same tree, just one
	// this tool has no card for. Rebinding it silently takes an explicit --force.
	if len(same) > 0 && !force {
		other := strings.SplitN(same[0].Role, ":", 2)[0]
		if !fleetKinds()[other] {
			return refuse("fleet role: %s is already %s (tenant %s), and there is no manifest for `%s` under %s.\n  Refusing to rebind it silently. Either add lanes/%s/manifest.json so this role is one we can describe,\n  or pass --force to rebind; the line becomes `%s %s %s`.",
				checkout, same[0].Role, same[0].Tenant, other, fleet.LanesDir(), other, checkout, tenant, role)
		}
	}
	if slot == "" && len(same) > 0 {
		slot = same[0].Slot
	}
	line := fmt.Sprintf("%s %s %s", checkout, tenant, role)
	if slot != "" {
		line += " " + slot
	}
	if len(same) > 0 {
		replaced := lines[same[0].Index]
		lines[same[0].Index] = line
		if strings.Fields(replaced)[2] != role {
			say("fleet role: replaced `%s`", replaced)
		}
	} else {
		lines = append(lines, line)
	}
	// Never CRLF: org's boot hook parses this with `read -r`, and a trailing \r rides
	// along on the role.
	tmp := fmt.Sprintf("%s.%d.tmp", mapfile, os.Getpid())
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, mapfile); err != nil {
		return err
	}
	local := fmt.Sprintf("# Session role: %s\n\nThis checkout is one lane of the fleet. The role card below is the whole\nof what is specific to it; everything else is enforced by ~/.fleet hooks.\n\n@%s\n", role, card)
	if err := os.WriteFile(filepath.Join(checkout, "CLAUDE.local.md"), []byte(local), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(checkout, ".claude"), 0o755); err != nil {
		return err
	}
	perms, _ := existing["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
		existing["permissions"] = perms
	}
	denySet := map[string]bool{}
	for _, d := range fleet.Strs(perms, "deny") {
		denySet[d] = true
	}
	for _, d := range fleet.Strs(manifest, "denies") {
		denySet[d] = true
	}
	deny := sortedKeys(denySet)
	denyAny := make([]any, len(deny))
	for i, d := range deny {
		denyAny[i] = d
	}
	perms["deny"] = denyAny
	// indent=2: a file a human maintains by hand; do not collapse it.
	sb, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settingsTarget, append(sb, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(rulesTarget), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(cfgTarget, []byte(cfgText), 0o644); err != nil {
		return err
	}
	if err := fleet.WriteJSON(hooksTarget, hooksData); err != nil {
		return err
	}
	rules := codexRules(fleet.Strs(manifest, "denies"))
	if err := os.WriteFile(rulesTarget, []byte(rules), 0o644); err != nil {
		return err
	}
	note := " WARNING: no git dir found, so the generated files are NOT excluded - check before committing."
	if excluded := excludeLocalArtifacts(checkout); excluded != "" {
		note = " All generated files excluded via " + excluded + "."
	}
	say("%s is now %s (manifest %s): Claude card + %d denies; Codex developer card + user hooks + %d exact command rules. Open a NEW Codex tab there, trust the project configuration, then trust the hook definitions.%s",
		checkout, role, fleet.LongPath(filepath.Join(fleet.LanesDir(), kind, "manifest.json")), len(deny), strings.Count(rules, "prefix_rule("), note)
	return nil
}
