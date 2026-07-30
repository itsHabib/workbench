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
	"strings"
	"testing"
	"time"
)

func TestSessionKeepsTokenInsideExactMergeBoundary(t *testing.T) {
	key := privateKey(t)
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/app/installations/44/access_tokens":
			authorization = request.Header.Get("Authorization")
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
		case "/repos/itsHabib/workbench/pulls/169/merge":
			if request.Method != http.MethodPut {
				t.Fatalf("method = %s", request.Method)
			}
			if request.Header.Get("Authorization") != "Bearer installation-secret" {
				t.Fatal("merge did not use the custodied installation token")
			}
			data, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"merge_method":"squash","sha":"` + strings.Repeat("a", 40) + `"}`
			if string(data) != want {
				t.Fatalf("merge request = %s, want %s", data, want)
			}
			_, _ = writer.Write([]byte(
				`{"sha":"` + strings.Repeat("b", 40) + `","merged":true}`,
			))
		default:
			t.Fatalf("path = %s", request.URL.Path)
		}
	}))
	defer server.Close()

	argv := []string{
		"gh", "pr", "merge", "169", "-R", "itsHabib/workbench",
		"--squash", "--match-head-commit", strings.Repeat("a", 40),
	}
	var result CommandResult
	err := withSession(context.Background(), AppConfig{
		AppID: 33, InstallationID: 44, PrivateKeyPEM: key,
		APIURL: server.URL, Repository: "itsHabib/workbench",
	}, server.Client(), func(session *Session) error {
		var err error
		result, err = session.Merge(context.Background(), argv)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("execution failed: result=%+v", result)
	}
	if !strings.HasPrefix(authorization, "Bearer ") ||
		strings.Contains(authorization, "installation-secret") {
		t.Fatal("token exchange authorization is invalid")
	}
}

func TestSessionRefusesInvalidMergeCommand(t *testing.T) {
	session := &Session{config: AppConfig{Repository: "o/r"}}
	tests := [][]string{
		{"gh", "pr", "merge"},
		{
			"gh", "pr", "merge", "1", "-R", "o/r", "--squash",
			"--delete-branch", "--match-head-commit", strings.Repeat("a", 40),
		},
		{
			"gh", "pr", "merge", "1", "-R", "o/r", "--squash",
			"--match-head-commit", strings.Repeat("a", 40), "--admin",
		},
	}
	for _, argv := range tests {
		if _, err := session.Merge(context.Background(), argv); err == nil {
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
