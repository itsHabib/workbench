// Package server is the workbench-mcp stdio MCP server: it exposes the four
// driver-state verbs (driver_record / driver_state / driver_runs /
// driver_verify) over JSON-RPC 2.0 on stdin/stdout, and owns the run lease for
// the life of the client session.
//
// It is the client boundary of the driver-state plane. Mechanism (the ledger,
// leases, hash chain) lives in the driverstate package; this package is the
// transport + verb-dispatch policy over it, plus the session-lifetime lease
// lease renewal the plane's F2/F3 flows depend on. It imports at most
// driverstate + contracts — no other tool (charter boundary law).
package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/itsHabib/workbench/driverstate"
)

// maxLine bounds a single JSON-RPC message. MCP stdio framing is one JSON
// object per line, and a run_imported manifest snapshot can be large, so the
// scanner buffer is generous (the default 64 KiB would truncate a real import).
const maxLine = 8 << 20

// Server holds the resolved state root and the leases this session owns. One
// lease per run is claimed lazily on first driver_record and held (auto-renewed)
// until the session ends, so a session parked for hours on CI keeps its lease
// without any verb call (spec §6, review M2). The zero value is not usable —
// construct with New.
type Server struct {
	dir string

	mu     sync.Mutex
	leases map[string]driverstate.Lease
}

// New returns a Server bound to the already-resolved state root dir. Resolution
// (WORKBENCH_STATE_DIR, else user profile) and the startup print live in the
// command, so the root is decided once and identically for every surface.
func New(dir string) *Server {
	return &Server{dir: dir, leases: make(map[string]driverstate.Lease)}
}

// renewInterval is the auto-renew cadence: staleness-threshold / 2, so a lease
// is always heartbeated well within its own TTL window (spec §6, review M2). It
// reads the live DefaultLeaseTTL rather than caching it, so a test that tunes
// the TTL sees the matching cadence.
func (s *Server) renewInterval() time.Duration { return driverstate.DefaultLeaseTTL / 2 }

// Serve runs the read-dispatch-write loop over in/out until in reaches EOF (the
// client closed stdio) or ctx is cancelled. A background renewer keeps every
// held lease alive for the session; on exit the renewer stops and the leases are
// released, so an orphaned lease self-expires within one threshold window even
// if release is lost. Returns the scanner error, if any.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ticker := time.NewTicker(s.renewInterval())
	defer ticker.Stop()
	go s.renewLoop(ctx.Done(), ticker.C)

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		resp, respond := s.handleMessage(scanner.Bytes())
		if !respond {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			s.releaseAll()
			return fmt.Errorf("workbench-mcp: write response: %w", err)
		}
	}
	s.releaseAll()
	return scanner.Err()
}

// renewLoop heartbeats every held lease each tick until done closes. It is the
// session-lifetime keep-alive: production drives it from a ticker at
// renewInterval; a test drives ticks by hand to assert the cadence and that a
// closed session stops renewing.
func (s *Server) renewLoop(done <-chan struct{}, ticks <-chan time.Time) {
	for {
		select {
		case <-done:
			return
		case <-ticks:
			s.renewAll()
		}
	}
}

// renewAll renews each held lease. A lease lost from under this session
// (stolen, or expired past renewal) is dropped from the held set and reported to
// stderr — it will be re-Claimed on the next driver_record for that run.
func (s *Server) renewAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for run, l := range s.leases {
		if err := l.Renew(); err != nil {
			fmt.Fprintf(os.Stderr, "workbench-mcp: lease renew lost for run %s: %v\n", run, err)
			delete(s.leases, run)
		}
	}
}

// releaseAll drops every lease this session holds. Best-effort: a release error
// is irrelevant because a dropped lease self-expires within one TTL window
// regardless (spec §6 — server exit stops renewal).
func (s *Server) releaseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for run, l := range s.leases {
		_ = l.Release()
		delete(s.leases, run)
	}
}

// leaseFor returns the session's lease for run, claiming and caching one on
// first use. A live lease held by another writer surfaces ErrLocked{Holder}
// (spec §7 F4) — single-writer-per-run is the lease's promise.
func (s *Server) leaseFor(run, actor string) (driverstate.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.leases[run]; ok {
		return l, nil
	}
	l, err := driverstate.Claim(s.dir, run, actor)
	if err != nil {
		return driverstate.Lease{}, err
	}
	s.leases[run] = l
	return l, nil
}
