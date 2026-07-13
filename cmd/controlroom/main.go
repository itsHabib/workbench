// Command controlroom serves and snapshots the local portfolio operations dashboard.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

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
			return (&http.Server{Handler: handler}).Serve(listener)
		},
	})
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
	if err := flags.Parse(args); err != nil {
		return usageError("snapshot flags: %v", err)
	}
	if flags.NArg() != 0 || *mode != "demo" || !*jsonOutput {
		return usageError("snapshot requires --mode demo --json")
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(demo.Snapshot()); err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	return nil
}

func runServe(args []string, stdout, stderr io.Writer, deps dependencies) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	mode := flags.String("mode", "", "data mode")
	addr := flags.String("addr", defaultAddr, "listen address")
	if err := flags.Parse(args); err != nil {
		return usageError("serve flags: %v", err)
	}
	if flags.NArg() != 0 || *mode != "demo" {
		return usageError("serve requires --mode demo")
	}
	if err := validateAddr(*addr); err != nil {
		return usageError("invalid --addr: %v", err)
	}
	listener, err := deps.listen("tcp4", *addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	host := listener.Addr().String()
	publisher := newDemoPublisher()
	handler, err := web.New(web.Config{Host: host, Snapshot: publisher.snapshot, Refresh: publisher.refresh})
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

func validateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		return fmt.Errorf("host must be IPv4 loopback 127.0.0.1")
	}
	if port == "" || strings.Contains(port, "+") {
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
