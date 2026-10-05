// Package config is the settings file shared by the CLI and the desktop UI.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// MaxConns is the per-network connection cap (aria2c stops at 16).
const MaxConns = 64

type Config struct {
	Dir      string   `json:"dir"`      // download folder
	Networks []string `json:"networks"` // interface ids; empty = every physical network
	Conns    int      `json:"conns"`    // connections per network
}

func Path() string {
	if p := os.Getenv("MNDL_CONFIG"); p != "" {
		return p
	}
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "multinet-dl", "config.json")
}

func DefaultDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(h, "Downloads")
}

func Load() Config {
	c := Config{Dir: DefaultDir(), Conns: 8}
	if b, err := os.ReadFile(Path()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Dir == "" {
		c.Dir = DefaultDir()
	}
	if c.Conns < 1 || c.Conns > MaxConns {
		c.Conns = 8
	}
	return c
}

func Save(c Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
