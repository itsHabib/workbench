package rooms

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	envImage = "RUNWAY_ROOMS_IMAGE"
	envModel = "RUNWAY_ROOMS_MODEL"
	envBin   = "RUNWAY_ROOMS_BIN"
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
		Launcher: "sudo",
		Prefix:   []string{"-E", roomsBin},
		Image:    image,
		Model:    model,
		Poll:     10 * time.Millisecond,
	}, nil
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
