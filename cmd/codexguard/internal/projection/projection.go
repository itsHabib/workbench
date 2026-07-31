// Package projection safely projects reviewed codexguard assets into a Codex
// home. It owns file installation only; policy and hook lifecycle stay in their
// respective packages.
package projection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Version identifies the projection plan contract.
const Version = "codexguard-projection-v1"

var (
	// ErrDivergent means an existing target differs from the reviewed asset.
	ErrDivergent = errors.New("projection target diverges")
	// ErrInvalidPlan means apply did not receive a valid status digest.
	ErrInvalidPlan = errors.New("projection plan digest is invalid")
	// ErrPlanChanged means target state changed after status observed it.
	ErrPlanChanged = errors.New("projection plan changed")
	// ErrUnsafePath means a target path traverses a link, junction, or
	// non-directory ancestor.
	ErrUnsafePath = errors.New("projection target path is unsafe")
)

// Asset is one reviewed file and its path relative to a Codex home.
type Asset struct {
	Path string
	Data []byte
}

// Entry is one observed target in a projection plan.
type Entry struct {
	Path          string `json:"path"`
	State         string `json:"state"`
	CurrentSHA256 string `json:"current_sha256,omitempty"`
	DesiredSHA256 string `json:"desired_sha256"`
}

// Plan binds desired assets to the exact target state observed by status.
type Plan struct {
	Version string  `json:"version"`
	Home    string  `json:"home"`
	Digest  string  `json:"digest"`
	Entries []Entry `json:"entries"`
}

// Inspect returns a deterministic, non-mutating plan.
func Inspect(home string, assets []Asset) (Plan, error) {
	root, normalized, err := normalize(home, assets)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Version: Version,
		Home:    root,
		Entries: make([]Entry, 0, len(normalized)),
	}
	for _, asset := range normalized {
		entry, err := inspectEntry(root, asset)
		if err != nil {
			return Plan{}, err
		}
		plan.Entries = append(plan.Entries, entry)
	}
	plan.Digest, err = planDigest(plan)
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// Apply installs only missing files after re-observing the expected plan.
// Current files are idempotent; divergent files are never overwritten.
func Apply(home, expected string, assets []Asset) (Plan, error) {
	return apply(home, expected, assets, applyHooks{})
}

type applyHooks struct {
	beforeCommit func()
	afterPublish func(int, stagedFile)
}

func apply(home, expected string, assets []Asset, hooks applyHooks) (Plan, error) {
	if len(expected) != sha256.Size*2 {
		return Plan{}, fmt.Errorf("%w: expected 64 hex characters", ErrInvalidPlan)
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return Plan{}, fmt.Errorf("%w: expected lowercase hexadecimal", ErrInvalidPlan)
	}
	if expected != strings.ToLower(expected) {
		return Plan{}, fmt.Errorf("%w: expected lowercase hexadecimal", ErrInvalidPlan)
	}
	plan, err := Inspect(home, assets)
	if err != nil {
		return Plan{}, err
	}
	if plan.Digest != expected {
		return plan, fmt.Errorf("%w: got %s, expected %s", ErrPlanChanged, plan.Digest, expected)
	}
	for _, entry := range plan.Entries {
		if entry.State == "divergent" {
			return plan, fmt.Errorf("%w: %s", ErrDivergent, entry.Path)
		}
	}
	root, normalized, err := normalize(home, assets)
	if err != nil {
		return Plan{}, err
	}
	staged, err := stage(root, normalized, plan.Entries)
	if err != nil {
		return Plan{}, err
	}
	defer removeStages(staged)
	if hooks.beforeCommit != nil {
		hooks.beforeCommit()
	}
	created, err := commit(staged, hooks.afterPublish)
	if err != nil {
		rollback(created)
		return Plan{}, err
	}
	applied, err := Inspect(home, assets)
	if err != nil {
		rollback(created)
		return Plan{}, err
	}
	for _, entry := range applied.Entries {
		if entry.State != "current" {
			rollback(created)
			return applied, fmt.Errorf("%w: %s became %s during apply", ErrPlanChanged, entry.Path, entry.State)
		}
	}
	return applied, nil
}

type normalizedAsset struct {
	path string
	data []byte
}

func normalize(home string, assets []Asset) (string, []normalizedAsset, error) {
	if strings.TrimSpace(home) == "" {
		return "", nil, errors.New("projection: Codex home is empty")
	}
	root, err := filepath.Abs(home)
	if err != nil {
		return "", nil, fmt.Errorf("projection: resolve Codex home: %w", err)
	}
	if len(assets) == 0 {
		return "", nil, errors.New("projection: no assets")
	}
	seen := make(map[string]struct{}, len(assets))
	normalized := make([]normalizedAsset, 0, len(assets))
	for _, asset := range assets {
		rel := filepath.Clean(filepath.FromSlash(asset.Path))
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", nil, fmt.Errorf("projection: asset path escapes Codex home: %q", asset.Path)
		}
		target := filepath.Join(root, rel)
		inside, err := filepath.Rel(root, target)
		if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
			return "", nil, fmt.Errorf("projection: asset path escapes Codex home: %q", asset.Path)
		}
		key := strings.ToLower(filepath.ToSlash(rel))
		if _, ok := seen[key]; ok {
			return "", nil, fmt.Errorf("projection: duplicate asset path: %q", asset.Path)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, normalizedAsset{path: filepath.ToSlash(rel), data: append([]byte(nil), asset.Data...)})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].path < normalized[j].path })
	return filepath.Clean(root), normalized, nil
}

func inspectEntry(root string, asset normalizedAsset) (Entry, error) {
	entry := Entry{
		Path:          asset.path,
		State:         "missing",
		DesiredSHA256: digest(asset.data),
	}
	target := filepath.Join(root, filepath.FromSlash(asset.path))
	if err := checkParents(root, filepath.Dir(target)); err != nil {
		return Entry{}, fmt.Errorf("projection: inspect %s: %w", asset.path, err)
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return entry, nil
	}
	if err != nil {
		return Entry{}, fmt.Errorf("projection: inspect %s: %w", asset.path, err)
	}
	if !info.Mode().IsRegular() {
		entry.State = "divergent"
		entry.CurrentSHA256 = "non-regular"
		return entry, nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return Entry{}, fmt.Errorf("projection: read %s: %w", asset.path, err)
	}
	entry.CurrentSHA256 = digest(data)
	entry.State = "divergent"
	if entry.CurrentSHA256 == entry.DesiredSHA256 {
		entry.State = "current"
	}
	return entry, nil
}

func planDigest(plan Plan) (string, error) {
	copyPlan := plan
	copyPlan.Digest = ""
	data, err := json.Marshal(copyPlan)
	if err != nil {
		return "", fmt.Errorf("projection: encode plan: %w", err)
	}
	return digest(data), nil
}

type stagedFile struct {
	parent     *parentRef
	target     string
	targetBase string
	stage      string
	stageBase  string
}

type parentRef struct {
	path string
	file *os.File
	info os.FileInfo
}

func openParent(path string) (*parentRef, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	parent := &parentRef{path: path, file: file, info: info}
	if err := parent.validate(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return parent, nil
}

func (p *parentRef) validate() error {
	opened, err := p.file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(p.path)
	if err != nil {
		return err
	}
	if pathEntryUnsafe(p.path, current) || !current.IsDir() {
		return fmt.Errorf("%w: target parent changed: %s", ErrPlanChanged, p.path)
	}
	if !os.SameFile(p.info, opened) || !os.SameFile(p.info, current) {
		return fmt.Errorf("%w: target parent changed: %s", ErrPlanChanged, p.path)
	}
	return nil
}

func stage(root string, assets []normalizedAsset, entries []Entry) ([]stagedFile, error) {
	staged := make([]stagedFile, 0, len(assets))
	for i, asset := range assets {
		if entries[i].State == "current" {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(asset.path))
		if err := makeParents(root, filepath.Dir(target)); err != nil {
			removeStages(staged)
			return nil, fmt.Errorf("projection: create target directory: %w", err)
		}
		parent, err := openParent(filepath.Dir(target))
		if err != nil {
			removeStages(staged)
			return nil, fmt.Errorf("projection: open target directory: %w", err)
		}
		file, err := os.CreateTemp(parent.path, ".codexguard-stage-*")
		if err != nil {
			_ = parent.file.Close()
			removeStages(staged)
			return nil, fmt.Errorf("projection: stage %s: %w", asset.path, err)
		}
		stagePath := file.Name()
		if err := writeStage(file, asset.data); err != nil {
			_ = os.Remove(stagePath)
			_ = parent.file.Close()
			removeStages(staged)
			return nil, fmt.Errorf("projection: stage %s: %w", asset.path, err)
		}
		if err := parent.validate(); err != nil {
			_ = os.Remove(stagePath)
			_ = parent.file.Close()
			removeStages(staged)
			return nil, fmt.Errorf("projection: stage %s: %w", asset.path, err)
		}
		staged = append(staged, stagedFile{
			parent:     parent,
			target:     target,
			targetBase: filepath.Base(target),
			stage:      stagePath,
			stageBase:  filepath.Base(stagePath),
		})
	}
	return staged, nil
}

func writeStage(file *os.File, data []byte) error {
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func commit(staged []stagedFile, afterPublish func(int, stagedFile)) ([]stagedFile, error) {
	created := make([]stagedFile, 0, len(staged))
	for i, file := range staged {
		if err := file.parent.validate(); err != nil {
			return created, fmt.Errorf("projection: target changed before apply: %s: %w", file.target, err)
		}
		if err := publishExclusive(file); err != nil {
			if errors.Is(err, os.ErrExist) {
				err = fmt.Errorf("%w: target already exists: %v", ErrPlanChanged, err)
			}
			return created, fmt.Errorf("projection: target changed before apply: %s: %w", file.target, err)
		}
		created = append(created, file)
		if afterPublish != nil {
			afterPublish(i, file)
		}
		if err := file.parent.validate(); err != nil {
			return created, fmt.Errorf("projection: target changed during apply: %s: %w", file.target, err)
		}
	}
	return created, nil
}

func rollback(created []stagedFile) {
	for _, file := range created {
		_ = removePublished(file)
	}
}

func removeStages(staged []stagedFile) {
	for _, file := range staged {
		_ = removeStaged(file)
		_ = file.parent.file.Close()
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func checkParents(root, parent string) error {
	rel, err := filepath.Rel(root, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("target parent escapes Codex home")
	}
	for _, path := range pathAncestors(parent) {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if pathEntryUnsafe(path, info) {
			return fmt.Errorf("%w: linked target ancestor: %s", ErrUnsafePath, path)
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: target ancestor is not a directory: %s", ErrUnsafePath, path)
		}
	}
	return nil
}

func pathAncestors(path string) []string {
	var reversed []string
	current := filepath.Clean(path)
	for {
		reversed = append(reversed, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	paths := make([]string, len(reversed))
	for i := range reversed {
		paths[len(reversed)-1-i] = reversed[i]
	}
	return paths
}

func makeParents(root, parent string) error {
	if err := checkParents(root, parent); err != nil {
		return err
	}
	rel, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	current := root
	parts := []string{"."}
	if rel != "." {
		parts = append(parts, strings.Split(rel, string(filepath.Separator))...)
	}
	for _, part := range parts {
		if part != "." {
			current = filepath.Join(current, part)
		}
		err := os.Mkdir(current, 0o700)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if pathEntryUnsafe(current, info) || !info.IsDir() {
			return fmt.Errorf("%w: target parent: %s", ErrUnsafePath, current)
		}
	}
	return nil
}
