// Package config loads and validates the routes file — flare's whole policy
// surface. Watched paths live here, never derived in code, so the config is
// the only place that knows where producers keep their logs.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Version is the routes-file major this binary understands. An unknown
// version is refused, never best-effort parsed.
const Version = 1

// Source kinds name the parser a watched file gets; channel types name the
// delivery mechanism. ChannelDrop is explicit silence — the only way a
// matched event goes nowhere.
const (
	SourceGateLog      = "gate-log"
	SourceShipReceipts = "ship-receipts"

	ChannelToast   = "toast"
	ChannelWebhook = "webhook"
	ChannelSlack   = "slack"
	ChannelDrop    = "drop"
)

// Source is one watched producer log: an explicit absolute path, never a
// derived one.
type Source struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// Channel is a named delivery target events route to.
type Channel struct {
	Type    string `json:"type"`
	URL     string `json:"url,omitempty"`
	Token   string `json:"token,omitempty"`
	Channel string `json:"channel,omitempty"`
}

// Match selects events. Every set field must match; omitted means any.
// Values are exact strings with "|" alternation.
type Match struct {
	Source   string `json:"source,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Decision string `json:"decision,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	Code     string `json:"code,omitempty"`
}

// Route sends matching events to one channel, optionally throttled to at
// most one delivery per window (a strictly worse event still passes).
type Route struct {
	Match           Match  `json:"match"`
	Channel         string `json:"channel"`
	ThrottleSeconds int    `json:"throttle_seconds,omitempty"`
}

// Config is the whole routes file. CatchAll is required: an event matching
// no route must still notify somewhere.
type Config struct {
	Version     int                `json:"version"`
	PollSeconds int                `json:"poll_seconds,omitempty"`
	Sources     []Source           `json:"sources"`
	Channels    map[string]Channel `json:"channels"`
	Routes      []Route            `json:"routes"`
	CatchAll    string             `json:"catch_all"`
}

// Load reads and validates a routes file. Unknown fields and unknown
// versions are refused, never best-effort parsed.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	if c.PollSeconds == 0 {
		c.PollSeconds = 60
	}
	return c, nil
}

func (c Config) validate() error {
	if c.Version != Version {
		return fmt.Errorf("version %d not supported (want %d)", c.Version, Version)
	}
	if len(c.Sources) == 0 {
		return errors.New("no sources")
	}
	if c.CatchAll == "" {
		return errors.New("catch_all channel is required: an unrouted event must not be silently dropped")
	}
	if c.catchAllDrops() {
		return errors.New("catch_all must notify somewhere: it cannot be a drop channel, or an unrouted event reads as calm")
	}
	if err := c.checkChannel(c.CatchAll); err != nil {
		return fmt.Errorf("catch_all: %w", err)
	}
	names := map[string]bool{}
	for _, s := range c.Sources {
		if err := checkSource(s); err != nil {
			return err
		}
		if names[s.Name] {
			return fmt.Errorf("duplicate source name %q", s.Name)
		}
		names[s.Name] = true
	}
	for name, ch := range c.Channels {
		if err := checkChannelDef(name, ch); err != nil {
			return err
		}
	}
	for i, r := range c.Routes {
		if err := c.checkChannel(r.Channel); err != nil {
			return fmt.Errorf("route %d: %w", i, err)
		}
	}
	return nil
}

func checkSource(s Source) error {
	if s.Name == "" || s.Path == "" {
		return fmt.Errorf("source %+v: name and path are required", s)
	}
	if s.Kind != SourceGateLog && s.Kind != SourceShipReceipts {
		return fmt.Errorf("source %q: unknown kind %q", s.Name, s.Kind)
	}
	return nil
}

func checkChannelDef(name string, ch Channel) error {
	switch ch.Type {
	case ChannelToast, ChannelDrop:
		return nil
	case ChannelWebhook:
		if ch.URL == "" {
			return fmt.Errorf("channel %q: webhook requires url", name)
		}
		return nil
	case ChannelSlack:
		if ch.Token == "" {
			return fmt.Errorf("channel %q: slack requires token", name)
		}
		if ch.Channel == "" {
			return fmt.Errorf("channel %q: slack requires channel", name)
		}
		return nil
	}
	return fmt.Errorf("channel %q: unknown type %q", name, ch.Type)
}

func (c Config) checkChannel(name string) error {
	if name == ChannelDrop {
		return nil
	}
	if _, ok := c.Channels[name]; !ok {
		return fmt.Errorf("channel %q not defined", name)
	}
	return nil
}

// catchAllDrops reports whether the configured catch_all would silence events
// instead of notifying — the literal drop channel, or a configured channel of
// type drop. The catch_all exists so an unrouted event still pages; a dropping
// catch_all would defeat that invariant.
func (c Config) catchAllDrops() bool {
	if c.CatchAll == ChannelDrop {
		return true
	}
	return c.Channels[c.CatchAll].Type == ChannelDrop
}
