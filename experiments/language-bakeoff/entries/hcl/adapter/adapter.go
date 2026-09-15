// Package adapter is the extension contract between the engine and the
// operational foundation. An adapter owns the real effects and observations
// for one block type; the compiler and planner never learn what a "file" or a
// "dir" is. Adding a block type means implementing this interface and
// registering the value: no parser or planner edits.
package adapter

import "context"

// Kind separates persistent desired state from one-shot work.
type Kind string

const (
	// Resource is persistent desired state. Every plan compares the adapter's
	// live observation with the desired fact; no state file remembers it.
	Resource Kind = "resource"
	// Task is one-shot work. It re-runs when its configuration, declared
	// inputs or declared outputs no longer match its last recorded receipt.
	Task Kind = "task"
)

// FieldType is the deliberately small set of attribute types a block declares.
type FieldType string

// The supported field types.
const (
	String     FieldType = "string"
	StringList FieldType = "list(string)"
)

// Field declares one authorable attribute of a block.
type Field struct {
	Name     string
	Type     FieldType
	Required bool
}

// Values holds evaluated block attributes: string or []string, keyed by name.
type Values map[string]any

// Str returns the string attribute name, or "" when it is absent.
func (v Values) Str(name string) string {
	s, _ := v[name].(string)
	return s
}

// List returns the list attribute name, or nil when it is absent.
func (v Values) List(name string) []string {
	l, _ := v[name].([]string)
	return l
}

// Paths declares the workspace paths a block owns (creates or rewrites) and
// reads. The compiler checks ownership and references against them; the
// planner treats a task's Reads and Owns as its staleness inputs and outputs.
type Paths struct {
	Owns  []string
	Reads []string
}

// Adapter is implemented by every block type.
type Adapter interface {
	// Type is the first block label and the reference root, e.g. "file".
	Type() string
	Kind() Kind
	Fields() []Field
	// Resolve validates evaluated attributes and returns the full set other
	// blocks may reference: authored values plus derived ones. No I/O.
	Resolve(cfg Values) (Values, error)
	// Paths reports which workspace paths the resolved block owns and reads.
	Paths(v Values) Paths
}

// Fact is one observation of the backend. Equal fingerprints mean "no change".
type Fact struct {
	Exists      bool
	Fingerprint string
	// Summary is a short human description, e.g. "34 bytes, sha256 3c5f0e1a2b9d".
	Summary string
	// Text is the full content when HasText is set (small, valid UTF-8), for
	// readable plan diffs. HasText distinguishes empty content from content
	// that is binary, large or absent.
	Text    string
	HasText bool
}

// ResourceAdapter converges persistent desired state.
type ResourceAdapter interface {
	Adapter
	// Want is the fact Observe reports once the resource has converged.
	Want(v Values) Fact
	// Observe reads the backend and must not change it. It returns an error
	// for states the adapter refuses to take over (e.g. a file where a
	// directory is wanted).
	Observe(ws Workspace, v Values) (Fact, error)
	// Apply makes the backend match v. It must be safe to repeat.
	Apply(ws Workspace, v Values) error
}

// TaskAdapter performs one-shot work. The engine decides when; Run only does.
type TaskAdapter interface {
	Adapter
	Run(ctx context.Context, ws Workspace, v Values) error
}

// FieldError points a validation failure at one attribute, so diagnostics can
// show the attribute's source range instead of the whole block.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Msg }
