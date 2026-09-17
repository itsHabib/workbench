package swarm

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Resource is an exclusive thing (a fixture database, a device, a live
// app) that one seat holds at a time. A lease has a holder, an epoch and an
// expiry; there is no liveness probe because the machine a resource drives
// may keep running after its holder dies. Renew by taking it again.
type Resource struct {
	Name   string    `json:"name"`
	Holder string    `json:"holder"`
	Epoch  int       `json:"epoch"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
}

func (s *State) resourcePath(name string) string {
	return s.path("resources", strings.ReplaceAll(name, "/", "_")+".json")
}

// Take leases name to holder for ttl. Another holder's unexpired lease
// refuses; an expired one changes hands with a new epoch.
func (s *State) Take(name, holder string, ttl time.Duration) (*Resource, error) {
	if name == "" || holder == "" {
		return nil, refuse("bad_take", "take needs a resource name and --as <seat>")
	}
	release, err := s.lock("resources", 5*time.Second, time.Minute)
	if err != nil {
		return nil, err
	}
	defer release()
	now := Now()
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	var r Resource
	err = readJSON(s.resourcePath(name), &r)
	switch {
	case err == nil && r.Holder != holder && now.Before(r.Until):
		return nil, refuse("held_by_other", "%s is held by %s until %s", name, r.Holder, r.Until.Format(time.RFC3339))
	case err == nil && r.Holder == holder:
		r.Until = now.Add(ttl)
	case err == nil:
		r = Resource{Name: name, Holder: holder, Epoch: r.Epoch + 1, Since: now, Until: now.Add(ttl)}
		s.appendEvent(Event{Kind: "take_over", Seat: holder, Detail: name})
	case errors.Is(err, os.ErrNotExist):
		r = Resource{Name: name, Holder: holder, Epoch: 1, Since: now, Until: now.Add(ttl)}
	default:
		return nil, err
	}
	if err := writeJSON(s.resourcePath(name), &r); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "take", Seat: holder, Detail: name, Epoch: r.Epoch})
	return &r, nil
}

// Drop releases name if holder holds it.
func (s *State) Drop(name, holder string) error {
	release, err := s.lock("resources", 5*time.Second, time.Minute)
	if err != nil {
		return err
	}
	defer release()
	var r Resource
	if err := readJSON(s.resourcePath(name), &r); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return refuse("not_held", "%s is not held", name)
		}
		return err
	}
	if r.Holder != holder {
		return refuse("held_by_other", "%s is held by %s, not %s", name, r.Holder, holder)
	}
	if err := os.Remove(s.resourcePath(name)); err != nil {
		return err
	}
	s.appendEvent(Event{Kind: "drop", Seat: holder, Detail: name, Epoch: r.Epoch})
	return nil
}

// Resources lists current leases, expired ones included and marked by Until.
func (s *State) Resources() ([]Resource, error) {
	entries, err := os.ReadDir(s.path("resources"))
	if err != nil {
		return nil, err
	}
	var out []Resource
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var r Resource
		if err := readJSON(filepath.Join(s.path("resources"), e.Name()), &r); err == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Holder returns who holds name now, or "" when free or expired.
func (s *State) Holder(name string) string {
	var r Resource
	if err := readJSON(s.resourcePath(name), &r); err != nil || Now().After(r.Until) {
		return ""
	}
	return r.Holder
}
