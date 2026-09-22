// Package jobs implements a single-host durable job ledger. A kernel lock
// serializes mutations; atomic snapshots let readers observe committed state.
// Tokens fence ledger updates, not processes or external side effects.
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/filelock"
)

// Request errors permit transport-specific status mapping.
var (
	ErrInvalid  = errors.New("invalid request")
	ErrConflict = errors.New("conflict")
	ErrNotFound = errors.New("not found")
)

// Store owns a single-host directory; Now supports deterministic tests.
type Store struct {
	Dir string
	Now func() time.Time
}

// Request is the command and HTTP operation envelope.
type Request struct {
	Op         string `json:"op"`
	ID         string `json:"id,omitempty"`
	Key        string `json:"key,omitempty"`
	Brief      string `json:"brief,omitempty"`
	Worker     string `json:"worker,omitempty"`
	Token      string `json:"token,omitempty"`
	Result     string `json:"result,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

// Attempt records an immutable claim identity and its evolving lease and result.
type Attempt struct {
	Token       string     `json:"token"`
	Worker      string     `json:"worker"`
	Key         string     `json:"key"`
	ClaimID     string     `json:"claim_id"`
	TTLSeconds  int        `json:"ttl_seconds"`
	StartedAt   time.Time  `json:"started_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Result      string     `json:"result,omitempty"`
	RetriedAt   *time.Time `json:"retried_at,omitempty"`
	Evidence    string     `json:"evidence,omitempty"`
}

// Job retains every attempt and the current acceptance state.
type Job struct {
	ID         string     `json:"id"`
	Brief      string     `json:"brief"`
	State      string     `json:"state"`
	CreatedAt  time.Time  `json:"created_at"`
	QueuedAt   time.Time  `json:"queued_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	Evidence   string     `json:"evidence,omitempty"`
	Attempts   []Attempt  `json:"attempts"`
}

// Metrics reports queue and attempt counts derived from a snapshot.
type Metrics struct {
	States                 map[string]int `json:"states"`
	ExpiredEligible        int            `json:"expired_eligible"`
	Attempts               int            `json:"attempts"`
	Retries                int            `json:"retries"`
	OldestQueueWaitSeconds float64        `json:"oldest_queue_wait_seconds"`
}
type snapshot struct {
	Version int   `json:"version"`
	Jobs    []Job `json:"jobs"`
}

// Execute reads or atomically applies one ledger operation.
func (s Store) Execute(r Request) (any, error) {
	if strings.TrimSpace(s.Dir) == "" {
		return nil, fmt.Errorf("%w: store directory required", ErrInvalid)
	}
	if r.Op == "list" || r.Op == "get" || r.Op == "metrics" {
		d, err := s.read()
		if err != nil {
			return nil, err
		}
		return d.read(r, s.now())
	}
	switch r.Op {
	case "submit", "claim", "renew", "complete", "accept", "retry":
	default:
		return nil, fmt.Errorf("%w: unknown operation", ErrInvalid)
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err = filelock.Lock(f); err != nil {
		return nil, err
	}
	defer filelock.Unlock(f)
	d, err := s.read()
	if err != nil {
		return nil, err
	}
	result, changed, err := d.apply(r, s.now())
	if err != nil {
		return nil, err
	}
	if changed {
		if err = s.save(d); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Store) read() (snapshot, error) {
	d := snapshot{Version: 1, Jobs: []Job{}}
	b, err := os.ReadFile(filepath.Join(s.Dir, "jobs.json"))
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	d = snapshot{}
	if err = json.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("corrupt job store: %w", err)
	}
	if err = d.validate(); err != nil {
		return d, fmt.Errorf("corrupt job store: %w", err)
	}
	return d, nil
}
func (d snapshot) validate() error {
	workers := map[string]bool{}
	if d.Version != 1 || d.Jobs == nil {
		return errors.New("invalid snapshot version or jobs")
	}
	ids, tokens, keys := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, j := range d.Jobs {
		if ids[j.ID] {
			return errors.New("duplicate job")
		}
		ids[j.ID] = true
		if err := validateJob(j); err != nil {
			return err
		}
		if j.State == "running" {
			w := j.Attempts[len(j.Attempts)-1].Worker
			if workers[w] {
				return errors.New("duplicate running worker")
			}
			workers[w] = true
		}
		for _, a := range j.Attempts {
			if tokens[a.Token] || keys[a.Key] {
				return errors.New("duplicate attempt")
			}
			tokens[a.Token], keys[a.Key] = true, true
		}
	}
	return nil
}
func validateJob(j Job) error {
	if strings.TrimSpace(j.ID) == "" || strings.TrimSpace(j.Brief) == "" || j.CreatedAt.IsZero() || j.QueuedAt.IsZero() || j.UpdatedAt.IsZero() || j.Attempts == nil {
		return errors.New("invalid job")
	}
	for _, a := range j.Attempts {
		if err := validateAttempt(a); err != nil {
			return err
		}
	}
	if j.State == "queued" && len(j.Attempts) == 0 {
		return nil
	}
	if len(j.Attempts) == 0 {
		return errors.New("missing attempt")
	}
	a := j.Attempts[len(j.Attempts)-1]
	return validateState(j, a)
}
func validateState(j Job, a Attempt) error {
	switch j.State {
	case "queued":
		if a.RetriedAt == nil {
			return errors.New("queued without retry")
		}
	case "running":
		if a.CompletedAt != nil || a.RetriedAt != nil {
			return errors.New("running terminal attempt")
		}
	case "reported", "accepted":
		if a.CompletedAt == nil || strings.TrimSpace(a.Result) == "" || a.RetriedAt != nil {
			return errors.New("invalid result")
		}
	default:
		return errors.New("invalid state")
	}
	if j.State == "accepted" && (j.AcceptedAt == nil || strings.TrimSpace(j.Evidence) == "") {
		return errors.New("invalid acceptance")
	}
	return nil
}
func validateAttempt(a Attempt) error {
	if a.Token == "" || strings.TrimSpace(a.Worker) == "" || strings.TrimSpace(a.Key) == "" || a.StartedAt.IsZero() || !a.ExpiresAt.After(a.StartedAt) || a.TTLSeconds < 1 || a.TTLSeconds > 3600 {
		return errors.New("invalid attempt")
	}
	if a.RetriedAt != nil && strings.TrimSpace(a.Evidence) == "" {
		return errors.New("missing retry evidence")
	}
	return nil
}
func (s Store) save(d snapshot) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".jobs-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return publishSnapshot(f.Name(), filepath.Join(s.Dir, "jobs.json"), s.Dir)
}
func (d snapshot) index(id string) int {
	for i := range d.Jobs {
		if d.Jobs[i].ID == id {
			return i
		}
	}
	return -1
}
func (d snapshot) read(r Request, now time.Time) (any, error) {
	if r.Op == "list" {
		return d.Jobs, nil
	}
	if r.Op == "get" {
		i := d.index(r.ID)
		if i < 0 {
			return nil, ErrNotFound
		}
		return d.Jobs[i], nil
	}
	m := Metrics{States: map[string]int{"queued": 0, "running": 0, "reported": 0, "accepted": 0}}
	for _, j := range d.Jobs {
		m.States[j.State]++
		m.Attempts += len(j.Attempts)
		for _, a := range j.Attempts {
			if a.RetriedAt != nil {
				m.Retries++
			}
		}
		q := j.QueuedAt
		if expired(j, now) {
			m.ExpiredEligible++
			q = j.Attempts[len(j.Attempts)-1].ExpiresAt
		}
		if j.State != "queued" && !expired(j, now) {
			continue
		}
		m.OldestQueueWaitSeconds = max(m.OldestQueueWaitSeconds, now.Sub(q).Seconds())
	}
	return m, nil
}
func expired(j Job, now time.Time) bool {
	return j.State == "running" && !now.Before(j.Attempts[len(j.Attempts)-1].ExpiresAt)
}
func (d *snapshot) apply(r Request, now time.Time) (any, bool, error) {
	if r.Op == "submit" {
		return d.submit(r, now)
	}
	if r.Op == "claim" {
		return d.claim(r, now)
	}
	i := d.index(r.ID)
	if i < 0 {
		return nil, false, ErrNotFound
	}
	j := &d.Jobs[i]
	if len(j.Attempts) == 0 || r.Token == "" || j.Attempts[len(j.Attempts)-1].Token != r.Token {
		return nil, false, ErrConflict
	}
	a := &j.Attempts[len(j.Attempts)-1]
	if r.Op == "accept" || r.Op == "retry" {
		return decide(j, a, r, now)
	}
	return work(j, a, r, now)
}
func work(j *Job, a *Attempt, r Request, now time.Time) (any, bool, error) {
	if r.Worker == "" || r.Worker != a.Worker {
		return nil, false, ErrConflict
	}
	if r.Op == "complete" && (j.State == "reported" || j.State == "accepted") && a.Result == r.Result && r.Result != "" {
		return *j, false, nil
	}
	if j.State != "running" || !now.Before(a.ExpiresAt) {
		return nil, false, ErrConflict
	}
	if r.Op == "renew" {
		if err := ttl(r); err != nil {
			return nil, false, err
		}
		// Renewal never shortens a lease, including after a clock rollback.
		next := now.Add(time.Duration(r.TTLSeconds) * time.Second)
		if next.After(a.ExpiresAt) {
			a.ExpiresAt = next
		}
		j.UpdatedAt = now
		return *j, true, nil
	}
	if strings.TrimSpace(r.Result) == "" {
		return nil, false, ErrInvalid
	}
	a.Result = r.Result
	a.CompletedAt = &now
	j.State = "reported"
	j.UpdatedAt = now
	return *j, true, nil
}
func decide(j *Job, a *Attempt, r Request, now time.Time) (any, bool, error) {
	if strings.TrimSpace(r.Evidence) == "" {
		return nil, false, ErrInvalid
	}
	if r.Op == "accept" {
		if j.State == "accepted" && j.Evidence == r.Evidence {
			return *j, false, nil
		}
		if j.State != "reported" {
			return nil, false, ErrConflict
		}
		j.State = "accepted"
		j.Evidence = r.Evidence
		j.AcceptedAt = &now
		j.UpdatedAt = now
		return *j, true, nil
	}
	if j.State == "queued" && a.RetriedAt != nil && a.Evidence == r.Evidence {
		return *j, false, nil
	}
	if j.State != "reported" && !expired(*j, now) {
		return nil, false, ErrConflict
	}
	a.RetriedAt = &now
	a.Evidence = r.Evidence
	j.State = "queued"
	j.QueuedAt = now
	j.UpdatedAt = now
	return *j, true, nil
}
func (d *snapshot) submit(r Request, now time.Time) (any, bool, error) {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.Brief) == "" {
		return nil, false, ErrInvalid
	}
	if i := d.index(r.ID); i >= 0 {
		if d.Jobs[i].Brief != r.Brief {
			return nil, false, ErrConflict
		}
		return d.Jobs[i], false, nil
	}
	j := Job{ID: r.ID, Brief: r.Brief, State: "queued", CreatedAt: now, QueuedAt: now, UpdatedAt: now, Attempts: []Attempt{}}
	d.Jobs = append(d.Jobs, j)
	return j, true, nil
}
func ttl(r Request) error {
	if r.TTLSeconds < 1 || r.TTLSeconds > 3600 {
		return fmt.Errorf("%w: ttl_seconds must be 1..3600", ErrInvalid)
	}
	return nil
}
func (d *snapshot) claim(r Request, now time.Time) (any, bool, error) {
	if strings.TrimSpace(r.Worker) == "" || strings.TrimSpace(r.Key) == "" {
		return nil, false, ErrInvalid
	}
	if err := ttl(r); err != nil {
		return nil, false, err
	}
	if result, found, err := d.replayClaim(r, now); found {
		return result, false, err
	}
	for _, j := range d.Jobs {
		if j.State == "running" && !expired(j, now) && j.Attempts[len(j.Attempts)-1].Worker == r.Worker {
			return nil, false, fmt.Errorf("%w: worker already active", ErrConflict)
		}
	}
	i := d.index(r.ID)
	if r.ID == "" {
		i = d.oldest(now)
	}
	if i < 0 {
		return nil, false, ErrNotFound
	}
	j := &d.Jobs[i]
	if j.State != "queued" && !expired(*j, now) {
		return nil, false, ErrConflict
	}
	token := make([]byte, 24)
	if _, err := rand.Read(token); err != nil {
		return nil, false, err
	}
	d.retireExpiredWorker(r.Worker, now)
	if expired(*j, now) {
		a := &j.Attempts[len(j.Attempts)-1]
		a.RetriedAt = &now
		a.Evidence = "lease expired; reclaimed"
	}
	j.Attempts = append(j.Attempts, Attempt{Token: hex.EncodeToString(token), Worker: r.Worker, Key: r.Key, ClaimID: r.ID, TTLSeconds: r.TTLSeconds, StartedAt: now, ExpiresAt: now.Add(time.Duration(r.TTLSeconds) * time.Second)})
	j.State = "running"
	j.UpdatedAt = now
	return *j, true, nil
}
func (d snapshot) replayClaim(r Request, now time.Time) (any, bool, error) {
	for _, j := range d.Jobs {
		for i, a := range j.Attempts {
			if a.Key != r.Key {
				continue
			}
			if a.Worker != r.Worker || a.ClaimID != r.ID || a.TTLSeconds != r.TTLSeconds || i != len(j.Attempts)-1 || j.State != "running" || expired(j, now) {
				return nil, true, ErrConflict
			}
			return j, true, nil
		}
	}
	return nil, false, nil
}
func (d snapshot) oldest(now time.Time) int {
	candidates := []int{}
	for i, j := range d.Jobs {
		if j.State == "queued" || expired(j, now) {
			candidates = append(candidates, i)
		}
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		return eligibleAt(d.Jobs[candidates[a]]).Before(eligibleAt(d.Jobs[candidates[b]]))
	})
	if len(candidates) == 0 {
		return -1
	}
	return candidates[0]
}
func eligibleAt(j Job) time.Time {
	if j.State == "running" {
		return j.Attempts[len(j.Attempts)-1].ExpiresAt
	}
	return j.QueuedAt
}

// Retire expired claims before reusing a worker identity. Merely testing expiry
// would let an old token become usable again after wall-clock rollback.
func (d *snapshot) retireExpiredWorker(worker string, now time.Time) {
	for i := range d.Jobs {
		j := &d.Jobs[i]
		if !expired(*j, now) {
			continue
		}
		a := &j.Attempts[len(j.Attempts)-1]
		if a.Worker != worker {
			continue
		}
		a.RetriedAt = &now
		a.Evidence = "lease expired; worker reassigned"
		j.State = "queued"
		j.QueuedAt = a.ExpiresAt
		j.UpdatedAt = now
	}
}
