package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/jobs"
)

func TestJobHTTPProtocol(t *testing.T) {
	handler := jobHandler(&jobs.Store{Dir: t.TempDir()})
	call := func(body, origin, host string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/jobs", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Host = host
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}
	for _, tc := range []struct {
		body, origin, host string
		status             int
	}{
		{`{"op":"submit","id":"lesson","brief":"produce an artifact"}`, "", "127.0.0.1", 200},
		{`{"op":"submit","id":"lesson","brief":"different"}`, "", "127.0.0.1", 409},
		{`{"op":"get","id":"missing"}`, "", "127.0.0.1", 404},
		{`{"op":"list"}`, "https://evil.example", "127.0.0.1", 403},
		{`{"op":"list"}`, "", "evil.example", 403},
		{`{"op":"list","unknown":true}`, "", "127.0.0.1", 400},
		{`{"op":"list"} {}`, "", "127.0.0.1", 400},
	} {
		got := call(tc.body, tc.origin, tc.host)
		if !json.Valid(got.Body.Bytes()) {
			t.Fatalf("invalid JSON: %s", got.Body.String())
		}
		if got.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, got.Code, got.Body.String())
		}
	}
	// Both API and CLI use the same store. A fresh instance sees the submitted work.
	store := &jobs.Store{Dir: t.TempDir()}
	handler = jobHandler(store)
	rr := call(`{"op":"submit","id":"shared","brief":"durable"}`, "", "127.0.0.1")
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	result, err := store.Execute(jobs.Request{Op: "get", ID: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("durable")) {
		t.Fatal(string(b))
	}
}
