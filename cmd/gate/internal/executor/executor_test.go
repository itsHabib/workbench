package executor

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSessionKeepsTokenInsideExactCommandBoundary(t *testing.T) {
	key := privateKey(t)
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		if request.URL.Path != "/app/installations/44/access_tokens" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != `{"permissions":{"contents":"write"},"repositories":["workbench"]}` {
			t.Fatalf("permissions = %s", data)
		}
		expires := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"token":"installation-secret","expires_at":"` + expires + `"}`))
	}))
	defer server.Close()

	argv := []string{
		"gh", "pr", "merge", "169", "-R", "itsHabib/workbench",
		"--squash", "--delete-branch", "--match-head-commit", strings.Repeat("a", 40),
	}
	var seenArgv []string
	var seenToken string
	var result CommandResult
	err := withSession(context.Background(), AppConfig{
		AppID: 33, InstallationID: 44, PrivateKeyPEM: key,
		APIURL: server.URL, Repository: "itsHabib/workbench",
	}, server.Client(), func(session *Session) error {
		var err error
		result, err = session.merge(
			context.Background(),
			argv,
			func(_ context.Context, got []string, token string) (CommandResult, error) {
				seenArgv = got
				seenToken = token
				return CommandResult{ExitCode: 0}, nil
			},
		)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !reflect.DeepEqual(seenArgv, argv) {
		t.Fatalf("execution changed: result=%+v argv=%v", result, seenArgv)
	}
	if seenToken != "installation-secret" {
		t.Fatal("runner did not receive installation token internally")
	}
	if !strings.HasPrefix(authorization, "Bearer ") ||
		strings.Contains(authorization, "installation-secret") {
		t.Fatal("token exchange authorization is invalid")
	}
}

func TestSessionRefusesInvalidMergeCommand(t *testing.T) {
	session := &Session{}
	tests := [][]string{
		{"gh", "pr", "merge"},
		{
			"gh", "pr", "merge", "1", "-R", "o/r", "--squash",
			"--delete-branch", "--match-head-commit", strings.Repeat("a", 40), "--admin",
		},
	}
	for _, argv := range tests {
		if _, err := session.merge(context.Background(), argv, nil); err == nil {
			t.Fatal("expected argv refusal")
		}
	}
}

func TestExchangeRefusesMalformedAndOverlongToken(t *testing.T) {
	key := privateKey(t)
	for name, response := range map[string]string{
		"empty":    `{"token":"","expires_at":"2030-01-01T00:30:00Z"}`,
		"overlong": `{"token":"secret","expires_at":"2030-01-01T02:00:00Z"}`,
		"control":  "{\"token\":\"secret\\nleak\",\"expires_at\":\"2030-01-01T00:30:00Z\"}",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(response))
			}))
			defer server.Close()
			now := func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
			if _, err := exchange(context.Background(), AppConfig{
				AppID: 1, InstallationID: 2, PrivateKeyPEM: key, APIURL: server.URL,
				Repository: "o/r",
			}, server.Client(), now); !errors.Is(err, ErrToken) {
				t.Fatalf("exchange = %v, want token refusal", err)
			}
		})
	}
}

func TestChildEnvironmentIsAllowlisted(t *testing.T) {
	got := childEnvironment("installation-secret")
	want := []string{
		"GH_TOKEN=installation-secret",
		"GH_PROMPT_DISABLED=1",
		"HOME=/tmp",
		"PATH=/usr/local/bin:/usr/bin:/bin",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("child environment = %v", got)
	}
}

func privateKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}
