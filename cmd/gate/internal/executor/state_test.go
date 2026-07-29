package executor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublishGateStateCASAndRefetch(t *testing.T) {
	fixture := newStateAPIFixture(t)
	defer fixture.server.Close()

	config := AppConfig{
		AppID: 1, InstallationID: 2, PrivateKeyPEM: privateKey(t),
		APIURL: fixture.server.URL, Repository: "o/r",
	}
	err := withSession(
		context.Background(),
		config,
		fixture.server.Client(),
		func(session *Session) error {
			snapshot, err := session.PublishGateState(
				context.Background(),
				fixture.parent,
				"gate: claim gxc_test",
				fixture.files,
			)
			if err != nil {
				return err
			}
			if snapshot.Tip != fixture.next ||
				string(snapshot.Files.Log) != string(fixture.files.Log) ||
				string(snapshot.Files.Anchor) != string(fixture.files.Anchor) {
				t.Fatalf("snapshot = %+v", snapshot)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

type stateAPIFixture struct {
	server *httptest.Server
	parent string
	next   string
	files  StateFiles
}

func newStateAPIFixture(t *testing.T) stateAPIFixture {
	t.Helper()
	parent := strings.Repeat("a", 40)
	parentTree := strings.Repeat("b", 40)
	logBlob := strings.Repeat("c", 40)
	anchorBlob := strings.Repeat("d", 40)
	nextTree := strings.Repeat("e", 40)
	next := strings.Repeat("f", 40)
	files := StateFiles{Log: []byte("log\n"), Anchor: []byte("anchor\n")}
	current := parent
	blobCalls := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/2/access_tokens", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		expires := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)
		writer.WriteHeader(http.StatusCreated)
		fmt.Fprintf(writer, `{"token":"secret","expires_at":%q}`, expires)
	})
	mux.HandleFunc("/repos/o/r/git/ref/heads/gate-state", func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(writer, `{"object":{"sha":%q}}`, current)
	})
	mux.HandleFunc("/repos/o/r/git/commits/"+parent, func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(writer, `{"sha":%q,"tree":{"sha":%q}}`, parent, parentTree)
	})
	mux.HandleFunc("/repos/o/r/git/blobs", func(writer http.ResponseWriter, _ *http.Request) {
		blobCalls++
		sha := logBlob
		if blobCalls == 2 {
			sha = anchorBlob
		}
		fmt.Fprintf(writer, `{"sha":%q}`, sha)
	})
	mux.HandleFunc("/repos/o/r/git/trees", func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(writer, `{"sha":%q}`, nextTree)
	})
	mux.HandleFunc("/repos/o/r/git/commits", func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(writer, `{"sha":%q,"tree":{"sha":%q}}`, next, nextTree)
	})
	mux.HandleFunc("/repos/o/r/git/refs/heads/gate-state", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			SHA   string `json:"sha"`
			Force bool   `json:"force"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.SHA != next || body.Force {
			t.Fatalf("unsafe ref update: %+v", body)
		}
		current = next
		fmt.Fprintf(writer, `{"object":{"sha":%q}}`, next)
	})
	mux.HandleFunc("/repos/o/r/git/commits/"+next, func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(writer, `{"sha":%q,"tree":{"sha":%q}}`, next, nextTree)
	})
	mux.HandleFunc("/repos/o/r/git/trees/"+nextTree, func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(
			writer,
			`{"tree":[{"path":"state/log.jsonl","mode":"100644","type":"blob","sha":%q},{"path":"anchor.json","mode":"100644","type":"blob","sha":%q}]}`,
			logBlob,
			anchorBlob,
		)
	})
	mux.HandleFunc("/repos/o/r/git/blobs/"+logBlob, func(writer http.ResponseWriter, _ *http.Request) {
		writeBlob(writer, logBlob, files.Log)
	})
	mux.HandleFunc("/repos/o/r/git/blobs/"+anchorBlob, func(writer http.ResponseWriter, _ *http.Request) {
		writeBlob(writer, anchorBlob, files.Anchor)
	})
	return stateAPIFixture{
		server: httptest.NewServer(mux),
		parent: parent,
		next:   next,
		files:  files,
	}
}

func TestPublishGateStateRefusesMovedTipBeforeWrites(t *testing.T) {
	current := strings.Repeat("a", 40)
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/app/installations/2/access_tokens":
			expires := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)
			writer.WriteHeader(http.StatusCreated)
			fmt.Fprintf(writer, `{"token":"secret","expires_at":%q}`, expires)
		case "/repos/o/r/git/ref/heads/gate-state":
			fmt.Fprintf(writer, `{"object":{"sha":%q}}`, current)
		default:
			writes++
		}
	}))
	defer server.Close()

	config := AppConfig{
		AppID: 1, InstallationID: 2, PrivateKeyPEM: privateKey(t),
		APIURL: server.URL, Repository: "o/r",
	}
	err := withSession(context.Background(), config, server.Client(), func(session *Session) error {
		_, err := session.PublishGateState(
			context.Background(),
			strings.Repeat("b", 40),
			"gate: claim gxc_test",
			StateFiles{Log: []byte("log\n"), Anchor: []byte("anchor\n")},
		)
		return err
	})
	if !errors.Is(err, ErrStateCAS) {
		t.Fatalf("publish = %v, want CAS refusal", err)
	}
	if writes != 0 {
		t.Fatalf("moved tip caused %d writes", writes)
	}
}

func writeBlob(writer http.ResponseWriter, sha string, data []byte) {
	fmt.Fprintf(
		writer,
		`{"sha":%q,"encoding":"base64","content":%q}`,
		sha,
		base64.StdEncoding.EncodeToString(data),
	)
}
