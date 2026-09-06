package main

// FR1: the substrate contains no lane name and no domain word, comments included.
//
// The reference suite enforces this with a grep over the Python sources. Those files
// are now shims that exec this binary, so the grep would pass vacuously; this test is
// the same check over the Go sources. The one exemption is the command token `gh pr`:
// the GitHub CLI's own verb, which the substrate parses the way it parses `git`, is
// shell surface rather than domain vocabulary. Only that token span is exempted, so a
// domain word elsewhere on a line that also says `gh pr` is still caught.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	fr1Words = regexp.MustCompile(`(?i)\b(pr|ci|review|hypermill|nx|finisher|author|liverun|supervisor|infra)\b`)
	// The token as a shell string (`gh pr`, or the regex literals `gh\s+pr`), and as Go
	// argv (`"gh", "pr"`): one exemption, two spellings of the same command.
	fr1GhToken = regexp.MustCompile(`gh(\\s\+|\\s\*|[[:space:]]+|",[[:space:]]*")pr([^A-Za-z0-9_]|$)`)
)

func TestFR1NoDomainWordsInTheSubstrate(t *testing.T) {
	var hits []string
	err := filepath.Walk(".", func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			scrubbed := fr1GhToken.ReplaceAllString(line, "gh_pull$2")
			if fr1Words.MatchString(scrubbed) {
				hits = append(hits, p+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) > 0 {
		t.Fatalf("FR1: domain words in the substrate:\n  %s", strings.Join(hits, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
