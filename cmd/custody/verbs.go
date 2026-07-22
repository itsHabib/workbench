package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/itsHabib/workbench/cmd/custody/internal/credstore"
	"github.com/itsHabib/workbench/cmd/custody/internal/grant"
	"github.com/itsHabib/workbench/cmd/custody/internal/manifest"
	"github.com/itsHabib/workbench/cmd/custody/internal/serve"
)

// cmdKeys dispatches the `keys` verb. v0 carries one subcommand, `set`, which
// reads a secret from stdin and writes it to the OS credential store via the
// mechanism #84 exposed. Import/list are later streams.
func cmdKeys(args []string) error {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, "usage: custody keys set -name <ref>   # secret read from stdin")
		return nil
	}
	if args[0] != "set" {
		return fmt.Errorf("keys: unknown subcommand %q (want: set)", args[0])
	}
	fs := flag.NewFlagSet("keys set", flag.ContinueOnError)
	name := fs.String("name", "", "secret reference to store (the ref after wincred:)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *name == "" {
		return errors.New("keys set: -name is required")
	}
	if err := credstore.KeysSet(newCredStore(), *name, os.Stdin); err != nil {
		return err
	}
	fmt.Printf("stored secret %q\n", *name)
	return nil
}

// cmdServe runs the localhost reverse proxy. It loads and validates the manifest
// (failing closed at startup), binds a loopback-only listener, and serves the
// engine. The listener refuses any non-loopback bind: the proxy holds real
// credentials and must never be reachable off the box (spec §6, NFR).
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8127", "loopback listen address")
	stateDir := fs.String("state", envOr("CUSTODY_STATE", defaultStateDir()), "state dir (manifest/grants/log) [env CUSTODY_STATE]")
	keyDir := fs.String("mint-key-dir", envOr("CUSTODY_KEY_DIR", defaultKeyDir()), "mint-key dir; a separate trust domain, must be outside -state [env CUSTODY_KEY_DIR]")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if err := requireLoopback(*addr); err != nil {
		return err
	}
	man, digest, err := loadManifest(filepath.Join(*stateDir, "manifest.json"))
	if err != nil {
		return err
	}
	grants, err := grant.NewStore(*stateDir, *keyDir)
	if err != nil {
		return err
	}
	logFile, err := openLog(*stateDir)
	if err != nil {
		return err
	}
	defer logFile.Close()
	engine, err := serve.New(serve.Config{
		Manifest:       man,
		ManifestDigest: digest,
		Grants:         grants,
		Secrets:        newCredStore(),
		LogWriter:      logFile,
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("serve: listen on %s: %w", *addr, err)
	}
	fmt.Fprintf(os.Stderr, "custody serve: listening on %s (state %s)\n", *addr, *stateDir)
	srv := &http.Server{Handler: engine, ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(listener)
}

// loadManifest reads the manifest bytes once — for the digest that pins which
// revision decided (spec §5) — then validates those same bytes. The digest and
// the enforced manifest come from one read, so a concurrent rewrite between two
// reads can never make the recorded manifest_digest disagree with the manifest
// the engine is actually enforcing. A bad manifest fails startup, never a request.
func loadManifest(path string) (*manifest.Manifest, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("serve: read manifest %s: %w", path, err)
	}
	man, err := manifest.Load(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return man, hex.EncodeToString(sum[:]), nil
}

// openLog opens (creating dirs) the append-only artifact log.
func openLog(stateDir string) (*os.File, error) {
	dir := filepath.Join(stateDir, "log")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("serve: log dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "requests.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("serve: open log: %w", err)
	}
	return f, nil
}

// requireLoopback refuses any bind address whose host is not loopback, so the
// credential proxy is never reachable off the box.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("serve: bad -addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("serve: -addr host %q must be loopback (127.0.0.1, ::1, or localhost)", host)
	}
	return nil
}
