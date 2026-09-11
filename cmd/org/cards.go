package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/itsHabib/workbench/filelock"
)

// The registry points to prose. It does not copy the prose or interpret it.
// roles.map remains the independent directory/seat binding used by runtimes.
type roleCard struct {
	Tenant string `json:"tenant"`
	Role   string `json:"role"`
	Card   string `json:"card"`
	Parent string `json:"parent,omitempty"`
}

type cardScope struct {
	fs                  *flag.FlagSet
	state, tenant, role string
	asJSON              bool
}

func newCardScope(name string, stderr io.Writer) *cardScope {
	s := &cardScope{fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	s.fs.SetOutput(stderr)
	s.fs.StringVar(&s.state, "state", envOr("ORG_STATE", defaultState()), "registry directory")
	s.fs.StringVar(&s.tenant, "tenant", envOr("ORG_TENANT", "mh"), "tenant")
	s.fs.StringVar(&s.role, "role", "", "role name")
	s.fs.BoolVar(&s.asJSON, "json", false, "JSON output")
	return s
}

func (s *cardScope) parse(args []string, needRole bool) error {
	if err := s.fs.Parse(args); err != nil {
		return err
	}
	if s.fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments; flags must precede values")
	}
	if s.state == "" || strings.TrimSpace(s.tenant) == "" {
		return fmt.Errorf("state and tenant must be nonempty")
	}
	if needRole && strings.TrimSpace(s.role) == "" {
		return fmt.Errorf("-role is required")
	}
	return nil
}

func loadCards(state string) ([]roleCard, error) {
	path := filepath.Join(state, "roles.json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []roleCard{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cards []roleCard
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cards); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%s must contain one JSON array", path)
	}
	if cards == nil {
		return nil, fmt.Errorf("%s must be a JSON array", path)
	}
	seen := map[[2]string]bool{}
	for _, c := range cards {
		key := [2]string{c.Tenant, c.Role}
		if c.Tenant == "" || c.Role == "" || !filepath.IsAbs(c.Card) || seen[key] {
			return nil, fmt.Errorf("invalid or duplicate role in %s", path)
		}
		seen[key] = true
	}
	return cards, nil
}

func registerCard(e *env, args []string) error {
	s := newCardScope("charter", e.stderr)
	file := s.fs.String("file", "", "Markdown card to register (kept in place)")
	parent := s.fs.String("parent", "", "optional parent reference; an empty value clears it")
	if err := s.parse(args, true); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("-file <card.md> is required; write scope and responsibilities in that prose file")
	}
	card, err := filepath.Abs(*file)
	if err != nil {
		return err
	}
	f, err := os.Open(card)
	if err != nil {
		return err
	}
	st, statErr := f.Stat()
	_ = f.Close()
	if statErr != nil {
		return statErr
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", card)
	}
	parentSet := false
	s.fs.Visit(func(f *flag.Flag) {
		if f.Name == "parent" {
			parentSet = true
		}
	})
	c := roleCard{Tenant: s.tenant, Role: s.role, Card: card, Parent: *parent}
	if err := saveCard(s.state, &c, parentSet); err != nil {
		return err
	}
	if s.asJSON {
		return printJSON(e, c)
	}
	fmt.Fprintf(e.stdout, "%s @ %s: %s\n", c.Role, c.Tenant, c.Card)
	fmt.Fprintln(e.stdout, "Edit the card to change the role; no lifecycle transition is needed.")
	return nil
}

func saveCard(state string, c *roleCard, parentSet bool) error {
	if err := os.MkdirAll(state, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(state, "roles.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		return err
	}
	cards, err := loadCards(state)
	if err != nil {
		return err
	}
	found := false
	for i, old := range cards {
		if old.Tenant != c.Tenant || old.Role != c.Role {
			continue
		}
		if !parentSet {
			c.Parent = old.Parent
		}
		cards[i], found = *c, true
		break
	}
	if !found {
		cards = append(cards, *c)
	}
	sort.Slice(cards, func(i, j int) bool {
		if cards[i].Tenant != cards[j].Tenant {
			return cards[i].Tenant < cards[j].Tenant
		}
		return cards[i].Role < cards[j].Role
	})
	return writeCards(state, cards)
}

func writeCards(state string, cards []roleCard) error {
	f, err := os.CreateTemp(state, ".roles-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cards); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(state, "roles.json"))
}

func readCard(e *env, args []string) error {
	s := newCardScope("boot", e.stderr)
	budget := s.fs.Int("max-bytes", 0, "prose byte budget; 0 reads the full card")
	if err := s.parse(args, true); err != nil {
		return err
	}
	if *budget < 0 {
		return fmt.Errorf("-max-bytes must be nonnegative")
	}
	cards, err := loadCards(s.state)
	if err != nil {
		return err
	}
	for _, c := range cards {
		if c.Tenant != s.tenant || c.Role != s.role {
			continue
		}
		return showCard(e, c, *budget, s.asJSON)
	}
	return fmt.Errorf("no registered card for %s @ %s; use org charter -role %s -file <card.md>. Existing chain context: org legacy boot -role %s (with the same -state and -tenant)", s.role, s.tenant, s.role, s.role)
}

func showCard(e *env, c roleCard, budget int, asJSON bool) error {
	b, err := os.ReadFile(c.Card)
	if err != nil {
		return err
	}
	truncated := budget > 0 && len(b) > budget
	if truncated {
		b = b[:budget]
		for !utf8.Valid(b) && len(b) > 0 {
			b = b[:len(b)-1]
		}
	}
	if asJSON {
		return printJSON(e, struct {
			roleCard
			Instructions string `json:"instructions"`
			Truncated    bool   `json:"truncated"`
		}{c, string(b), truncated})
	}
	fmt.Fprintf(e.stdout, "# Role: %s @ %s\nCard: %s\n", c.Role, c.Tenant, c.Card)
	if c.Parent != "" {
		fmt.Fprintf(e.stdout, "Parent: %s\n", c.Parent)
	}
	fmt.Fprintf(e.stdout, "\n%s\n", b)
	if truncated {
		fmt.Fprintf(e.stdout, "[Card truncated; read %s for the full instructions.]\n", c.Card)
	}
	return nil
}

func listCards(e *env, args []string) error {
	s := newCardScope("status", e.stderr)
	if err := s.parse(args, false); err != nil {
		return err
	}
	cards, err := loadCards(s.state)
	if err != nil {
		return err
	}
	rows := []roleCard{}
	for _, c := range cards {
		if c.Tenant == s.tenant {
			rows = append(rows, c)
		}
	}
	if s.asJSON {
		return printJSON(e, rows)
	}
	for _, c := range rows {
		fmt.Fprintf(e.stdout, "%s\t%s\t%s\n", c.Role, c.Parent, c.Card)
	}
	fmt.Fprintln(e.stdout, "Registered role cards only. Historical chains: org legacy status.")
	return nil
}
