package evidence

import (
	"crypto/sha1" // Git blob identity, not an authorization signature.
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

// ExactSource reads one regular UTF-8 file at an immutable commit and verifies
// GitHub's blob identity. It never reads an author's checkout or a moving ref.
func ExactSource(repo, head, name string) (string, string, error) {
	if len(head) != 40 || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00\r\n") {
		return "", "", fmt.Errorf("evidence_source_invalid: head or path")
	}
	if _, err := hex.DecodeString(head); err != nil {
		return "", "", fmt.Errorf("evidence_source_invalid: head")
	}
	segments := strings.Split(name, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	raw, err := gh("api", "repos/"+repo+"/contents/"+strings.Join(segments, "/")+"?ref="+head)
	if err != nil {
		return "", "", err
	}
	return decodeExactSource(raw, name)
}

func decodeExactSource(raw []byte, name string) (string, string, error) {
	var file struct {
		Type, Path, SHA, Encoding, Content string
		Size                               int
		Target                             string
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return "", "", fmt.Errorf("evidence_source_invalid: %w", err)
	}
	if file.Type != "file" || file.Path != name || file.Encoding != "base64" || file.Target != "" || file.Size > 256*1024 {
		return "", "", fmt.Errorf("evidence_source_invalid: expected bounded regular file %s", name)
	}
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil || len(content) != file.Size || strings.ContainsRune(string(content), 0) || !utf8.Valid(content) {
		return "", "", fmt.Errorf("evidence_source_invalid: content %s", name)
	}
	sum := sha1.Sum(append([]byte(fmt.Sprintf("blob %d%c", len(content), 0)), content...))
	if hex.EncodeToString(sum[:]) != file.SHA {
		return "", "", fmt.Errorf("evidence_source_invalid: blob hash %s", name)
	}
	return string(content), file.SHA, nil
}

// CurrentHead rechecks the live PR before attaching supplemental evidence.
func CurrentHead(repo string, number int) (string, error) {
	raw, err := gh("api", fmt.Sprintf("repos/%s/pulls/%d", repo, number))
	if err != nil {
		return "", err
	}
	var pr struct{ Head struct{ SHA string } }
	if err := json.Unmarshal(raw, &pr); err != nil || pr.Head.SHA == "" {
		return "", fmt.Errorf("evidence_source_invalid: PR head unavailable")
	}
	return pr.Head.SHA, nil
}

// ExactPaths returns Git's complete blob-path index at an immutable head. It
// distinguishes real unchanged companions from hypothetical examples in prose.
func ExactPaths(repo, head string) ([]string, error) {
	if len(head) != 40 {
		return nil, fmt.Errorf("evidence_source_invalid: head")
	}
	if _, err := hex.DecodeString(head); err != nil {
		return nil, fmt.Errorf("evidence_source_invalid: head")
	}
	raw, err := gh("api", "repos/"+repo+"/git/trees/"+head+"?recursive=1")
	if err != nil {
		return nil, err
	}
	return decodeExactPaths(raw)
}

func decodeExactPaths(raw []byte) ([]string, error) {
	var tree struct {
		SHA       string
		Truncated *bool
		Tree      []struct{ Path, Type string }
	}
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("evidence_index_invalid: %w", err)
	}
	if len(tree.SHA) != 40 || tree.Truncated == nil || *tree.Truncated || tree.Tree == nil {
		return nil, fmt.Errorf("evidence_index_incomplete: GitHub did not return a complete tree")
	}
	paths := []string{}
	for _, entry := range tree.Tree {
		if entry.Type == "blob" {
			paths = append(paths, entry.Path)
		}
	}
	return paths, nil
}
