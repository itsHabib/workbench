package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/demo"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

const testHost = "127.0.0.1:4317"

func TestRouteAndMethodMatrix(t *testing.T) {
	handler, _ := testHandler(t, nil, nil)
	tests := []struct {
		method      string
		path        string
		wantStatus  int
		wantType    string
		wantAllow   string
		wantNoStore bool
	}{
		{http.MethodGet, "/", http.StatusOK, "text/html; charset=utf-8", "", true},
		{http.MethodHead, "/", http.StatusOK, "text/html; charset=utf-8", "", true},
		{http.MethodGet, "/static/app.js", http.StatusOK, "text/javascript; charset=utf-8", "", false},
		{http.MethodHead, "/static/styles.css", http.StatusOK, "text/css; charset=utf-8", "", false},
		{http.MethodGet, "/api/v1/snapshot", http.StatusOK, "application/json", "", true},
		{http.MethodHead, "/api/v1/snapshot", http.StatusOK, "application/json", "", true},
		{http.MethodGet, "/healthz", http.StatusOK, "text/plain; charset=utf-8", "", true},
		{http.MethodHead, "/healthz", http.StatusOK, "text/plain; charset=utf-8", "", true},
		{http.MethodPost, "/", http.StatusMethodNotAllowed, "text/plain; charset=utf-8", "GET, HEAD", false},
		{http.MethodDelete, "/api/v1/snapshot", http.StatusMethodNotAllowed, "text/plain; charset=utf-8", "GET, HEAD", false},
		{http.MethodGet, "/api/v1/refresh", http.StatusMethodNotAllowed, "text/plain; charset=utf-8", "POST", false},
		{http.MethodGet, "/missing", http.StatusNotFound, "text/plain; charset=utf-8", "", false},
		{http.MethodGet, "/static/../server.go", http.StatusNotFound, "text/plain; charset=utf-8", "", false},
	}
	for _, test := range tests {
		t.Run(test.method+"_"+test.path, func(t *testing.T) {
			recorder := request(t, handler, test.method, test.path, nil)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); got != test.wantType {
				t.Fatalf("content type = %q", got)
			}
			if got := recorder.Header().Get("Allow"); got != test.wantAllow {
				t.Fatalf("Allow = %q", got)
			}
			if test.wantNoStore && recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
			}
			assertSecurityHeaders(t, recorder)
			if test.method == http.MethodHead && recorder.Body.Len() != 0 {
				t.Fatalf("HEAD body = %q", recorder.Body.String())
			}
		})
	}
}

func TestHostAndQueryRejection(t *testing.T) {
	handler, _ := testHandler(t, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	req.Host = "localhost:4317"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("host status = %d", recorder.Code)
	}
	for _, path := range []string{"/?repo=x", "/api/v1/snapshot?repo=x", "/healthz?deep=1"} {
		if got := request(t, handler, http.MethodGet, path, nil).Code; got != http.StatusBadRequest {
			t.Fatalf("%s status = %d", path, got)
		}
	}
}

func TestShellCookieAndSecurityContract(t *testing.T) {
	handler, _ := testHandler(t, nil, nil)
	recorder := request(t, handler, http.MethodGet, "/", nil)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %+v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != csrfCookie || cookie.Value == "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode || cookie.HttpOnly || cookie.Secure {
		t.Fatalf("cookie = %+v", cookie)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header was emitted")
	}
	if got := recorder.Header().Get("Content-Security-Policy"); got != csp {
		t.Fatalf("CSP = %q", got)
	}
}

func TestRefreshRejectsEveryInvalidLayerWithoutCallback(t *testing.T) {
	calls := 0
	refresh := func(context.Context, RefreshRequest) (RefreshReceipt, error) {
		calls++
		return RefreshReceipt{BaselineVersion: 1, Status: "started"}, nil
	}
	handler, token := testHandler(t, nil, refresh)
	tests := []struct {
		name        string
		contentType string
		origin      string
		cookie      string
		header      string
		body        string
	}{
		{"missing content type", "", "http://" + testHost, token, token, validRefreshBody()},
		{"parameterized content type", "application/json; charset=utf-8", "http://" + testHost, token, token, validRefreshBody()},
		{"wrong origin", "application/json", "http://localhost:4317", token, token, validRefreshBody()},
		{"missing cookie", "application/json", "http://" + testHost, "", token, validRefreshBody()},
		{"wrong cookie", "application/json", "http://" + testHost, "wrong", token, validRefreshBody()},
		{"missing header", "application/json", "http://" + testHost, token, "", validRefreshBody()},
		{"wrong header", "application/json", "http://" + testHost, token, "wrong", validRefreshBody()},
		{"malformed body", "application/json", "http://" + testHost, token, token, "{"},
		{"unknown body field", "application/json", "http://" + testHost, token, token, `{"mode":"demo","trigger":"manual","extra":true}`},
		{"wrong mode", "application/json", "http://" + testHost, token, token, `{"mode":"real","trigger":"manual"}`},
		{"wrong trigger", "application/json", "http://" + testHost, token, token, `{"mode":"demo","trigger":"automatic"}`},
		{"trailing JSON", "application/json", "http://" + testHost, token, token, validRefreshBody() + `{}`},
		{"oversized body", "application/json", "http://" + testHost, token, token, strings.Repeat(" ", 1025) + validRefreshBody()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://"+testHost+"/api/v1/refresh", strings.NewReader(test.body))
			req.Host = testHost
			if test.contentType != "" {
				req.Header.Set("Content-Type", test.contentType)
			}
			if test.origin != "" {
				req.Header.Set("Origin", test.origin)
			}
			if test.header != "" {
				req.Header.Set("X-Control-Room-CSRF", test.header)
			}
			if test.cookie != "" {
				req.AddCookie(&http.Cookie{Name: csrfCookie, Value: test.cookie})
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
			}
		})
	}
	if calls != 0 {
		t.Fatalf("refresh callback calls = %d", calls)
	}
}

func TestAcceptedRefreshReturnsReceiptAndBumpsSnapshot(t *testing.T) {
	version := uint64(1)
	snapshot := func() model.Snapshot {
		value := demo.Snapshot()
		value.Version = version
		return value
	}
	refresh := func(_ context.Context, request RefreshRequest) (RefreshReceipt, error) {
		if request.Mode != "demo" || request.Trigger != "manual" {
			t.Fatalf("request = %+v", request)
		}
		baseline := version
		version++
		return RefreshReceipt{BaselineVersion: baseline, Status: "started"}, nil
	}
	handler, token := testHandler(t, snapshot, refresh)
	req := httptest.NewRequest(http.MethodPost, "http://"+testHost+"/api/v1/refresh", strings.NewReader(validRefreshBody()))
	req.Host = testHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+testHost)
	req.Header.Set("X-Control-Room-CSRF", token)
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	var receipt RefreshReceipt
	if err := json.Unmarshal(recorder.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.BaselineVersion != 1 || receipt.Status != "started" || version != 2 {
		t.Fatalf("receipt = %+v, version = %d", receipt, version)
	}
}

func TestRealAutomaticRefreshUsesServerMode(t *testing.T) {
	called := false
	handler, err := New(Config{
		Host: testHost, Mode: "real", Snapshot: demo.Snapshot,
		Refresh: func(_ context.Context, request RefreshRequest) (RefreshReceipt, error) {
			called = request.Mode == "real" && request.Trigger == "auto"
			return RefreshReceipt{BaselineVersion: 1, Status: "started"}, nil
		},
		TokenSource: func(buffer []byte) (int, error) {
			for i := range buffer {
				buffer[i] = 0x5a
			}
			return len(buffer), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	shell := request(t, handler, http.MethodGet, "/", nil)
	token := shell.Result().Cookies()[0].Value
	if !strings.Contains(shell.Body.String(), `data-control-room-mode="real"`) || !strings.Contains(shell.Body.String(), ">REAL</span>") {
		t.Fatalf("real shell mode was not rendered: %s", shell.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "http://"+testHost+"/api/v1/refresh", strings.NewReader(`{"mode":"real","trigger":"auto"}`))
	req.Host = testHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+testHost)
	req.Header.Set("X-Control-Room-CSRF", token)
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted || !called {
		t.Fatalf("response=%d called=%v body=%q", recorder.Code, called, recorder.Body.String())
	}
}

func TestSnapshotEncodingFailureIsSafe(t *testing.T) {
	snapshot := func() model.Snapshot {
		value := demo.Snapshot()
		value.Runs[0].Actual.Runtime = model.Availability[string]{State: model.Available}
		return value
	}
	handler, _ := testHandler(t, snapshot, nil)
	recorder := request(t, handler, http.MethodGet, "/api/v1/snapshot", nil)
	if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != "snapshot unavailable\n" {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "available value is missing") {
		t.Fatal("internal encoding detail leaked")
	}
}

func TestEmbeddedShellAndScriptContract(t *testing.T) {
	htmlBytes, err := AssetSource("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlBytes)
	for _, required := range []string{"<h1", "id=\"main-content\"", "Skip to main content", "<dialog", "Runs", "Tasks", "Pull requests", "Reliability", "Tool health", "Sources", "/static/app.js", "/static/styles.css"} {
		if !strings.Contains(html, required) {
			t.Errorf("shell missing %q", required)
		}
	}
	if strings.Contains(html, "<style") || strings.Contains(html, "<script>") {
		t.Fatal("shell contains inline style or script")
	}
	appBytes, err := AssetSource("app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := string(appBytes)
	for _, required := range []string{`addEventListener("cancel"`, `addEventListener("keydown"`, `event.key !== "Escape"`, "item.rule_id", "item.id", "critical: 4", "drawer.dataset.entityType", "snapshot.reliability.find", "operator_state", "next_action", "formatTime(run.updated_at)", "Tracelens findings", "tokens.includes(needle)", "X-Control-Room-CSRF", `refresh("auto")`, "60000", "attempt < 110", "diagnostic sources remained loading"} {
		if !strings.Contains(app, required) {
			t.Errorf("app missing interaction contract %q", required)
		}
	}
	for _, forbidden := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "Date.now()", "file://", "vscode://"} {
		if strings.Contains(app, forbidden) {
			t.Errorf("app contains forbidden token %q", forbidden)
		}
	}
	if _, err := AssetSource("../server.go"); err == nil {
		t.Fatal("asset traversal was accepted")
	}
}

func TestProducerHTMLNeverAppearsInShell(t *testing.T) {
	snapshot := func() model.Snapshot {
		value := demo.Snapshot()
		value.Tasks[0].Title = `<img src=x onerror="alert(1)">`
		return value
	}
	handler, _ := testHandler(t, snapshot, nil)
	shell := request(t, handler, http.MethodGet, "/", nil)
	if strings.Contains(shell.Body.String(), "onerror") {
		t.Fatal("producer string reached server-rendered HTML")
	}
	api := request(t, handler, http.MethodGet, "/api/v1/snapshot", nil)
	if !bytes.Contains(api.Body.Bytes(), []byte(`\u003cimg`)) {
		t.Fatalf("JSON did not encode HTML safely: %s", api.Body.String())
	}
}

func testHandler(t *testing.T, snapshot SnapshotSupplier, refresh RefreshFunc) (http.Handler, string) {
	t.Helper()
	if snapshot == nil {
		snapshot = demo.Snapshot
	}
	if refresh == nil {
		refresh = func(context.Context, RefreshRequest) (RefreshReceipt, error) {
			return RefreshReceipt{BaselineVersion: 1, Status: "started"}, nil
		}
	}
	handler, err := New(Config{
		Host: testHost, Mode: "demo", Snapshot: snapshot, Refresh: refresh,
		TokenSource: func(buffer []byte) (int, error) {
			for i := range buffer {
				buffer[i] = 0x5a
			}
			return len(buffer), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	shell := request(t, handler, http.MethodGet, "/", nil)
	cookies := shell.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie bootstrap = %+v", cookies)
	}
	return handler, cookies[0].Value
}

func request(t *testing.T, handler http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+testHost+path, body)
	req.Host = testHost
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func validRefreshBody() string { return `{"mode":"demo","trigger":"manual"}` }

func assertSecurityHeaders(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
	if recorder.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing no-referrer policy")
	}
	if recorder.Header().Get("Content-Security-Policy") != csp {
		t.Fatal("missing CSP")
	}
}
