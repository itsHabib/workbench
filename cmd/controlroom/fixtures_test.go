package controlroom_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	fixturesRoot    = "testdata/contracts"
	demoClockAnchor = "2026-07-13T12:00:00.000Z"
)

var requiredSources = []string{
	"ship",
	"dossier",
	"github",
	"tracelens",
	"toolhealth",
	"tower",
}

type sourceCoverage struct {
	healthy   []string
	degraded  []string
	shipRules bool
}

var inventory = map[string]sourceCoverage{
	"ship": {
		healthy: []string{
			"workflow-list-healthy.json",
			"workflow-status-healthy.json",
			"driver-list-healthy.json",
		},
		degraded: []string{
			"workflow-list-empty.json",
			"workflow-list-malformed.json",
			"source-unavailable.json",
		},
		shipRules: true,
	},
	"dossier": {
		healthy: []string{
			"task-get-healthy.json",
			"task-list-healthy.json",
		},
		degraded: []string{
			"session-failure.json",
		},
	},
	"github": {
		healthy: []string{
			"graphql-inventory-healthy.json",
			"pr-detail-complete.json",
		},
		degraded: []string{
			"receipt-inventory-truncated.json",
			"pr-detail-truncated.json",
			"source-unavailable.json",
		},
	},
	"tracelens": {
		healthy: []string{
			"analysis-findings.json",
		},
		degraded: []string{
			"analysis-unavailable-telemetry.json",
			"source-unavailable.json",
		},
	},
	"toolhealth": {
		healthy: []string{
			"accumulated-friction.txt",
		},
		degraded: []string{
			"live-incident.txt",
			"source-unavailable.json",
		},
	},
	"tower": {
		healthy: []string{
			"ls-available.json",
		},
		degraded: []string{
			"source-unavailable.json",
		},
	},
}

var shipForbiddenKeys = []string{
	"sourceJson",
	"manifestPath",
	"artifactsDir",
}

var shipForbiddenDriverRunKeys = []string{
	"id",
	"tickStartedAt",
	"tickEndedAt",
}

var shipForbiddenDriverStreamKeys = []string{
	"driverBatchId",
	"driverRunId",
	"workOnCurrentBranch",
}

var (
	fixtureSecretPatterns = secretPatterns()
	fixtureHomePatterns   = homePathPatterns()
)

func TestFixtureInventoryCoverage(t *testing.T) {
	root := fixturesRoot
	for _, source := range requiredSources {
		dir := filepath.Join(root, source)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("missing source directory %s: %v", dir, err)
		}
		cov, ok := inventory[source]
		if !ok {
			t.Fatalf("inventory missing entry for source %q", source)
		}
		for _, name := range cov.healthy {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("missing healthy fixture %s: %v", path, err)
			}
		}
		for _, name := range cov.degraded {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("missing degraded/unavailable fixture %s: %v", path, err)
			}
		}
	}
	readme := filepath.Join(root, "README.md")
	if _, err := os.Stat(readme); err != nil {
		t.Fatalf("missing fixture inventory readme: %v", err)
	}
}

func TestAllFixturePathsAreSanitized(t *testing.T) {
	err := filepath.WalkDir(fixturesRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			return err
		}
		assertNoAbsolutePaths(t, path, doc)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnavailableReceiptContract(t *testing.T) {
	// Dossier's session-failure fixture intentionally preserves MCP error framing;
	// the remaining sources use the shared adapter receipt envelope.
	for _, source := range []string{"ship", "github", "tracelens", "toolhealth", "tower"} {
		path := filepath.Join(fixturesRoot, source, "source-unavailable.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var receipt struct {
			Source     string `json:"source"`
			State      string `json:"state"`
			ObservedAt string `json:"observed_at"`
			ErrorCode  string `json:"error_code"`
			Message    string `json:"message"`
		}
		if err := json.Unmarshal(data, &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.Source != source || receipt.State != "unavailable" || receipt.ObservedAt == "" ||
			receipt.ErrorCode == "" || receipt.Message == "" {
			t.Errorf("%s: incomplete unavailable receipt", path)
		}
	}
}

func TestFixtureJSONSyntax(t *testing.T) {
	err := filepath.WalkDir(fixturesRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".json":
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !json.Valid(data) {
				t.Errorf("%s: invalid JSON", path)
			}
		case ".jsonl":
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			for i, line := range lines {
				if line == "" {
					continue
				}
				if !json.Valid([]byte(line)) {
					t.Errorf("%s:%d: invalid JSONL line", path, i+1)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFixturePrivacySanitization(t *testing.T) {
	operatorDisplayName := os.Getenv("OPERATOR_DISPLAY_NAME")
	scanRoots := []string{
		fixturesRoot,
		filepath.Join("..", "..", "docs", "features", "portfolio-control-room"),
	}
	for _, root := range scanRoots {
		if err := scanPrivacyRoot(t, root, operatorDisplayName); err != nil {
			t.Fatal(err)
		}
	}
}

func scanPrivacyRoot(t *testing.T, root, operatorDisplayName string) error {
	t.Helper()
	return filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanPrivacyFile(t, path, string(data), operatorDisplayName)
		return nil
	})
}

func scanPrivacyFile(t *testing.T, path, content, operatorDisplayName string) {
	t.Helper()
	if operatorDisplayName != "" && strings.Contains(content, operatorDisplayName) {
		t.Errorf("%s: contains configured operator display name", path)
	}
	for _, p := range fixtureSecretPatterns {
		for _, match := range p.re.FindAllString(content, -1) {
			if !isPlaceholderSecret(match) {
				t.Errorf("%s: matches secret pattern %s (%q)", path, p.name, match)
			}
		}
	}
	for _, p := range fixtureHomePatterns {
		for _, match := range p.FindAllString(content, -1) {
			if !isPlaceholderPath(match) {
				t.Errorf("%s: matches operator home path pattern", path)
			}
		}
	}
}

func TestShipFixtureContracts(t *testing.T) {
	shipDir := filepath.Join(fixturesRoot, "ship")
	files, err := filepath.Glob(filepath.Join(shipDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		assertNoForbiddenKeys(t, path, doc, shipForbiddenKeys)
	}
	assertShipWorkflowList(t, filepath.Join(shipDir, "workflow-list-healthy.json"))
	assertShipWorkflowStatus(t, filepath.Join(shipDir, "workflow-status-healthy.json"))
	assertShipDriverList(t, filepath.Join(shipDir, "driver-list-healthy.json"))
}

func TestDemoClockAnchoredHealthyFixtures(t *testing.T) {
	healthyJSON := []string{
		filepath.Join(fixturesRoot, "ship", "workflow-list-healthy.json"),
		filepath.Join(fixturesRoot, "ship", "workflow-status-healthy.json"),
		filepath.Join(fixturesRoot, "ship", "driver-list-healthy.json"),
		filepath.Join(fixturesRoot, "dossier", "task-get-healthy.json"),
		filepath.Join(fixturesRoot, "dossier", "task-list-healthy.json"),
		filepath.Join(fixturesRoot, "github", "graphql-inventory-healthy.json"),
		filepath.Join(fixturesRoot, "tracelens", "analysis-findings.json"),
		filepath.Join(fixturesRoot, "tower", "ls-available.json"),
	}
	for _, path := range healthyJSON {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), demoClockAnchor[:10]) {
			t.Errorf("%s: healthy fixture should anchor timestamps to demo clock date 2026-07-13", path)
		}
	}
}

func TestToolhealthFixturesDistinguishFrictionAndIncident(t *testing.T) {
	friction, err := os.ReadFile(filepath.Join(fixturesRoot, "toolhealth", "accumulated-friction.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(friction), "accumulated_friction") {
		t.Fatal("accumulated-friction.txt must declare accumulated_friction kind")
	}
	if strings.Contains(string(friction), "LIVE INCIDENT") {
		t.Fatal("accumulated-friction.txt must not declare a live incident")
	}
	incident, err := os.ReadFile(filepath.Join(fixturesRoot, "toolhealth", "live-incident.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(incident), "live_incident") {
		t.Fatal("live-incident.txt must declare live_incident kind")
	}
	if !strings.Contains(string(incident), "LIVE INCIDENT") {
		t.Fatal("live-incident.txt must visibly mark an active incident")
	}
}

func TestDossierFixturesUseJSONRPCFraming(t *testing.T) {
	for _, name := range []string{"task-get-healthy.json", "task-list-healthy.json", "session-failure.json"} {
		path := filepath.Join(fixturesRoot, "dossier", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if envelope["jsonrpc"] == nil {
			t.Errorf("%s: missing jsonrpc field", path)
		}
		if envelope["result"] == nil && envelope["error"] == nil {
			t.Errorf("%s: JSON-RPC envelope needs result or error", path)
		}
	}
}

func TestGitHubDetailStateFixtures(t *testing.T) {
	complete, err := os.ReadFile(filepath.Join(fixturesRoot, "github", "pr-detail-complete.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(complete), `"detail_state": "complete"`) {
		t.Fatal("pr-detail-complete.json must set detail_state complete")
	}
	truncated, err := os.ReadFile(filepath.Join(fixturesRoot, "github", "pr-detail-truncated.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(truncated), `"detail_state": "truncated"`) {
		t.Fatal("pr-detail-truncated.json must set detail_state truncated")
	}
}

type secretPattern struct {
	name string
	re   *regexp.Regexp
}

func secretPatterns() []secretPattern {
	return []secretPattern{
		{name: "github_token", re: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9]{20,}`)},
		{name: "cursor_api_key", re: regexp.MustCompile("(?i)CURSOR_API_KEY\\s*=\\s*[^\\s#`]+")},
		{name: "bearer_token", re: regexp.MustCompile(`(?i)authorization:\s*bearer\s+[A-Za-z0-9._-]{20,}`)},
		{name: "openai_anthropic_key", re: regexp.MustCompile(`sk-(?:ant-)?[A-Za-z0-9]{20,}`)},
		{
			name: "generic_env_secret",
			re:   regexp.MustCompile(`(?i)(?:^|[\s;])(?:[A-Z0-9_]*(?:TOKEN|KEY|SECRET|PASSWORD|CREDENTIAL)[A-Z0-9_]*)\s*=\s*[^\s#]+`),
		},
		{name: "private_key_pem", re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	}
}

func isPlaceholderSecret(match string) bool {
	lower := strings.ToLower(match)
	for _, ok := range []string{"<redacted>", "<placeholder>", "example", "synthetic", "{", "your_", "xxx"} {
		if strings.Contains(lower, ok) {
			return true
		}
	}
	if strings.HasSuffix(strings.TrimSpace(match), "=") {
		return true
	}
	val := match
	if i := strings.Index(match, "="); i >= 0 {
		val = strings.TrimSpace(match[i+1:])
	}
	return val == "" || val == "..." || val == "redacted"
}

func homePathPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`[A-Za-z]:\\{1,2}Users\\{1,2}[^\\]+\\{1,2}`),
		regexp.MustCompile(`/home/[^/\s]+/`),
		regexp.MustCompile(`/Users/[^/\s]+/`),
	}
}

func isPlaceholderPath(match string) bool {
	return strings.Contains(match, "<") || strings.Contains(match, "{") || strings.Contains(match, "...")
}

func assertNoForbiddenKeys(t *testing.T, path string, v any, forbidden []string) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			for _, f := range forbidden {
				if k == f {
					t.Errorf("%s: forbidden key %q", path, k)
				}
			}
			assertNoForbiddenKeys(t, path, child, forbidden)
		}
	case []any:
		for _, child := range x {
			assertNoForbiddenKeys(t, path, child, forbidden)
		}
	}
}

func assertNoAbsolutePaths(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if s, ok := child.(string); ok {
				fileURL := k == "url" && strings.HasPrefix(s, "file://")
				if (k == "path" || k == "url") && (fileURL || isDisallowedAbsolutePath(s)) {
					t.Errorf("%s: absolute path in %q: %s", path, k, s)
				}
			}
			assertNoAbsolutePaths(t, path, child)
		}
	case []any:
		for _, child := range x {
			assertNoAbsolutePaths(t, path, child)
		}
	}
}

func isDisallowedAbsolutePath(value string) bool {
	path := strings.TrimPrefix(value, "file://")
	if path == value && strings.Contains(value, "://") {
		return false
	}
	if strings.HasPrefix(path, "/tmp/") {
		return false
	}
	return strings.HasPrefix(path, "/") ||
		regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(path) ||
		strings.HasPrefix(path, `\\`)
}

func assertShipWorkflowList(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Runs []struct {
			ID      string `json:"id"`
			Repo    string `json:"repo"`
			DocPath string `json:"docPath"`
			Status  string `json:"status"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Runs) == 0 {
		t.Fatal("workflow-list-healthy.json must contain runs")
	}
	run := envelope.Runs[0]
	if run.DocPath == "" {
		t.Fatal("workflow run must include docPath linkage")
	}
	if !strings.HasPrefix(run.DocPath, "docs/") {
		t.Fatalf("docPath must be a neutral relative path, got %q", run.DocPath)
	}
}

func assertShipWorkflowStatus(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		ID      string `json:"id"`
		DocPath string `json:"docPath"`
	}
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	if run.ID == "" {
		t.Error("workflow status must include id")
	}
	if !strings.HasPrefix(run.DocPath, "docs/") {
		t.Errorf("workflow status docPath must be neutral relative, got %q", run.DocPath)
	}
}

func assertShipDriverList(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		V    int              `json:"v"`
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.V != 1 {
		t.Fatalf("driver list envelope version want 1 got %d", envelope.V)
	}
	if len(envelope.Runs) == 0 {
		t.Fatal("driver-list-healthy.json must contain runs")
	}
	for _, run := range envelope.Runs {
		assertShipDriverRun(t, run)
	}
}

func assertShipDriverRun(t *testing.T, run map[string]any) {
	t.Helper()
	for _, key := range shipForbiddenDriverRunKeys {
		if _, ok := run[key]; ok {
			t.Errorf("driver run must not expose %q", key)
		}
	}
	batches, ok := run["batches"].([]any)
	if !ok || len(batches) == 0 {
		t.Error("driver run must include batches")
		return
	}
	for _, batch := range batches {
		assertShipDriverBatch(t, batch)
	}
}

func assertShipDriverBatch(t *testing.T, batch any) {
	t.Helper()
	batchMap, ok := batch.(map[string]any)
	if !ok {
		t.Error("driver batch must be an object")
		return
	}
	batchIndex, _ := batchMap["batchIndex"].(float64)
	if batchIndex < 1 {
		t.Error("driver batchIndex must follow Ship's one-based contract")
	}
	streams, ok := batchMap["streams"].([]any)
	if !ok || len(streams) == 0 {
		t.Error("driver batch must include streams")
		return
	}
	for _, stream := range streams {
		assertShipDriverStream(t, stream)
	}
}

func assertShipDriverStream(t *testing.T, stream any) {
	t.Helper()
	streamMap, ok := stream.(map[string]any)
	if !ok {
		t.Error("driver stream must be an object")
		return
	}
	for _, key := range shipForbiddenDriverStreamKeys {
		if _, ok := streamMap[key]; ok {
			t.Errorf("driver stream must not expose %q", key)
		}
	}
	streamIndex, ok := streamMap["streamIndex"].(float64)
	if !ok || streamIndex < 0 {
		t.Error("driver streamIndex must follow Ship's zero-based contract")
	}
	specPath, _ := streamMap["specPath"].(string)
	if specPath == "" {
		t.Error("driver stream must include specPath linkage")
		return
	}
	if !strings.HasPrefix(specPath, "docs/") {
		t.Fatalf("specPath must be neutral relative, got %q", specPath)
	}
}
