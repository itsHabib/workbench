package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/itsHabib/workbench/contracts/automode"
)

func classifyCommand(command string) (normalized, error) {
	if opaqueCommand(command) {
		return normalized{}, errUnsupported
	}
	words, err := splitWords(command)
	if err != nil || len(words) == 0 {
		return normalized{}, errUnsupported
	}
	lower := lowerWords(words)
	out := classifyKnownCommand(command, words, lower)
	if out.parameters == nil {
		out.parameters = automode.Values{}
	}
	out.parameters["command_digest"] = automode.Value{Value: textDigest(command)}
	return out, nil
}

func classifyKnownCommand(command string, words, lower []string) normalized {
	if merge := parseMerge(command, words, lower); merge.operation != "" {
		return merge
	}
	if isForcePush(lower) {
		return normalized{operation: "git.force_push"}
	}
	if hasPrefix(lower, "gh", "repo", "delete") {
		return normalized{operation: "github.repo.delete"}
	}
	if hasPrefix(lower, "gh", "repo", "edit") && contains(lower, "--visibility") {
		return normalized{operation: "github.repo.visibility"}
	}
	if isMint(lower) {
		return normalized{operation: "authority.mint"}
	}
	if isAuthorityMutation(lower) {
		return normalized{operation: "authority.state_mutation"}
	}
	if isTest(lower) {
		return normalized{operation: "test", parameters: automode.Values{"program": {Value: lower[0]}}}
	}
	if isRead(lower) {
		return normalized{operation: "read", parameters: automode.Values{"program": {Value: lower[0]}}}
	}
	return normalized{operation: "unknown"}
}

func opaqueCommand(command string) bool {
	lower := strings.ToLower(command)
	for _, marker := range []string{
		"`", "&", ";", "|", "<", ">", "(", ")", "%", "!", "^",
		"$", "@", "*", "?", "[", "]", "{", "}", "\r", "\n",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func parseMerge(command string, words, lower []string) normalized {
	if !hasPrefix(lower, "gh", "pr", "merge") {
		return normalized{}
	}
	out := normalized{
		operation:  "github.pr.merge",
		candidate:  command,
		admin:      contains(lower, "--admin"),
		parameters: automode.Values{},
	}
	if len(words) < 4 {
		return out
	}
	number, ok := parseNumber(words[3])
	if !ok {
		return out
	}
	out.number = number
	out.repo = flagValue(words, lower, "-r", "--repo")
	out.headSHA = flagValue(words, lower, "--match-head-commit")
	out.parameters = automode.Values{"pr": {Value: words[3]}}
	if !validRepo(out.repo) {
		out.repo = ""
	}
	if out.repo != "" {
		out.parameters["repo"] = automode.Value{Value: out.repo}
	}
	if !fullSHA.MatchString(out.headSHA) {
		out.headSHA = ""
	}
	if out.headSHA != "" {
		out.parameters["head_sha"] = automode.Value{Value: out.headSHA}
	}
	return out
}

func textDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func flagValue(words, lower []string, names ...string) string {
	for i, word := range lower {
		if !matches(word, names) || i+1 >= len(words) {
			continue
		}
		return words[i+1]
	}
	return ""
}

func lowerWords(words []string) []string {
	out := make([]string, len(words))
	for i, word := range words {
		out[i] = strings.ToLower(word)
	}
	return out
}

func isForcePush(words []string) bool {
	if !hasPrefix(words, "git", "push") {
		return false
	}
	return contains(words, "--force") || contains(words, "--force-with-lease") || contains(words, "-f")
}

func isMint(words []string) bool {
	return hasPrefix(words, "gate", "grant") ||
		hasPrefix(words, "custody", "grant") ||
		hasPrefix(words, "custody", "derive") ||
		hasPrefix(words, "custody", "keys")
}

func isAuthorityMutation(words []string) bool {
	if hasPrefix(words, "gate", "gate") || hasPrefix(words, "gate", "judge") {
		return true
	}
	if hasPrefix(words, "custody", "serve") {
		return true
	}
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "rm", "rmdir", "del", "remove-item":
		return containsFragment(words[1:], "gate") && containsFragment(words[1:], "state")
	}
	return false
}

func isTest(words []string) bool {
	if hasPrefix(words, "go", "test") {
		return !hasUnsafeFlag(words[2:], "-exec", "--exec", "-toolexec", "--toolexec")
	}
	if hasPrefix(words, "go", "vet") {
		return !hasUnsafeFlag(words[2:], "-vettool", "--vettool", "-toolexec", "--toolexec", "-fix", "--fix")
	}
	if hasPrefix(words, "golangci-lint", "run") {
		return !hasUnsafeFlag(words[2:], "--fix", "--cpu-profile-path", "--mem-profile-path", "--trace-path") &&
			!hasUnsafePrefix(words[2:], "--output.")
	}
	return false
}

func isRead(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "rg":
		return readOnlyRipgrep(words)
	case "grep", "get-content", "select-string", "type":
		return true
	case "git":
		return readOnlyGit(words)
	case "gh":
		return len(words) > 2 && readOnlyGH(words[1], words[2]) && !hasUnsafeFlag(words[3:], "-w", "--web")
	case "gate":
		return readOnlyGate(words)
	}
	return false
}

func readOnlyGate(words []string) bool {
	if len(words) < 2 || !contains([]string{"next", "explain", "audit"}, words[1]) {
		return false
	}
	if hasUnsafeFlag(words[2:], "-cpuprofile", "--cpuprofile", "-blockprofile", "--blockprofile", "-trace", "--trace") {
		return false
	}
	if words[1] == "explain" && hasUnsafeFlag(words[2:], "-html", "--html", "-out", "--out") {
		return false
	}
	return true
}

func readOnlyGit(words []string) bool {
	if len(words) < 2 {
		return false
	}
	if words[1] == "branch" {
		return len(words) == 2 || (len(words) == 3 && words[2] == "--show-current")
	}
	if !contains([]string{"status", "diff", "log", "show", "rev-parse"}, words[1]) {
		return false
	}
	return !hasUnsafeFlag(words[2:], "--ext-diff", "--textconv", "--output")
}

func readOnlyRipgrep(words []string) bool {
	for _, word := range words[1:] {
		shortZip := strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "--") && strings.Contains(word[1:], "z")
		if shortZip || word == "--search-zip" || word == "--pre" || word == "--hostname-bin" ||
			strings.HasPrefix(word, "--pre=") ||
			strings.HasPrefix(word, "--hostname-bin=") {
			return false
		}
	}
	return true
}

func readOnlyGH(noun, verb string) bool {
	switch noun {
	case "pr":
		return contains([]string{"view", "list", "checks", "diff", "status"}, verb)
	case "run":
		return contains([]string{"view", "list"}, verb)
	case "repo":
		return verb == "view"
	}
	return false
}

func hasPrefix(words []string, prefix ...string) bool {
	if len(words) < len(prefix) {
		return false
	}
	for i, word := range prefix {
		if words[i] != word {
			return false
		}
	}
	return true
}

func contains(words []string, want string) bool {
	for _, word := range words {
		if word == want {
			return true
		}
	}
	return false
}

func containsFragment(words []string, want string) bool {
	for _, word := range words {
		if strings.Contains(word, want) {
			return true
		}
	}
	return false
}

func matches(value string, wants []string) bool {
	for _, want := range wants {
		if value == want {
			return true
		}
	}
	return false
}

func hasUnsafeFlag(words []string, flags ...string) bool {
	for _, word := range words {
		for _, flag := range flags {
			if word == flag || strings.HasPrefix(word, flag+"=") {
				return true
			}
		}
	}
	return false
}

func hasUnsafePrefix(words []string, prefixes ...string) bool {
	for _, word := range words {
		for _, prefix := range prefixes {
			if strings.HasPrefix(word, prefix) {
				return true
			}
		}
	}
	return false
}
