package seat

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSecretsSkipsNoise(t *testing.T) {
	got := parseSecrets(strings.NewReader("# rooms\n\nCLAUDE_CODE_OAUTH_TOKEN=abc\nnot a pair\nX=1=2\n"))
	if !reflect.DeepEqual(got, []string{"CLAUDE_CODE_OAUTH_TOKEN=abc", "X=1=2"}) {
		t.Fatalf("%q", got)
	}
}

func TestEnvironmentDropsHarnessMarkerAndSetsSeat(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("SWARM_SEAT", "old")
	t.Setenv("SWARM_STORE", "resp:h:1/p")
	env, err := environment("p7", "/nonexistent/none")
	if err == nil {
		t.Fatal("missing secrets file accepted")
	}
	env, err = environment("p7", "")
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, kv := range env {
		has[kv] = true
	}
	if has["CLAUDECODE=1"] || has["SWARM_SEAT=old"] || !has["SWARM_SEAT=p7"] || !has["SWARM_STORE=resp:h:1/p"] {
		t.Fatalf("%v", env)
	}
}

func TestFirstLineTruncates(t *testing.T) {
	if got := firstLine("401 Invalid bearer token\nmore"); got != "401 Invalid bearer token" {
		t.Fatal(got)
	}
	if got := firstLine(strings.Repeat("x", 300)); len(got) != 200 {
		t.Fatal(len(got))
	}
}
