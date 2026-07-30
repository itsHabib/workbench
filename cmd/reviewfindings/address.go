package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/itsHabib/workbench/contracts/reviewfindings"
	"github.com/itsHabib/workbench/driverstate"
)

type addressOptions struct {
	run       string
	stream    string
	work      string
	artifact  string
	decision  string
	actor     string
	task      string
	head      string
	maxCycles int
}

func runAddress(ctx context.Context, args []string, runner commandRunner, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: reviewfindings address accept|claim|started|completed|resume")
		return exitError
	}
	dir, _, err := driverstate.StateRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	var code int
	switch args[0] {
	case "accept":
		code = addressAccept(ctx, dir, args[1:], runner, stdout, stderr)
	case "claim":
		code = addressClaim(dir, args[1:], stdout, stderr)
	case "started":
		code = addressStarted(dir, args[1:], stdout, stderr)
	case "completed":
		code = addressCompleted(ctx, dir, args[1:], runner, stdout, stderr)
	case "resume":
		code = addressResume(ctx, dir, args[1:], runner, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown address command %q\n", args[0])
		code = exitError
	}
	return code
}

func addressAccept(ctx context.Context, dir string, args []string, runner commandRunner, stdout, stderr io.Writer) int {
	opts, err := parseAddressOptions("accept", args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	data, err := os.ReadFile(opts.artifact)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	artifact, err := reviewfindings.Decode(data)
	if err != nil {
		return writeAddressError(driverstate.AddressRefusal{Code: "malformed-artifact", Message: err.Error()}, stderr)
	}
	decision, err := readAddressDecision(opts.decision)
	if err != nil {
		return writeAddressError(driverstate.AddressRefusal{Code: "malformed-decision", Message: err.Error()}, stderr)
	}
	if err := validateAddressDecision(decision, artifact); err != nil {
		return writeAddressError(err, stderr)
	}
	liveHead, state, err := fetchAddressPR(ctx, runner, artifact.Subject.Repo, artifact.Subject.Number)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	if !strings.EqualFold(state, "OPEN") {
		return writeAddressError(driverstate.AddressRefusal{Code: "pr-not-open", Message: "pull request is " + state}, stderr)
	}
	lease, err := driverstate.Claim(dir, opts.run, opts.actor)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	defer lease.Release()
	work, event, err := driverstate.PrepareReviewAddress(dir, lease, driverstate.PrepareAddressInput{
		Stream: opts.stream, LiveHead: strings.ToLower(liveHead),
		MaxCycles: opts.maxCycles, ExpectedCycle: decision.Cycle, Artifact: artifact,
	})
	if err != nil {
		return writeAddressError(err, stderr)
	}
	return writeAddressJSON(stdout, map[string]any{
		"work": work, "event": event.ID,
	})
}

func addressClaim(dir string, args []string, stdout, stderr io.Writer) int {
	opts, err := parseAddressOptions("claim", args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	lease, err := driverstate.Claim(dir, opts.run, opts.actor)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	defer lease.Release()
	child, event, err := driverstate.ClaimReviewAddress(dir, lease, opts.stream, opts.work, opts.actor)
	if err != nil {
		return writeAddressError(err, stderr)
	}
	return writeAddressJSON(stdout, map[string]any{"work_id": opts.work, "child_run": child, "event": event.ID})
}

func addressStarted(dir string, args []string, stdout, stderr io.Writer) int {
	opts, err := parseAddressOptions("started", args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	lease, err := driverstate.Claim(dir, opts.run, opts.actor)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	defer lease.Release()
	event, err := driverstate.StartReviewAddress(dir, lease, opts.stream, opts.work, opts.task)
	if err != nil {
		return writeAddressError(err, stderr)
	}
	return writeAddressJSON(stdout, map[string]any{"work_id": opts.work, "task_id": opts.task, "event": event.ID})
}

func addressCompleted(ctx context.Context, dir string, args []string, runner commandRunner, stdout, stderr io.Writer) int {
	opts, err := parseAddressOptions("completed", args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	resume, err := driverstate.ResumeReviewAddress(dir, opts.run, opts.stream, opts.work)
	if err != nil {
		return writeAddressError(err, stderr)
	}
	liveHead, state, err := fetchAddressPR(ctx, runner, resume.Work.Artifact.Subject.Repo, resume.Work.Artifact.Subject.Number)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	if !strings.EqualFold(state, "OPEN") || !strings.EqualFold(liveHead, opts.head) {
		return writeAddressError(driverstate.AddressRefusal{
			Code: "stale-head", Message: fmt.Sprintf("requested completed head %s is not the live open PR head %s", opts.head, liveHead),
		}, stderr)
	}
	lease, err := driverstate.Claim(dir, opts.run, opts.actor)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	defer lease.Release()
	event, err := driverstate.CompleteReviewAddress(dir, lease, opts.stream, opts.work, strings.ToLower(opts.head))
	if err != nil {
		return writeAddressError(err, stderr)
	}
	return writeAddressJSON(stdout, map[string]any{"work_id": opts.work, "head_sha": strings.ToLower(opts.head), "event": event.ID})
}

func addressResume(ctx context.Context, dir string, args []string, runner commandRunner, stdout, stderr io.Writer) int {
	opts, err := parseAddressOptions("resume", args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	resume, err := driverstate.ResumeReviewAddress(dir, opts.run, opts.stream, opts.work)
	if err != nil && resume.Work.WorkID == "" {
		return writeAddressError(err, stderr)
	}
	liveHead, state, fetchErr := fetchAddressPR(ctx, runner, resume.Work.Artifact.Subject.Repo, resume.Work.Artifact.Subject.Number)
	if fetchErr != nil {
		fmt.Fprintln(stderr, fetchErr)
		return exitError
	}
	reconciliation := "diverged"
	if strings.EqualFold(liveHead, resume.Work.SourceHeadSHA) {
		reconciliation = "source-head"
	}
	if resume.State.ResultHeadSHA != "" && strings.EqualFold(liveHead, resume.State.ResultHeadSHA) {
		reconciliation = "result-head"
	}
	output := map[string]any{
		"address": resume, "live_head": liveHead, "pr_state": state,
		"reconciliation": reconciliation,
	}
	if writeAddressJSON(stdout, output) != exitOK {
		return exitError
	}
	if err != nil {
		return writeAddressError(err, stderr)
	}
	if !strings.EqualFold(state, "OPEN") {
		return writeAddressError(driverstate.AddressRefusal{Code: "pr-not-open", Message: "pull request is " + state}, stderr)
	}
	if resume.State.Status == "pending" && reconciliation != "source-head" {
		return writeAddressError(driverstate.AddressRefusal{
			Code: "stale-head", Message: "pending work no longer matches the live PR head", WorkRef: resume.State.WorkRef,
		}, stderr)
	}
	if resume.State.Status == "completed" && reconciliation != "result-head" {
		return writeAddressError(driverstate.AddressRefusal{
			Code: "stale-head", Message: "completed work no longer matches the recorded result head", WorkRef: resume.State.WorkRef,
		}, stderr)
	}
	return exitOK
}

func parseAddressOptions(command string, args []string) (addressOptions, error) {
	var opts addressOptions
	flags := flag.NewFlagSet("address "+command, flag.ContinueOnError)
	flags.SetOutput(&bytes.Buffer{})
	flags.StringVar(&opts.run, "run", "", "authoritative implementation child run")
	flags.StringVar(&opts.stream, "stream", "", "implementation stream")
	flags.StringVar(&opts.work, "work", "", "address work id")
	flags.StringVar(&opts.artifact, "artifact", "", "ReviewFindingsV1 path")
	flags.StringVar(&opts.decision, "decision", "", "ReviewDecisionV1 path authorizing accepted findings")
	flags.StringVar(&opts.actor, "actor", "session:codex", "ledger actor")
	flags.StringVar(&opts.task, "task", "", "Codex task/thread id")
	flags.StringVar(&opts.head, "head", "", "exact completed PR head")
	flags.IntVar(&opts.maxCycles, "max-cycles", 0, "engine-owned review cycle ceiling")
	if err := flags.Parse(args); err != nil {
		return addressOptions{}, err
	}
	switch {
	case opts.run == "":
		return addressOptions{}, errors.New("-run is required")
	case opts.stream == "":
		return addressOptions{}, errors.New("-stream is required")
	case opts.actor == "":
		return addressOptions{}, errors.New("-actor is required")
	case command == "accept" && opts.artifact == "":
		return addressOptions{}, errors.New("-artifact is required")
	case command == "accept" && opts.decision == "":
		return addressOptions{}, errors.New("-decision is required")
	case command == "accept" && opts.maxCycles < 1:
		return addressOptions{}, errors.New("-max-cycles must be positive")
	case command != "accept" && opts.work == "":
		return addressOptions{}, errors.New("-work is required")
	case command == "started" && opts.task == "":
		return addressOptions{}, errors.New("-task is required")
	case command == "completed" && opts.head == "":
		return addressOptions{}, errors.New("-head is required")
	}
	return opts, nil
}

func fetchAddressPR(ctx context.Context, runner commandRunner, repo string, pr int) (string, string, error) {
	data, err := runner.Run(ctx, "gh", "pr", "view", strconv.Itoa(pr), "-R", repo, "--json", "headRefOid,state")
	if err != nil {
		return "", "", fmt.Errorf("read pull request: %w", err)
	}
	var result pullRequest
	if err := json.Unmarshal(data, &result); err != nil {
		return "", "", fmt.Errorf("decode pull request: %w", err)
	}
	return result.HeadRefOID, result.State, nil
}

func writeAddressError(err error, stderr io.Writer) int {
	fmt.Fprintln(stderr, err)
	var parked driverstate.AddressParked
	if errors.As(err, &parked) {
		return exitParked
	}
	var refused driverstate.AddressRefusal
	if errors.As(err, &refused) {
		return exitRefused
	}
	return exitError
}

func writeAddressJSON(stdout io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return exitError
	}
	return exitOK
}
