// Command escalate is the resolution back-channel of the Escalation plane: it
// ingests the decision a notification carried back for a parked escalation and
// drives gate's `resolve` verb to record it — closing the agent→human→agent
// loop WITHOUT flare (the router) ever writing a decision. It is the missing
// seam the plane formalizes: gate emits the park (push origin), flare routes it
// (Observability), and this component owns the resolution ingest that no plane
// owned before.
//
// It composes through gate's CLI seam — shelling the gate binary, never
// importing it — exactly as the console composes through gate's read seam, so
// the workbench boundary law holds (share artifacts + exit codes, not call
// stacks). The exit code is a faithful pass-through of `gate resolve`'s
// (0 merge / 1 blocked / 2 parked / 3 refused / 4 error); an escalate-side
// failure to even reach gate — a malformed decision, gate not runnable — exits
// 5, outside gate's code space so the two never collide.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/escalate/internal/ingest"
	"github.com/itsHabib/workbench/cmd/escalate/internal/serve"
)

// codeIngestError is escalate's own failure code, deliberately outside gate's
// 0–4 decision space so a caller can tell "escalate could not ingest" apart
// from any decision gate returned.
const codeIngestError = 5

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(codeIngestError)
	}
	switch os.Args[1] {
	case "resolve":
		if err := cmdResolve(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "escalate:", err)
			os.Exit(codeIngestError)
		}
	case "serve":
		if err := cmdServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "escalate:", err)
			os.Exit(codeIngestError)
		}
	default:
		usage()
		os.Exit(codeIngestError)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  escalate resolve -escalation esc_x -decision pass|block -grant grt_x -why "..." -who NAME [-gate gate] [-state DIR]
    ingests a human's decision for a parked escalation and drives `+"`gate resolve`"+`.
  escalate serve [-addr 127.0.0.1:8099] [-gate gate] [-state DIR]
    runs the Slack interactive-action ingress. SLACK_SIGNING_SECRET must be set
    (authenticates the callback) and ESCALATE_ALLOWED_SLACK_USERS must list the
    Slack user id(s) allowed to resolve (comma-separated; authorizes the tapper).
  exit code is gate resolve's (0/1/2/3/4); an ingest-side failure exits 5.
  -state defaults to $GATE_STATE; -gate defaults to the gate binary on PATH.`)
}

func cmdResolve(args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	gateBin := fs.String("gate", "gate", "path to the gate binary")
	stateDir := fs.String("state", os.Getenv("GATE_STATE"), "gate state dir (default $GATE_STATE)")
	escID := fs.String("escalation", "", "the escalation artifact id a notification carried")
	decision := fs.String("decision", "", "pass or block")
	grantID := fs.String("grant", "", "grant artifact id")
	why := fs.String("why", "", "the decision's reasoning")
	who := fs.String("who", "", "who decided — provenance for the resolution stamp")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	c := ingest.New(*gateBin, *stateDir, nil)
	out, code, err := c.Resolve(context.Background(), ingest.Decision{
		Escalation: *escID,
		Verdict:    *decision,
		Who:        *who,
		Why:        *why,
		Grant:      *grantID,
	})
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(out); err != nil {
		return err
	}
	os.Exit(code)
	return nil
}

// cmdServe runs the Slack interactive-action ingress: the same ingest mechanism
// as resolve, fed by a signed HTTP callback instead of CLI flags. The signing
// secret comes from SLACK_SIGNING_SECRET and is REQUIRED — an ingress with no
// secret would accept forged decisions, so serve refuses to start without one
// rather than expose an unauthenticated endpoint.
func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8099", "listen address (loopback by default; a tunnel exposes it)")
	gateBin := fs.String("gate", "gate", "path to the gate binary")
	stateDir := fs.String("state", os.Getenv("GATE_STATE"), "gate state dir (default $GATE_STATE)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	secret := os.Getenv("SLACK_SIGNING_SECRET")
	if secret == "" {
		return errors.New("serve: SLACK_SIGNING_SECRET is required (refusing to run an unauthenticated ingress)")
	}
	// Authorization is required too, and for the same reason: a signed callback
	// only proves Slack sent it, not that the tapper may move a merge gate. Without
	// an allowlist any member of the escalation channel could resolve a park, so
	// serve refuses to start with no authorized users rather than expose a
	// channel-wide control. Ids are the immutable Slack `Uxxxx`, comma-separated.
	allowed := splitList(os.Getenv("ESCALATE_ALLOWED_SLACK_USERS"))
	if len(allowed) == 0 {
		return errors.New("serve: ESCALATE_ALLOWED_SLACK_USERS is required (a comma-separated allowlist of Slack user ids; refusing to run an ingress any channel member could resolve)")
	}
	handler := serve.New(serve.Config{
		Secret:    []byte(secret),
		Ingest:    ingest.New(*gateBin, *stateDir, nil),
		FindGrant: serve.GateGrantFinder(*gateBin, *stateDir),
		Authorize: serve.AllowUsers(allowed...),
	})
	// This listener faces a public tunnel, and signature verification only runs
	// after the body is read — so an unauthenticated slow client could otherwise
	// hold connections open indefinitely and exhaust goroutines before auth ever
	// runs. Bounded read/write timeouts cap that. The handler now acks within
	// Slack's ~3s window and runs `gate resolve` in the background (delivering the
	// outcome to response_url), so the response itself is fast — a tight
	// WriteTimeout is enough; the detached resolve has its own bound in serve.
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	log.Printf("escalate serve: listening on %s (gate=%s state=%q, %d authorized user(s))", *addr, *gateBin, *stateDir, len(allowed))
	return srv.ListenAndServe()
}

// splitList parses a comma-separated env list into trimmed, non-empty entries,
// so " U1 , , U2 " yields [U1 U2] and an unset or blank var yields none (which
// the caller treats as fail-closed).
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
