package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

func TestCloudModelAdaptsToolUseToContentString(t *testing.T) {
	input := map[string]any{
		"bucket":     "real-break",
		"evidence":   "parse_test.go:42: got 3, want 4",
		"why":        "TestParse assertion",
		"confidence": 0.9,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"content":[{"type":"tool_use","id":"tu_1","name":"structured_output","input":%s}]}`, mustJSON(t, input))
	}))
	t.Cleanup(srv.Close)

	m := &cloudModel{
		model:  cloudModelDefault,
		apiKey: "test-key",
		url:    srv.URL,
		client: srv.Client(),
	}
	content, err := m.chat(context.Background(), ciPrompt, "log excerpt", ciSchema)
	if err != nil {
		t.Fatal(err)
	}
	var adv ciAdvisory
	if err := json.Unmarshal([]byte(content), &adv); err != nil {
		t.Fatalf("unmarshal adapted content: %v", err)
	}
	if adv.Bucket != "real-break" || adv.Evidence != input["evidence"] {
		t.Fatalf("adapter content: %+v", adv)
	}
}

func TestCloudModelSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad key"}}`)
	}))
	t.Cleanup(srv.Close)

	m := &cloudModel{
		model:  cloudModelDefault,
		apiKey: "test-key",
		url:    srv.URL,
		client: srv.Client(),
	}
	_, err := m.chat(context.Background(), ciPrompt, "log excerpt", ciSchema)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected status in error, got: %v", err)
	}
}

type recordingModel struct {
	called bool
}

func (m *recordingModel) chat(_ context.Context, _, _ string, _ json.RawMessage) (string, error) {
	m.called = true
	return `{"headline":"x","severity":"low","verdict":"nit","confidence":0.9}`, nil
}

func (m *recordingModel) impl() string { return "recording" }

func TestReviewsBackendSelection(t *testing.T) {
	t.Run("default local when nil", func(t *testing.T) {
		got, err := ModelBackend("local")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.(*localModel); !ok {
			t.Fatalf("expected localModel, got %T", got)
		}
	})
	t.Run("selected model invoked", func(t *testing.T) {
		rec := &recordingModel{}
		st, err := state.Open(t.TempDir(), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		evd, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{
			"comments": []map[string]any{
				{"author": "bot[bot]", "is_bot": true, "body": "nit: rename foo"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Reviews(st, "run_t", evd.ID, subj, rec); err != nil {
			t.Fatal(err)
		}
		if !rec.called {
			t.Fatal("expected injected model to be called")
		}
	})
}

func TestCIClassifyBackendSelection(t *testing.T) {
	t.Run("default local when nil", func(t *testing.T) {
		got, err := ModelBackend("")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.(*localModel); !ok {
			t.Fatalf("expected localModel, got %T", got)
		}
	})
	t.Run("cloud backend", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		got, err := ModelBackend("cloud")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.(*cloudModel); !ok {
			t.Fatalf("expected cloudModel, got %T", got)
		}
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
