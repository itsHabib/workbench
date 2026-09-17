package plane

import (
	"fmt"
	"strings"
)

// OpenStore opens a store from a spec: "file:DIR", "resp:HOST:PORT/PREFIX"
// or "mem:". A spec is what crosses a process boundary, so worker processes
// and the harness agree on one store.
func OpenStore(spec string) (Store, error) {
	switch {
	case strings.HasPrefix(spec, "file:"):
		return OpenFile(strings.TrimPrefix(spec, "file:"))
	case strings.HasPrefix(spec, "resp:"):
		rest := strings.TrimPrefix(spec, "resp:")
		addr, prefix, _ := strings.Cut(rest, "/")
		return OpenRESP(addr, prefix)
	case spec == "mem:":
		return NewMem(), nil
	}
	return nil, fmt.Errorf("plane: store spec %q is not file:DIR or resp:HOST:PORT/PREFIX", spec)
}
