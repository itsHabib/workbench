package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/demo"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/web"
)

func TestHTTPServerTimeouts(t *testing.T) {
	server := newHTTPServer(http.NotFoundHandler())
	if server.ReadHeaderTimeout != 5*time.Second || server.ReadTimeout != 30*time.Second || server.WriteTimeout != 30*time.Second {
		t.Fatalf("unexpected HTTP timeouts: header=%s read=%s write=%s", server.ReadHeaderTimeout, server.ReadTimeout, server.WriteTimeout)
	}
}

func TestSnapshotCommandMatchesDemoContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"snapshot", "--mode", "demo", "--json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid output: %v", err)
	}
	wantBytes, err := json.Marshal(demo.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantBytes, &want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustJSON(t, got), mustJSON(t, want)) {
		t.Fatalf("snapshot output drifted\ngot: %s\nwant: %s", mustJSON(t, got), mustJSON(t, want))
	}
	if !strings.HasPrefix(stdout.String(), "{\n  \"") || !strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("snapshot is not indented JSON: %q", stdout.String())
	}
}

func TestCommandUsageErrors(t *testing.T) {
	tests := [][]string{
		nil,
		{"unknown"},
		{"snapshot", "--mode", "real", "--json"},
		{"snapshot", "--mode", "demo"},
		{"serve", "--mode", "real"},
		{"serve", "--mode", "demo", "--addr", "0.0.0.0:4317"},
		{"serve", "--mode", "demo", "--addr", "localhost:4317"},
		{"serve", "--mode", "demo", "--addr", "127.0.0.1:not-a-port"},
		{"serve", "--mode", "demo", "--addr", "127.0.0.1:70000"},
	}
	for _, args := range tests {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runWith(args, &stdout, &stderr, dependencies{
				listen: func(string, string) (net.Listener, error) { return nil, errors.New("must not listen") },
				serve:  func(net.Listener, http.Handler) error { return errors.New("must not serve") },
			})
			if err == nil || !strings.Contains(err.Error(), "usage error") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestServePrintsCanonicalEphemeralURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runWith([]string{"serve", "--mode", "demo", "--addr", "127.0.0.1:0"}, &stdout, &stderr, dependencies{
		listen: net.Listen,
		serve: func(listener net.Listener, handler http.Handler) error {
			request, err := http.NewRequest(http.MethodGet, "http://"+listener.Addr().String()+"/healthz", nil)
			if err != nil {
				return err
			}
			request.Host = listener.Addr().String()
			recorder := &responseRecorder{header: make(http.Header)}
			handler.ServeHTTP(recorder, request)
			if recorder.status != http.StatusOK {
				return errors.New("handler was not usable")
			}
			return http.ErrServerClosed
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout.String(), "http://127.0.0.1:") || strings.Contains(stdout.String(), ":0") {
		t.Fatalf("URL = %q", stdout.String())
	}
}

func TestDemoPublisherBumpsMonotonically(t *testing.T) {
	publisher := newDemoPublisher()
	first := publisher.snapshot()
	receipt, err := publisher.refresh(t.Context(), structRefreshRequest())
	if err != nil {
		t.Fatal(err)
	}
	second := publisher.snapshot()
	if first.Version != 1 || receipt.BaselineVersion != 1 || receipt.Status != "started" || second.Version != 2 {
		t.Fatalf("versions = %d, %+v, %d", first.Version, receipt, second.Version)
	}
	if !first.GeneratedAt.Equal(second.GeneratedAt) {
		t.Fatal("refresh changed the fixed demo clock")
	}
}

func structRefreshRequest() web.RefreshRequest {
	return web.RefreshRequest{Mode: "demo", Trigger: "manual"}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type responseRecorder struct {
	header http.Header
	status int
}

func (r *responseRecorder) Header() http.Header            { return r.header }
func (r *responseRecorder) Write(body []byte) (int, error) { return len(body), nil }
func (r *responseRecorder) WriteHeader(status int)         { r.status = status }
