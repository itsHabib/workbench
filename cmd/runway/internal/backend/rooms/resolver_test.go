package rooms

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
)

func TestParseCustodyRefGrammar(t *testing.T) {
	cases := []struct {
		name    string
		ref     string
		ok      bool
		key     string
		actions []string
	}{
		{"single action", "custody:tracker/read", true, "tracker", []string{"read"}},
		{"multi action", "custody:tracker/read,comment", true, "tracker", []string{"read", "comment"}},
		{"hyphenated key and action", "custody:my-key/do-thing", true, "my-key", []string{"do-thing"}},
		{"not a custody ref", "env:TRACKER", false, "", nil},
		{"bare scheme no slash", "custody:tracker", false, "", nil},
		{"empty action list", "custody:tracker/", false, "", nil},
		{"empty key", "custody:/read", false, "", nil},
		{"empty action in list", "custody:tracker/read,,comment", false, "", nil},
		{"trailing comma", "custody:tracker/read,", false, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := parseCustodyRef("SECRET", tc.ref)
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("want error for %q, parsed %+v", tc.ref, ref)
				}
				return
			}
			if ref.key != tc.key || strings.Join(ref.actions, ",") != strings.Join(tc.actions, ",") {
				t.Fatalf("ref=%+v want key=%s actions=%v", ref, tc.key, tc.actions)
			}
		})
	}
}

// fakePort is an in-memory custodyPort so resolver policy is tested without a
// custody binary or grant records.
type fakePort struct {
	parent    parentGrant
	parentErr error
	child     childGrant
	deriveErr error
	gotDerive deriveRequest
}

func (f *fakePort) ParentGrant(string) (parentGrant, error) {
	if f.parentErr != nil {
		return parentGrant{}, f.parentErr
	}
	return f.parent, nil
}

func (f *fakePort) Derive(_ context.Context, req deriveRequest) (childGrant, error) {
	f.gotDerive = req
	if f.deriveErr != nil {
		return childGrant{}, f.deriveErr
	}
	return f.child, nil
}

func TestResolveRefusesWithRemedyWhenNoLiveParent(t *testing.T) {
	now := time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC)
	deadline := now.Add(40 * time.Minute)
	ref := custodyRef{secretName: "CUSTODY_GRANT_TRACKER", key: "tracker", actions: []string{"comment"}}

	cases := map[string]*fakePort{
		"no parent staged": {parentErr: errors.New("no parent grant staged for key \"tracker\"")},
		"parent expired":   {parent: parentGrant{id: "p", actions: []string{"comment"}, expiry: now.Add(-time.Minute)}},
		"parent narrower":  {parent: parentGrant{id: "p", actions: []string{"read"}, expiry: now.Add(time.Hour)}},
	}
	for name, port := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Resolver{port: port}.Resolve(context.Background(), ref, deadline, time.Minute, "172.30.0.7", now)
			u, ok := backend.AsAuthorityUnresolved(err)
			if !ok {
				t.Fatalf("want AuthorityUnresolved, got %v", err)
			}
			if u.Ref != "CUSTODY_GRANT_TRACKER" {
				t.Fatalf("ref=%q", u.Ref)
			}
			if u.Remedy != "custody grant -key tracker -actions comment -ttl 8h" {
				t.Fatalf("remedy=%q", u.Remedy)
			}
		})
	}
}

func TestResolveRefusalCarriesReasonCode(t *testing.T) {
	// The refusal error must be the reason_code string the receipt/journal keys
	// on, not prose — a caller branches on it via the typed error.
	port := &fakePort{parentErr: errors.New("missing")}
	ref := custodyRef{secretName: "S", key: "k", actions: []string{"read"}}
	_, err := Resolver{port: port}.Resolve(context.Background(), ref, time.Now().Add(time.Hour), 0, "", time.Now())
	if !strings.Contains(err.Error(), "authority_unresolved") {
		t.Fatalf("error must name authority_unresolved: %v", err)
	}
}

func TestCapTTLTable(t *testing.T) {
	now := time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC)
	deadline := now.Add(40 * time.Minute)
	grace := time.Minute
	// runCap = deadline + grace + margin = 40m + 1m + 90s = 42m30s.
	runCap := 40*time.Minute + grace + authorityTTLMargin

	cases := []struct {
		name         string
		parentExpiry time.Time
		want         time.Duration
	}{
		{"deadline-limited (parent far)", now.Add(7 * 24 * time.Hour), runCap},
		{"parent-limited (parent sooner)", now.Add(10 * time.Minute), 10 * time.Minute},
		{"margin is included past deadline+grace", now.Add(time.Hour), runCap},
		{"exactly at run cap", now.Add(runCap), runCap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := capTTL(tc.parentExpiry, deadline, grace, now)
			if got != tc.want {
				t.Fatalf("capTTL=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestResolveDerivesRunCappedSourceBoundChild(t *testing.T) {
	now := time.Date(2026, 7, 22, 19, 0, 0, 0, time.UTC)
	deadline := now.Add(40 * time.Minute)
	grace := time.Minute
	port := &fakePort{
		parent: parentGrant{
			id:      "parent0000000000000000000000feed",
			digest:  "sha256:aa",
			token:   "cst2_parent0000000000000000000000feed.sig",
			actions: []string{"read", "comment"},
			expiry:  now.Add(7 * 24 * time.Hour),
		},
		child: childGrant{
			id:          "child00000000000000000000000beef",
			digest:      "sha256:bb",
			token:       "cst2_child00000000000000000000000beef.sig",
			actions:     []string{"read"},
			boundSource: "172.30.0.7",
			mintedAt:    now,
			expiry:      now.Add(42 * time.Minute),
		},
	}
	ref := custodyRef{secretName: "CUSTODY_GRANT_TRACKER", key: "tracker", actions: []string{"read"}}
	rec, err := Resolver{port: port}.Resolve(context.Background(), ref, deadline, grace, "172.30.0.7", now)
	if err != nil {
		t.Fatal(err)
	}
	if port.gotDerive.boundSource != "172.30.0.7" {
		t.Fatalf("derive not source-bound: %+v", port.gotDerive)
	}
	if want := 40*time.Minute + grace + authorityTTLMargin; port.gotDerive.ttl != want {
		t.Fatalf("derive ttl=%s want %s (run-capped)", port.gotDerive.ttl, want)
	}
	if rec.ParentID != port.parent.id || rec.ChildID != port.child.id || rec.ChildToken != port.child.token {
		t.Fatalf("record=%+v", rec)
	}
	if strings.Join(rec.ParentActions, ",") != "read,comment" || strings.Join(rec.Actions, ",") != "read" {
		t.Fatalf("attenuation not recorded: %+v", rec)
	}
}
