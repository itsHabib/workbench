// Package reviewpanel defines the exact-head ReviewPanelV1 evidence exchanged
// by evidence collectors and review-completeness verifiers.
package reviewpanel

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// SchemaVersion is the supported ReviewPanelV1 major version.
const SchemaVersion = 1

// Evidence records the repository declaration and the observed disposition of
// every required reviewer for one exact pull-request head.
type Evidence struct {
	SchemaVersion int         `json:"schema_version"`
	Subject       Subject     `json:"subject"`
	Declaration   Declaration `json:"declaration"`
	Completed     []Reviewer  `json:"completed"`
	Pending       []string    `json:"pending"`
	Missing       []string    `json:"missing"`
	Unknown       []string    `json:"unknown"`
}

// Subject binds panel evidence to one exact pull-request head.
type Subject struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
}

// Declaration identifies the repository-owned expected reviewer set.
type Declaration struct {
	Path     string   `json:"path"`
	Revision string   `json:"revision,omitempty"`
	Expected []string `json:"expected"`
}

// Reviewer records one exact-head review submission.
type Reviewer struct {
	Name     string `json:"name"`
	Actor    string `json:"actor"`
	State    string `json:"state"`
	HeadSHA  string `json:"head_sha"`
	ReviewID int64  `json:"review_id"`
}

// MarshalJSON keeps every schema-required collection an array even when a
// producer leaves its Go slice nil. ReviewPanelV1 does not permit null arrays.
func (e Evidence) MarshalJSON() ([]byte, error) {
	type plain Evidence
	e.Declaration.Expected = nonNil(e.Declaration.Expected)
	e.Completed = nonNil(e.Completed)
	e.Pending = nonNil(e.Pending)
	e.Missing = nonNil(e.Missing)
	e.Unknown = nonNil(e.Unknown)
	return json.Marshal(plain(e))
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// Validate enforces contract consistency without deciding whether an
// incomplete panel may proceed.
func Validate(e Evidence) error {
	switch {
	case e.SchemaVersion != SchemaVersion:
		return fmt.Errorf("reviewpanel: unsupported schema_version %d", e.SchemaVersion)
	case e.Subject.Repo == "":
		return errors.New("reviewpanel: subject repo is empty")
	case e.Subject.Number < 1:
		return errors.New("reviewpanel: subject number must be positive")
	case e.Subject.HeadSHA == "":
		return errors.New("reviewpanel: subject head_sha is empty")
	case e.Declaration.Path == "":
		return errors.New("reviewpanel: declaration path is empty")
	}
	if err := unique("expected", e.Declaration.Expected); err != nil {
		return err
	}
	if err := unique("pending", e.Pending); err != nil {
		return err
	}
	if err := unique("missing", e.Missing); err != nil {
		return err
	}
	if err := unique("unknown", e.Unknown); err != nil {
		return err
	}
	seen, err := validateCompleted(e)
	if err != nil {
		return err
	}
	if err := addDispositions(seen, e.Pending, e.Missing, e.Unknown); err != nil {
		return err
	}
	return validatePartition(e.Declaration.Expected, seen)
}

func validateCompleted(e Evidence) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(e.Completed))
	for _, reviewer := range e.Completed {
		if reviewer.Name == "" || reviewer.Actor == "" || reviewer.State == "" ||
			reviewer.HeadSHA == "" || reviewer.ReviewID < 1 {
			return nil, errors.New("reviewpanel: completed reviewer fields are required")
		}
		if !validReviewerState(reviewer.State) {
			return nil, fmt.Errorf("reviewpanel: completed reviewer %s has invalid state %s", reviewer.Name, reviewer.State)
		}
		if reviewer.HeadSHA != e.Subject.HeadSHA {
			return nil, fmt.Errorf("reviewpanel: completed reviewer %s is stale", reviewer.Name)
		}
		if _, ok := seen[reviewer.Name]; ok {
			return nil, fmt.Errorf("reviewpanel: completed reviewer %s is duplicated", reviewer.Name)
		}
		seen[reviewer.Name] = struct{}{}
	}
	return seen, nil
}

func validReviewerState(state string) bool {
	switch state {
	case "APPROVED", "COMMENTED", "CHANGES_REQUESTED", "CLEAN":
		return true
	}
	return false
}

func addDispositions(seen map[string]struct{}, sets ...[]string) error {
	for _, names := range sets {
		if err := addDispositionSet(seen, names); err != nil {
			return err
		}
	}
	return nil
}

func addDispositionSet(seen map[string]struct{}, names []string) error {
	for _, name := range names {
		if _, ok := seen[name]; ok {
			return fmt.Errorf("reviewpanel: reviewer %s has multiple dispositions", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validatePartition(expected []string, seen map[string]struct{}) error {
	if len(expected) == 0 {
		return nil
	}
	if len(seen) != len(expected) {
		return errors.New("reviewpanel: dispositions must partition expected reviewers")
	}
	expected = append([]string(nil), expected...)
	actual := make([]string, 0, len(seen))
	for name := range seen {
		actual = append(actual, name)
	}
	sort.Strings(expected)
	sort.Strings(actual)
	for i := range expected {
		if expected[i] != actual[i] {
			return errors.New("reviewpanel: dispositions must partition expected reviewers")
		}
	}
	return nil
}

func unique(field string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return fmt.Errorf("reviewpanel: %s contains an empty value", field)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("reviewpanel: %s contains duplicate %s", field, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
