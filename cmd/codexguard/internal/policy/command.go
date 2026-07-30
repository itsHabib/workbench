package policy

import (
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
	if merge := parseMerge(command, words, lower); merge.operation != "" {
		return merge, nil
	}
	if isForcePush(lower) {
		return normalized{operation: "git.force_push", parameters: []automode.NamedValue{}}, nil
	}
	if hasPrefix(lower, "gh", "repo", "delete") {
		return normalized{operation: "github.repo.delete", parameters: []automode.NamedValue{}}, nil
	}
	if hasPrefix(lower, "gh", "repo", "edit") && contains(lower, "--visibility") {
		return normalized{operation: "github.repo.visibility", parameters: []automode.NamedValue{}}, nil
	}
	if isMint(lower) {
		return normalized{operation: "authority.mint", parameters: []automode.NamedValue{}}, nil
	}
	if isAuthorityMutation(lower) {
		return normalized{operation: "authority.state_mutation", parameters: []automode.NamedValue{}}, nil
	}
	if isTest(lower) {
		return normalized{operation: "test", parameters: []automode.NamedValue{{Name: "program", Value: lower[0]}}}, nil
	}
	if isRead(lower) {
		return normalized{operation: "read", parameters: []automode.NamedValue{{Name: "program", Value: lower[0]}}}, nil
	}
	return normalized{operation: "unknown", parameters: []automode.NamedValue{}}, nil
}

func opaqueCommand(command string) bool {
	lower := strings.ToLower(command)
	for _, marker := range []string{"$(", "`", "&&", "||", ";", "|", "\r", "\n"} {
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
		parameters: []automode.NamedValue{},
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
	out.parameters = []automode.NamedValue{
		{Name: "head_sha", Value: out.headSHA},
		{Name: "pr", Value: words[3]},
		{Name: "repo", Value: out.repo},
	}
	return out
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
	return hasPrefix(words, "go", "test") ||
		hasPrefix(words, "go", "vet") ||
		hasPrefix(words, "golangci-lint", "run")
}

func isRead(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "rg", "grep", "get-content", "select-string", "type":
		return true
	case "git":
		return len(words) > 1 && contains([]string{"status", "diff", "log", "show", "rev-parse", "branch"}, words[1])
	case "gh":
		return len(words) > 2 && readOnlyGH(words[1], words[2])
	case "gate":
		return len(words) > 1 && contains([]string{"next", "explain", "audit"}, words[1])
	}
	return false
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
