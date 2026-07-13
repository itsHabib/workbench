// Command controlroom serves and snapshots the local portfolio operations dashboard.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/adapters/dossier"
	githubadapter "github.com/itsHabib/workbench/cmd/controlroom/internal/adapters/github"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/adapters/ship"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/adapters/toolhealth"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/adapters/tower"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/adapters/tracelens"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/app"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/demo"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/web"
)

const defaultAddr = "127.0.0.1:4317"

type dependencies struct {
	listen func(string, string) (net.Listener, error)
	serve  func(net.Listener, http.Handler) error
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	return runWith(args, stdout, stderr, dependencies{
		listen: net.Listen,
		serve: func(listener net.Listener, handler http.Handler) error {
			return newHTTPServer(handler).Serve(listener)
		},
	})
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
}

func runWith(args []string, stdout, stderr io.Writer, deps dependencies) error {
	if len(args) == 0 {
		return usageError("missing subcommand")
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], stdout, stderr, deps)
	case "snapshot":
		return runSnapshot(args[1:], stdout, stderr)
	default:
		return usageError("unknown subcommand %q", args[0])
	}
}

func runSnapshot(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mode := flags.String("mode", "", "data mode")
	jsonOutput := flags.Bool("json", false, "write JSON")
	realConfig := addRealFlags(flags)
	if err := flags.Parse(args); err != nil {
		return usageError("snapshot flags: %v", err)
	}
	if flags.NArg() != 0 || (*mode != "demo" && *mode != "real") || !*jsonOutput {
		return usageError("snapshot requires --mode demo|real --json")
	}
	snapshot := demo.Snapshot()
	if *mode == "real" {
		publisher, err := newRealPublisher(*realConfig)
		if err != nil {
			return usageError("real configuration: %v", err)
		}
		defer publisher.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
		defer cancel()
		snapshot, err = publisher.Collect(ctx, "manual")
		if err != nil {
			return fmt.Errorf("collect snapshot: %w", err)
		}
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	return nil
}

func runServe(args []string, stdout, stderr io.Writer, deps dependencies) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mode := flags.String("mode", "", "data mode")
	addr := flags.String("addr", defaultAddr, "listen address")
	realConfig := addRealFlags(flags)
	if err := flags.Parse(args); err != nil {
		return usageError("serve flags: %v", err)
	}
	if flags.NArg() != 0 || (*mode != "demo" && *mode != "real") {
		return usageError("serve requires --mode demo|real")
	}
	if err := validateAddr(*addr); err != nil {
		return usageError("invalid --addr: %v", err)
	}
	var snapshot web.SnapshotSupplier
	var refresh web.RefreshFunc
	closePublisher := func() {}
	if *mode == "demo" {
		publisher := newDemoPublisher()
		snapshot, refresh = publisher.snapshot, publisher.refresh
	} else {
		publisher, buildErr := newRealPublisher(*realConfig)
		if buildErr != nil {
			return usageError("real configuration: %v", buildErr)
		}
		snapshot = publisher.Snapshot
		refresh = func(_ context.Context, request web.RefreshRequest) (web.RefreshReceipt, error) {
			receipt, refreshErr := publisher.Refresh(request.Trigger)
			return web.RefreshReceipt{BaselineVersion: receipt.BaselineVersion, Status: receipt.Status}, refreshErr
		}
		closePublisher = publisher.Close
	}
	defer closePublisher()
	listener, err := deps.listen("tcp4", *addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	host := listener.Addr().String()
	handler, err := web.New(web.Config{Host: host, Mode: *mode, Snapshot: snapshot, Refresh: refresh})
	if err != nil {
		return fmt.Errorf("construct server: %w", err)
	}
	if _, err := fmt.Fprintf(stdout, "http://%s\n", host); err != nil {
		return fmt.Errorf("write URL: %w", err)
	}
	if err := deps.serve(listener, handler); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type realFlags struct {
	workspaceRoot string
	dossierCorpus string
	githubScopes  stringList
	ship          string
	dossier       string
	github        string
	tower         string
	tracelens     string
	toolhealth    string
}

func addRealFlags(flags *flag.FlagSet) *realFlags {
	config := &realFlags{}
	flags.StringVar(&config.workspaceRoot, "workspace-root", "", "absolute portfolio workspace root")
	flags.StringVar(&config.dossierCorpus, "dossier-corpus", "", "absolute Dossier corpus path")
	flags.Var(&config.githubScopes, "github-scope", "GitHub scope (repeat one to four times)")
	flags.StringVar(&config.ship, "ship-executable", "ship", "Ship executable")
	flags.StringVar(&config.dossier, "dossier-executable", "dossier", "Dossier executable")
	flags.StringVar(&config.github, "github-executable", "gh", "GitHub CLI executable")
	flags.StringVar(&config.tower, "tower-executable", "", "optional Tower executable")
	flags.StringVar(&config.tracelens, "tracelens-executable", "tracelens", "Tracelens executable")
	flags.StringVar(&config.toolhealth, "toolhealth-executable", "toolhealth", "tool-health executable")
	return config
}

type realPublisher struct {
	*app.Coordinator
	dossier *dossier.Adapter
}

func (publisher *realPublisher) Close() {
	publisher.Coordinator.Close()
	_ = publisher.dossier.Close()
}

func newRealPublisher(flags realFlags) (*realPublisher, error) {
	workspace, err := requiredAbsoluteDirectory("workspace root", flags.workspaceRoot)
	if err != nil {
		return nil, err
	}
	corpus, err := requiredAbsoluteDirectory("Dossier corpus", flags.dossierCorpus)
	if err != nil {
		return nil, err
	}
	if len(flags.githubScopes) < 1 || len(flags.githubScopes) > 4 {
		return nil, fmt.Errorf("one to four --github-scope values are required")
	}
	configured := map[string]string{
		"ship": flags.ship, "dossier": flags.dossier, "github": flags.github,
		"tracelens": flags.tracelens, "toolhealth": flags.toolhealth,
	}
	for name, value := range configured {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s executable command is required", name)
		}
	}
	configured["tower"] = flags.tower
	executables := make(map[string]string, len(configured))
	executableIdentities := make(map[string]string, len(configured))
	for name, value := range configured {
		executables[name], executableIdentities[name] = sourceExecutable(value)
	}
	shipAdapter := ship.New(executables["ship"])
	dossierAdapter := dossier.New(executables["dossier"], corpus)
	githubAdapter, err := githubadapter.New(executables["github"], flags.githubScopes)
	if err != nil {
		return nil, fmt.Errorf("GitHub scopes: %w", err)
	}
	towerAdapter := tower.New(executables["tower"])
	tracelensAdapter := tracelens.New(executables["tracelens"])
	toolhealthAdapter := toolhealth.New(executables["toolhealth"])
	fingerprint := configFingerprint(workspace, corpus, flags.githubScopes, executableIdentities)
	coordinator, err := app.New(app.Config{Mode: "real", Fingerprint: fingerprint, Collectors: app.Collectors{
		Ship: func(ctx context.Context) app.Result {
			result := shipAdapter.Collect(ctx)
			return app.Result{Receipt: result.Receipt, Runs: result.Runs}
		},
		Dossier: func(ctx context.Context, manual bool) app.Result {
			var result dossier.Result
			if manual {
				result = dossierAdapter.CollectManual(ctx)
			} else {
				result = dossierAdapter.Collect(ctx)
			}
			return app.Result{Receipt: result.Receipt, Tasks: result.Tasks}
		},
		GitHub: func(ctx context.Context) app.Result {
			result := githubAdapter.Collect(ctx)
			return app.Result{Receipt: result.Receipt, PullRequests: result.PullRequests}
		},
		Tower: func(ctx context.Context) app.Result {
			result := towerAdapter.Collect(ctx)
			return app.Result{Receipt: result.Receipt}
		},
		Tracelens: func(ctx context.Context, runs []model.Run, receipt model.SourceReceipt) app.Result {
			result := tracelensAdapter.Collect(ctx, runs, receipt)
			return app.Result{Receipt: result.Receipt, Reliability: result.Diagnoses}
		},
		ToolHealth: func(ctx context.Context) app.Result {
			result := toolhealthAdapter.Collect(ctx)
			return app.Result{Receipt: result.Receipt, ToolHealth: result.Tools}
		},
	}})
	if err != nil {
		_ = dossierAdapter.Close()
		return nil, err
	}
	return &realPublisher{Coordinator: coordinator, dossier: dossierAdapter}, nil
}

func requiredAbsoluteDirectory(label, value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be an explicit absolute path", label)
	}
	clean := filepath.Clean(value)
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s must name an existing directory", label)
	}
	return clean, nil
}

func sourceExecutable(value string) (runtimePath, identity string) {
	if value == "" {
		return "", "disabled"
	}
	resolved, err := exec.LookPath(value)
	if err != nil {
		return value, "unresolved:" + value
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return resolved, "resolved:" + resolved
	}
	return absolute, absolute
}

func configFingerprint(workspace, corpus string, scopes []string, executables map[string]string) string {
	sortedScopes := slices.Clone(scopes)
	slices.Sort(sortedScopes)
	names := make([]string, 0, len(executables))
	for name := range executables {
		names = append(names, name)
	}
	slices.Sort(names)
	ordered := make([][2]string, 0, len(names))
	for _, name := range names {
		ordered = append(ordered, [2]string{name, filepath.Clean(executables[name])})
	}
	payload, _ := json.Marshal(struct {
		Workspace   string      `json:"workspace"`
		Corpus      string      `json:"corpus"`
		Scopes      []string    `json:"scopes"`
		Executables [][2]string `json:"executables"`
	}{filepath.Clean(workspace), filepath.Clean(corpus), sortedScopes, ordered})
	return fmt.Sprintf("%x", sha256.Sum256(payload))
}

func validateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		return fmt.Errorf("host must be IPv4 loopback 127.0.0.1")
	}
	if port == "" {
		return fmt.Errorf("port is required")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return fmt.Errorf("port must be between 0 and 65535")
	}
	return nil
}

func usageError(format string, args ...any) error {
	return fmt.Errorf("usage error: "+format, args...)
}

type demoPublisher struct {
	mu      sync.RWMutex
	version uint64
}

func newDemoPublisher() *demoPublisher { return &demoPublisher{version: 1} }

func (p *demoPublisher) snapshot() model.Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	snapshot := demo.Snapshot()
	snapshot.Version = p.version
	return snapshot
}

func (p *demoPublisher) refresh(_ context.Context, _ web.RefreshRequest) (web.RefreshReceipt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	baseline := p.version
	p.version++
	return web.RefreshReceipt{BaselineVersion: baseline, Status: "started"}, nil
}
