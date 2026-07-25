package rooms

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	envImage       = "RUNWAY_ROOMS_IMAGE"
	envModel       = "RUNWAY_ROOMS_MODEL"
	envBin         = "RUNWAY_ROOMS_BIN"
	envTapGateway  = "RUNWAY_ROOMS_TAP_GATEWAY"
	envTapSource   = "RUNWAY_ROOMS_TAP_SOURCE"
	envWitness     = "RUNWAY_ROOMS_WITNESS"
	envEgressExtra = "RUNWAY_ROOMS_EGRESS_EXTRA"
)

const defaultModel = "composer-2.5"

// Config is the resolved agent-cursor placement profile. Launcher and Prefix
// describe one non-shell argv: normally `sudo -E rooms`, while tests inject a
// hermetic helper executable.
type Config struct {
	Launcher string
	Prefix   []string
	Image    string
	Model    string
	Poll     time.Duration
	// TapGateway is the profile-shaped custody base URL host the guest reaches
	// custody at (D6), e.g. "http://172.30.0.1:8127"; CUSTODY_BASE_<KEY> is this
	// plus "/<key>". TapSource is the transport source a derived child is bound
	// to (D2b). Both come from the placement profile, never compiled-in.
	TapGateway string
	TapSource  string
	// Witness runs each placement under `rooms --witness` (default on), so the
	// room's egress is recorded host-side into witness.json/witness.pcap and
	// digested into the room-authority receipt. It fails the room closed on a
	// host without tcpdump, so it is an opt-out (RUNWAY_ROOMS_WITNESS=0).
	Witness bool
	// EgressExtra are additional allowlist destinations (hostnames or IP/CIDR
	// literals) appended to the enforced egress allowlist when a custody ref is
	// present — the escape hatch for a host whose IPs rotate past rooms' launch-
	// time DNS pin (e.g. a pinned git-host CIDR, or the guest's DNS resolver).
	// From RUNWAY_ROOMS_EGRESS_EXTRA, comma-separated.
	EgressExtra []string
}

// ConfigFromEnvironment resolves the one installed Rooms profile. Host paths
// stay here; placed requests carry only the logical `agent-cursor` name.
func ConfigFromEnvironment() (Config, error) {
	roomsBin := os.Getenv(envBin)
	if roomsBin == "" {
		roomsBin = "rooms"
	}
	image := os.Getenv(envImage)
	if image == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Config{}, fmt.Errorf("rooms: resolve home for default image: %w", err)
		}
		image = filepath.Join(home, "rooms", "images", "agent-alpine-cursor.ext4")
	}
	model := os.Getenv(envModel)
	if model == "" {
		model = defaultModel
	}
	return Config{
		Launcher:    "sudo",
		Prefix:      []string{"-E", roomsBin},
		Image:       image,
		Model:       model,
		Poll:        10 * time.Millisecond,
		TapGateway:  os.Getenv(envTapGateway),
		TapSource:   os.Getenv(envTapSource),
		Witness:     witnessEnabled(),
		EgressExtra: splitList(os.Getenv(envEgressExtra)),
	}, nil
}

// splitList splits a comma-separated env value into trimmed, non-empty entries.
// An unset or all-blank value yields nil, so the egress allowlist appends
// nothing rather than a stray empty destination rooms would reject.
func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	out := make([]string, 0)
	for _, entry := range strings.Split(value, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// witnessEnabled reports whether placements run under `rooms --witness` (default
// on). Set RUNWAY_ROOMS_WITNESS to a falsey value (0/false/off/no) to disable it
// — e.g. on a host without tcpdump, where --witness fails the room closed.
func witnessEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envWitness))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// custodyBase renders the guest-facing CUSTODY_BASE_<KEY> value for key from the
// profile's tap gateway (D6): "<gateway>/<key>". An unset gateway yields an
// empty base — the guest then has no reachable custody endpoint and vendor calls
// fail closed, never fall back.
func (c Config) custodyBase(key string) string {
	if c.TapGateway == "" {
		return ""
	}
	return strings.TrimRight(c.TapGateway, "/") + "/" + key
}

// tapSource is the transport source a derived child is bound to (D2b).
func (c Config) tapSource() string { return c.TapSource }

// tapGatewayHost is the bare host of the custody tap gateway — the destination
// the egress allowlist must permit so guest vendor calls reach the proxy. It
// strips the scheme and port from TapGateway ("http://172.30.0.1:8127" ->
// "172.30.0.1"); an unset or unparseable gateway yields "" so the caller omits
// the entry rather than allowlisting a garbage destination.
func (c Config) tapGatewayHost() string {
	if c.TapGateway == "" {
		return ""
	}
	parsed, err := url.Parse(c.TapGateway)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	return parsed.Hostname()
}

func (c Config) validate() error {
	if c.Launcher == "" {
		return fmt.Errorf("rooms: launcher is empty")
	}
	if c.Image == "" {
		return fmt.Errorf("rooms: profile image is empty")
	}
	if c.Model == "" {
		return fmt.Errorf("rooms: profile model is empty")
	}
	return nil
}

func (c Config) pollInterval() time.Duration {
	if c.Poll <= 0 {
		return 10 * time.Millisecond
	}
	return c.Poll
}
