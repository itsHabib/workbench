package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/jobs"
)

// runJobs bypasses the legacy mutating router: reading a queue never ticks a watcher.
func runJobs(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: fleet job <submit|list|get|claim|renew|complete|accept|retry|metrics|serve> [flags]")
		return 2
	}
	fs := flag.NewFlagSet("fleet job "+args[0], flag.ContinueOnError)
	var req jobs.Request
	req.Op = args[0]
	dir := fs.String("state", filepath.Join(fleet.State, "jobs"), "job store directory")
	fs.StringVar(&req.ID, "id", "", "stable job ID")
	fs.StringVar(&req.Key, "key", "", "unique claim retry key (reuse after response loss)")
	fs.StringVar(&req.Brief, "brief", "", "work and acceptance criteria")
	fs.StringVar(&req.Worker, "worker", "", "stable worker identity")
	fs.StringVar(&req.Token, "token", "", "current attempt token")
	fs.StringVar(&req.Result, "result", "", "artifact reference/result JSON text")
	fs.StringVar(&req.Evidence, "evidence", "", "acceptance evidence or retry reason")
	fs.IntVar(&req.TTLSeconds, "ttl", 300, "lease duration in seconds, maximum 3600")
	addr := fs.String("listen", "127.0.0.1:7381", "loopback HTTP address")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		return 2
	}
	store := &jobs.Store{Dir: *dir}
	if req.Op == "serve" {
		return serveJobs(store, *addr)
	}
	result, err := store.Execute(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func serveJobs(store *jobs.Store, addr string) int {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		fmt.Fprintln(os.Stderr, "job API requires a numeric loopback listen address")
		return 2
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "Fleet job API:", listener.Addr())
	server := &http.Server{Handler: jobHandler(store), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	if err = server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func jobHandler(store *jobs.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No browser authority: reject cross-origin requests and DNS rebinding hosts.
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if ip == nil || !ip.IsLoopback() || r.Header.Get("Origin") != "" {
			jobHTTPError(w, "loopback non-browser clients only", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/v1/jobs" {
			jobHTTPError(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			jobHTTPError(w, "use POST with a job operation", http.StatusMethodNotAllowed)
			return
		}
		if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
			jobHTTPError(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		var req jobs.Request
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			jobHTTPError(w, "invalid job request: "+err.Error(), http.StatusBadRequest)
			return
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			jobHTTPError(w, "expected one JSON request", http.StatusBadRequest)
			return
		}
		result, err := store.Execute(req)
		if err != nil {
			writeJobError(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})
}

func writeJobError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, jobs.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, jobs.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, jobs.ErrConflict):
		status = http.StatusConflict
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func jobHTTPError(w http.ResponseWriter, message string, status int) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
