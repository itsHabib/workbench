package swarm

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/itsHabib/workbench/cmd/swarm/internal/plane"
)

// Resource is an exclusive thing (a fixture database, a device, a live
// app) that one seat holds at a time. It is an item on the plane: a lease
// with a holder, an epoch that only grows, and an expiry on the store's
// clock. There is no liveness probe, because the machine a resource drives
// may keep running after its holder dies. Renew by taking it again.
type Resource struct {
	Name   string    `json:"name"`
	Holder string    `json:"holder"`
	Epoch  int       `json:"epoch"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
}

const kindResource = "resource"

// Plane opens the coordination store this state uses: SWARM_STORE when set
// (resp:HOST:PORT/PREFIX lets seats on different machines share it),
// otherwise a file store beside the rest of the state.
func (s *State) Plane() (plane.Store, error) {
	if spec := os.Getenv("SWARM_STORE"); spec != "" {
		st, err := plane.OpenStore(spec)
		if f, ok := st.(*plane.File); ok {
			f.Now = func() time.Time { return Now() }
		}
		return st, err
	}
	f, err := plane.OpenFile(s.path("plane"))
	if err != nil {
		return nil, err
	}
	f.Now = func() time.Time { return Now() }
	return f, nil
}

// incarnation is who a lease belongs to. SWARM_INCARNATION distinguishes
// two processes that share a seat name, as clones of one snapshot do.
func incarnation(holder string) string {
	if inc := os.Getenv("SWARM_INCARNATION"); inc != "" {
		return holder + "@" + inc
	}
	return holder
}

// Take leases name to holder for ttl. Another holder's unexpired lease
// refuses; an expired or released one changes hands under a new epoch.
func (s *State) Take(name, holder string, ttl time.Duration) (*Resource, error) {
	if name == "" || holder == "" {
		return nil, refuse("bad_take", "take needs a resource name and --as <seat>")
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Put(ctx, plane.Item{Kind: kindResource, ID: name}); err != nil {
		return nil, err
	}
	owner := incarnation(holder)
	it, err := st.Get(ctx, kindResource, name)
	if err != nil {
		return nil, err
	}
	var g plane.Grant
	if it.Owner == owner && Now().Before(it.Until) {
		// Renewal keeps the epoch: the holder's token stays valid.
		g, err = st.Renew(ctx, plane.Grant{Kind: kindResource, ID: name, Epoch: it.Epoch, Owner: owner}, ttl)
	} else {
		if it.Owner != "" && it.Owner != owner && !Now().Before(it.Until) {
			s.appendEvent(Event{Kind: "take_over", Seat: holder, Detail: name})
		}
		g, err = st.Claim(ctx, kindResource, name, owner, ttl, "")
	}
	if errors.Is(err, plane.ErrHeld) {
		return nil, refuse("held_by_other", "%s is held by %s until %s", name, it.Owner, it.Until.Format(time.RFC3339))
	}
	if err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "take", Seat: holder, Detail: name, Epoch: int(g.Epoch)})
	return &Resource{Name: name, Holder: holder, Epoch: int(g.Epoch), Since: Now(), Until: g.Until}, nil
}

// Drop releases name if holder holds it under epoch. epoch 0 means "the
// lease I hold now"; a caller that kept its token passes it, and a delayed
// drop from an earlier lease is refused instead of releasing a later one.
func (s *State) Drop(name, holder string, epoch int) error {
	st, err := s.Plane()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	it, err := st.Get(ctx, kindResource, name)
	if errors.Is(err, plane.ErrNotFound) || (err == nil && it.Owner == "") {
		return refuse("not_held", "%s is not held", name)
	}
	if err != nil {
		return err
	}
	owner := incarnation(holder)
	if it.Owner != owner {
		return refuse("held_by_other", "%s is held by %s, not %s", name, it.Owner, holder)
	}
	if epoch != 0 && int64(epoch) != it.Epoch {
		return refuse("lease_fenced", "%s is at epoch %d; your token %d is from an earlier lease", name, it.Epoch, epoch)
	}
	if err := st.Release(ctx, plane.Grant{Kind: kindResource, ID: name, Epoch: it.Epoch, Owner: owner}); err != nil {
		if errors.Is(err, plane.ErrFenced) {
			return refuse("lease_fenced", "%s changed hands while you were dropping it", name)
		}
		return err
	}
	s.appendEvent(Event{Kind: "drop", Seat: holder, Detail: name, Epoch: int(it.Epoch)})
	return nil
}

// Resources lists leases that have a holder, expired ones included and
// marked by Until.
func (s *State) Resources() ([]Resource, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	items, err := st.List(context.Background(), kindResource)
	if err != nil {
		return nil, err
	}
	var out []Resource
	for _, it := range items {
		if it.Owner != "" {
			out = append(out, Resource{Name: it.ID, Holder: it.Owner, Epoch: int(it.Epoch), Until: it.Until})
		}
	}
	return out, nil
}

// Holder returns who holds name now, or "" when free or expired.
func (s *State) Holder(name string) string {
	st, err := s.Plane()
	if err != nil {
		return ""
	}
	defer st.Close()
	it, err := st.Get(context.Background(), kindResource, name)
	if err != nil || it.Owner == "" || !Now().Before(it.Until) {
		return ""
	}
	return it.Owner
}
