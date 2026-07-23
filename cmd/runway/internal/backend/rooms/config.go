package rooms

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	envImage      = "RUNWAY_ROOMS_IMAGE"
	envModel      = "RUNWAY_ROOMS_MODEL"
	envBin        = "RUNWAY_ROOMS_BIN"
	envTapGateway = "RUNWAY_ROOMS_TAP_GATEWAY"
	envTapSource  = "RUNWAY_ROOMS_TAP_SOURCE"
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
		Launcher:   "sudo",
		Prefix:     []string{"-E", roomsBin},
		Image:      image,
		Model:      model,
		Poll:       10 * time.Millisecond,
		TapGateway: os.Getenv(envTapGateway),
		TapSource:  os.Getenv(envTapSource),
	}, nil
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
