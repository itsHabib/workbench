// Package app composes source adapters into immutable Control Room snapshots.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/policy"
)

const (
	defaultCoreTimeout       = 15 * time.Second
	defaultEnrichmentTimeout = 35 * time.Second
)

var sourceOrder = []string{"ship", "dossier", "github", "tower", "tracelens", "toolhealth"}

// Result is one source-owned observation before cross-source policy is applied.
type Result struct {
	Receipt      model.SourceReceipt
	Runs         []model.Run
	Tasks        []model.Task
	PullRequests []model.PullRequest
	Reliability  []model.Diagnosis
	ToolHealth   []model.ToolHealth
}

// Collectors are the narrow source seams used by the coordinator.
type Collectors struct {
	Ship       func(context.Context) Result
	Dossier    func(context.Context, bool) Result
	GitHub     func(context.Context) Result
	Tower      func(context.Context) Result
	Tracelens  func(context.Context, []model.Run, model.SourceReceipt) Result
	ToolHealth func(context.Context) Result
}

// Config fixes process-local publication identity and deadlines.
type Config struct {
	Mode              string
	Fingerprint       string
	CoreTimeout       time.Duration
	EnrichmentTimeout time.Duration
	Now               func() time.Time
	Collectors        Collectors
}

// RefreshReceipt identifies the fixed baseline of an asynchronous collection.
type RefreshReceipt struct {
	BaselineVersion uint64
	Status          string
}

type sourcePayload struct {
	receipt     model.SourceReceipt
	runs        []model.Run
	tasks       []model.Task
	prs         []model.PullRequest
	reliability []model.Diagnosis
	toolHealth  []model.ToolHealth
}

type flight struct {
	epoch    uint64
	identity string
	baseline uint64
	cancel   context.CancelFunc
	done     chan struct{}
}

// Coordinator owns refresh joining, epoch gates, stale retention, and publication.
type Coordinator struct {
	mode        string
	fingerprint string
	coreTimeout time.Duration
	enrichTime  time.Duration
	now         func() time.Time
	collectors  Collectors

	root       context.Context
	cancelRoot context.CancelFunc
	closeOnce  sync.Once
	snapshot   atomic.Pointer[model.Snapshot]

	mu          sync.Mutex
	version     uint64
	activeEpoch uint64
	inFlight    *flight
	lastCurrent map[string]sourcePayload
	closed      bool
}

// New constructs a real-mode coordinator and publishes its version-zero bootstrap.
func New(config Config) (*Coordinator, error) {
	if config.Mode != "real" || config.Fingerprint == "" {
		return nil, fmt.Errorf("real mode and a configuration fingerprint are required")
	}
	if config.Collectors.Ship == nil || config.Collectors.Dossier == nil || config.Collectors.GitHub == nil ||
		config.Collectors.Tower == nil || config.Collectors.Tracelens == nil || config.Collectors.ToolHealth == nil {
		return nil, fmt.Errorf("all source collectors are required")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	coreTimeout := config.CoreTimeout
	if coreTimeout <= 0 {
		coreTimeout = defaultCoreTimeout
	}
	enrichTime := config.EnrichmentTimeout
	if enrichTime <= 0 {
		enrichTime = defaultEnrichmentTimeout
	}
	root, cancel := context.WithCancel(context.Background())
	c := &Coordinator{
		mode: config.Mode, fingerprint: config.Fingerprint, coreTimeout: coreTimeout,
		enrichTime: enrichTime, now: now, collectors: config.Collectors,
		root: root, cancelRoot: cancel, lastCurrent: make(map[string]sourcePayload),
	}
	bootstrap := model.Snapshot{Version: 0, Sources: make([]model.SourceReceipt, 0, len(sourceOrder))}
	for _, source := range sourceOrder {
		bootstrap.Sources = append(bootstrap.Sources, loadingReceipt(source, now()))
	}
	c.snapshot.Store(&bootstrap)
	return c, nil
}

// Snapshot returns a deep copy of the latest immutable publication.
func (c *Coordinator) Snapshot() model.Snapshot {
	value := c.snapshot.Load()
	if value == nil {
		return model.Snapshot{}
	}
	return cloneSnapshot(*value)
}

// Refresh starts or joins an asynchronous collection.
func (c *Coordinator) Refresh(trigger string) (RefreshReceipt, error) {
	receipt, _, err := c.start(trigger)
	return receipt, err
}

// Collect starts or joins a collection and waits for its enrichment rendezvous.
func (c *Coordinator) Collect(ctx context.Context, trigger string) (model.Snapshot, error) {
	_, current, err := c.start(trigger)
	if err != nil {
		return model.Snapshot{}, err
	}
	select {
	case <-ctx.Done():
		return model.Snapshot{}, ctx.Err()
	case <-current.done:
		return c.Snapshot(), nil
	}
}

// Close cancels the active epoch and prevents further useful publication.
func (c *Coordinator) Close() {
	c.closeOnce.Do(func() {
		c.cancelRoot()
		c.mu.Lock()
		c.closed = true
		if c.inFlight != nil {
			c.inFlight.cancel()
		}
		c.activeEpoch++
		c.mu.Unlock()
	})
}

func (c *Coordinator) start(trigger string) (RefreshReceipt, *flight, error) {
	if trigger != "manual" && trigger != "auto" {
		return RefreshReceipt{}, nil, fmt.Errorf("trigger must be manual or auto")
	}
	identity := refreshIdentity(c.mode, trigger, c.fingerprint)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return RefreshReceipt{}, nil, fmt.Errorf("coordinator is closed")
	}
	if c.inFlight != nil && c.inFlight.identity == identity {
		current := c.inFlight
		receipt := RefreshReceipt{BaselineVersion: current.baseline, Status: "joined"}
		c.mu.Unlock()
		return receipt, current, nil
	}
	if c.inFlight != nil {
		c.inFlight.cancel()
	}
	c.activeEpoch++
	ctx, cancel := context.WithCancel(c.root)
	current := &flight{
		epoch: c.activeEpoch, identity: identity, baseline: c.version,
		cancel: cancel, done: make(chan struct{}),
	}
	c.inFlight = current
	c.mu.Unlock()
	go c.collect(ctx, current, trigger == "manual")
	return RefreshReceipt{BaselineVersion: current.baseline, Status: "started"}, current, nil
}

func refreshIdentity(mode, trigger, fingerprint string) string {
	sum := sha256.Sum256([]byte(mode + "\x00" + trigger + "\x00" + fingerprint))
	return hex.EncodeToString(sum[:])
}

func (c *Coordinator) collect(ctx context.Context, current *flight, manual bool) {
	defer func() {
		current.cancel()
		c.mu.Lock()
		if c.inFlight == current {
			c.inFlight = nil
		}
		close(current.done)
		c.mu.Unlock()
	}()
	coreResults := c.collectCore(ctx, manual)
	core, updates := c.composeCore(coreResults)
	published, ok := c.publish(current.epoch, core, updates)
	if !ok {
		return
	}
	enrichment := c.collectEnrichment(ctx, published)
	final, updates := c.composeEnrichment(published, enrichment)
	_, _ = c.publish(current.epoch, final, updates)
}

type namedResult struct {
	source string
	result Result
}

func (c *Coordinator) collectCore(parent context.Context, manual bool) map[string]Result {
	ctx, cancel := context.WithTimeout(parent, c.coreTimeout)
	defer cancel()
	results := make(chan namedResult, 4)
	launch := func(source string, collect func(context.Context) Result) {
		go func() { results <- namedResult{source: source, result: safeCollect(ctx, source, c.now, collect)} }()
	}
	launch("ship", c.collectors.Ship)
	launch("dossier", func(ctx context.Context) Result { return c.collectors.Dossier(ctx, manual) })
	launch("github", c.collectors.GitHub)
	launch("tower", c.collectors.Tower)
	return awaitResults(ctx, results, []string{"ship", "dossier", "github", "tower"}, c.now)
}

func (c *Coordinator) collectEnrichment(parent context.Context, core model.Snapshot) map[string]Result {
	ctx, cancel := context.WithTimeout(parent, c.enrichTime)
	defer cancel()
	results := make(chan namedResult, 2)
	var rendezvous sync.WaitGroup
	rendezvous.Add(2)
	shipReceipt := currentReceipt(core.Sources, "ship")
	go func() {
		defer rendezvous.Done()
		collect := func(ctx context.Context) Result { return c.collectors.Tracelens(ctx, core.Runs, shipReceipt) }
		results <- namedResult{source: "tracelens", result: safeCollect(ctx, "tracelens", c.now, collect)}
	}()
	go func() {
		defer rendezvous.Done()
		results <- namedResult{source: "toolhealth", result: safeCollect(ctx, "toolhealth", c.now, c.collectors.ToolHealth)}
	}()
	output := awaitResults(ctx, results, []string{"tracelens", "toolhealth"}, c.now)
	if ctx.Err() == nil {
		rendezvous.Wait()
	}
	return output
}

func awaitResults(ctx context.Context, input <-chan namedResult, sources []string, now func() time.Time) map[string]Result {
	output := make(map[string]Result, len(sources))
	for len(output) < len(sources) {
		select {
		case value := <-input:
			if _, expected := output[value.source]; !expected {
				output[value.source] = normalizeResult(value.source, value.result, now())
			}
		case <-ctx.Done():
			for _, source := range sources {
				if _, ok := output[source]; !ok {
					output[source] = unavailable(source, now(), "deadline_exceeded", "Source collection exceeded its deadline")
				}
			}
		}
	}
	return output
}

func safeCollect(ctx context.Context, source string, now func() time.Time, collect func(context.Context) Result) (result Result) {
	defer func() {
		if recover() != nil {
			result = unavailable(source, now(), "collector_panicked", "Source collector failed safely")
		}
	}()
	return collect(ctx)
}

func normalizeResult(source string, result Result, observed time.Time) Result {
	result.Receipt.Source = source
	if result.Receipt.ObservedAt.IsZero() {
		result.Receipt.ObservedAt = observed
	}
	if result.Receipt.State == model.SourceOK || result.Receipt.State == model.SourceDegraded || result.Receipt.State == model.SourceUnavailable {
		return result
	}
	return unavailable(source, observed, "invalid_receipt", "Source returned an invalid receipt")
}

func (c *Coordinator) composeCore(results map[string]Result) (model.Snapshot, map[string]sourcePayload) {
	snapshot := model.Snapshot{Mode: c.mode, GeneratedAt: c.now()}
	updates := make(map[string]sourcePayload)
	cache := c.cachedPayloads()
	for _, source := range []string{"ship", "dossier", "github", "tower"} {
		applyResult(&snapshot, source, results[source], cache[source], updates)
	}
	for _, source := range []string{"tracelens", "toolhealth"} {
		snapshot.Sources = append(snapshot.Sources, loadingReceipt(source, c.now()))
		if retained, ok := cache[source]; ok {
			appendPayload(&snapshot, retained)
			snapshot.Sources = append(snapshot.Sources, staleReceipt(retained.receipt))
		}
	}
	finishSnapshot(&snapshot)
	return snapshot, updates
}

func (c *Coordinator) composeEnrichment(core model.Snapshot, results map[string]Result) (model.Snapshot, map[string]sourcePayload) {
	snapshot := cloneSnapshot(core)
	snapshot.GeneratedAt = c.now()
	snapshot.Sources = withoutSources(snapshot.Sources, "tracelens", "toolhealth")
	snapshot.Reliability = nil
	snapshot.ToolHealth = nil
	updates := make(map[string]sourcePayload)
	cache := c.cachedPayloads()
	for _, source := range []string{"tracelens", "toolhealth"} {
		applyResult(&snapshot, source, results[source], cache[source], updates)
	}
	finishSnapshot(&snapshot)
	return snapshot, updates
}

func applyResult(snapshot *model.Snapshot, source string, result Result, retained sourcePayload, updates map[string]sourcePayload) {
	result = normalizeResult(source, result, snapshot.GeneratedAt)
	snapshot.Sources = append(snapshot.Sources, result.Receipt)
	if result.Receipt.State == model.SourceOK || result.Receipt.State == model.SourceDegraded {
		payload := payloadFromResult(result)
		appendPayload(snapshot, payload)
		updates[source] = payload
		return
	}
	if retained.receipt.Source != "" {
		appendPayload(snapshot, retained)
		snapshot.Sources = append(snapshot.Sources, staleReceipt(retained.receipt))
	}
}

func finishSnapshot(snapshot *model.Snapshot) {
	repositories := make(map[string]struct{})
	for _, run := range snapshot.Runs {
		if run.Repository != "" {
			repositories[run.Repository] = struct{}{}
		}
	}
	for _, pr := range snapshot.PullRequests {
		if pr.Repository != "" {
			repositories[pr.Repository] = struct{}{}
		}
	}
	snapshot.Repositories = snapshot.Repositories[:0]
	for repository := range repositories {
		snapshot.Repositories = append(snapshot.Repositories, repository)
	}
	sort.Strings(snapshot.Repositories)
}

func (c *Coordinator) publish(epoch uint64, candidate model.Snapshot, updates map[string]sourcePayload) (model.Snapshot, bool) {
	now := c.now()
	candidate = policy.ApplyPolicy(candidate, now)
	candidate.GeneratedAt = now
	c.mu.Lock()
	defer c.mu.Unlock()
	if epoch != c.activeEpoch {
		return model.Snapshot{}, false
	}
	c.version++
	candidate.Version = c.version
	stored := cloneSnapshot(candidate)
	c.snapshot.Store(&stored)
	for source, payload := range updates {
		c.lastCurrent[source] = clonePayload(payload)
	}
	return cloneSnapshot(stored), true
}

func (c *Coordinator) cachedPayloads() map[string]sourcePayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	values := make(map[string]sourcePayload, len(c.lastCurrent))
	for source, payload := range c.lastCurrent {
		values[source] = clonePayload(payload)
	}
	return values
}

func payloadFromResult(result Result) sourcePayload {
	return clonePayload(sourcePayload{
		receipt: result.Receipt, runs: result.Runs, tasks: result.Tasks, prs: result.PullRequests,
		reliability: result.Reliability, toolHealth: result.ToolHealth,
	})
}

func appendPayload(snapshot *model.Snapshot, payload sourcePayload) {
	snapshot.Runs = append(snapshot.Runs, payload.runs...)
	snapshot.Tasks = append(snapshot.Tasks, payload.tasks...)
	snapshot.PullRequests = append(snapshot.PullRequests, payload.prs...)
	snapshot.Reliability = append(snapshot.Reliability, payload.reliability...)
	snapshot.ToolHealth = append(snapshot.ToolHealth, payload.toolHealth...)
}

func clonePayload(payload sourcePayload) sourcePayload {
	cloned := cloneSnapshot(model.Snapshot{
		Runs: payload.runs, Tasks: payload.tasks, PullRequests: payload.prs,
		Reliability: payload.reliability, ToolHealth: payload.toolHealth,
	})
	payload.runs, payload.tasks, payload.prs = cloned.Runs, cloned.Tasks, cloned.PullRequests
	payload.reliability, payload.toolHealth = cloned.Reliability, cloned.ToolHealth
	return payload
}

func unavailable(source string, observed time.Time, code, message string) Result {
	return Result{Receipt: model.SourceReceipt{
		Source: source, State: model.SourceUnavailable, ObservedAt: observed,
		ErrorCode: code, Message: message,
	}}
}

func loadingReceipt(source string, observed time.Time) model.SourceReceipt {
	return model.SourceReceipt{Source: source, State: model.SourceLoading, ObservedAt: observed}
}

func staleReceipt(current model.SourceReceipt) model.SourceReceipt {
	return model.SourceReceipt{
		Source: current.Source, State: model.SourceStale, ObservedAt: current.ObservedAt,
		DurationMS: current.DurationMS, ErrorCode: "retained", Message: "Retained from the last current observation",
	}
}

func currentReceipt(receipts []model.SourceReceipt, source string) model.SourceReceipt {
	for _, receipt := range receipts {
		if receipt.Source == source && receipt.State != model.SourceStale {
			return receipt
		}
	}
	return model.SourceReceipt{Source: source, State: model.SourceUnavailable}
}

func withoutSources(receipts []model.SourceReceipt, sources ...string) []model.SourceReceipt {
	removed := make(map[string]bool, len(sources))
	for _, source := range sources {
		removed[source] = true
	}
	result := make([]model.SourceReceipt, 0, len(receipts))
	for _, receipt := range receipts {
		if !removed[receipt.Source] {
			result = append(result, receipt)
		}
	}
	return result
}

func cloneSnapshot(value model.Snapshot) model.Snapshot {
	value.Sources = append([]model.SourceReceipt(nil), value.Sources...)
	value.Repositories = append([]string(nil), value.Repositories...)
	value.Runs = append([]model.Run(nil), value.Runs...)
	for i := range value.Runs {
		value.Runs[i].Evidence = append([]model.SafeLink(nil), value.Runs[i].Evidence...)
		value.Runs[i].DocPath = cloneAvailability(value.Runs[i].DocPath)
		value.Runs[i].SpecPath = cloneAvailability(value.Runs[i].SpecPath)
		value.Runs[i].StartedAt = cloneAvailability(value.Runs[i].StartedAt)
		value.Runs[i].EndedAt = cloneAvailability(value.Runs[i].EndedAt)
		value.Runs[i].DurationMS = cloneAvailability(value.Runs[i].DurationMS)
		value.Runs[i].Requested.Runtime = cloneAvailability(value.Runs[i].Requested.Runtime)
		value.Runs[i].Requested.Provider = cloneAvailability(value.Runs[i].Requested.Provider)
		value.Runs[i].Requested.Model = cloneAvailability(value.Runs[i].Requested.Model)
		value.Runs[i].Actual.Runtime = cloneAvailability(value.Runs[i].Actual.Runtime)
		value.Runs[i].Actual.Provider = cloneAvailability(value.Runs[i].Actual.Provider)
		value.Runs[i].Actual.Model = cloneAvailability(value.Runs[i].Actual.Model)
	}
	value.Tasks = append([]model.Task(nil), value.Tasks...)
	for i := range value.Tasks {
		value.Tasks[i].Dependencies = append([]string(nil), value.Tasks[i].Dependencies...)
		value.Tasks[i].Blockers = append([]string(nil), value.Tasks[i].Blockers...)
		value.Tasks[i].Artifacts = append([]model.SafeLink(nil), value.Tasks[i].Artifacts...)
	}
	value.PullRequests = append([]model.PullRequest(nil), value.PullRequests...)
	for i := range value.PullRequests {
		value.PullRequests[i].Checks = append([]model.Check(nil), value.PullRequests[i].Checks...)
		value.PullRequests[i].TruncatedConnections = append([]string(nil), value.PullRequests[i].TruncatedConnections...)
	}
	value.Reliability = append([]model.Diagnosis(nil), value.Reliability...)
	for i := range value.Reliability {
		value.Reliability[i].Findings = append([]model.Finding(nil), value.Reliability[i].Findings...)
		value.Reliability[i].Evidence = append([]model.SafeLink(nil), value.Reliability[i].Evidence...)
		value.Reliability[i].Report = cloneAvailability(value.Reliability[i].Report)
		value.Reliability[i].InputTokens = cloneAvailability(value.Reliability[i].InputTokens)
		value.Reliability[i].OutputTokens = cloneAvailability(value.Reliability[i].OutputTokens)
		value.Reliability[i].CostUSD = cloneAvailability(value.Reliability[i].CostUSD)
		value.Reliability[i].LatencyMS = cloneAvailability(value.Reliability[i].LatencyMS)
	}
	value.ToolHealth = append([]model.ToolHealth(nil), value.ToolHealth...)
	for i := range value.ToolHealth {
		value.ToolHealth[i].Pain = append([]string(nil), value.ToolHealth[i].Pain...)
	}
	value.Attention = append([]model.AttentionItem(nil), value.Attention...)
	for i := range value.Attention {
		value.Attention[i].Links = append([]model.SafeLink(nil), value.Attention[i].Links...)
		value.Attention[i].SupportingSources = append([]string(nil), value.Attention[i].SupportingSources...)
	}
	return value
}

func cloneAvailability[T any](value model.Availability[T]) model.Availability[T] {
	if value.Value != nil {
		cloned := *value.Value
		value.Value = &cloned
	}
	return value
}
