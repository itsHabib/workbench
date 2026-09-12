// Package provider supplies the installed provider transport, never scheduling.
package provider

import (
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
)

//go:embed runtime.mjs
var bridge string

// Command runs one durable provider turn. The request is written to path, a new
// private file, before the bridge exists; argv carries only that path, never the
// prompt. Nothing is left for the launcher to copy after Start, so the bridge gets
// its whole request even when the launcher exits the moment Start returns.
func Command(path string, request map[string]any) (*exec.Cmd, error) {
	if runtime.GOOS == "darwin" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		request["process_observer"] = exe
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if err := writeNew(path, raw); err != nil {
		return nil, err
	}
	return exec.Command("node", "--input-type=module", "-e", bridge, path), nil
}

// writeNew creates path for one attempt only: an existing file, or a link planted
// in its place, is refused rather than followed or reused.
func writeNew(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
