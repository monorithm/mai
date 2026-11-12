// Package api is the client of the owner HTTP API of the mai server. It reads the
// configuration file of the CLI and the owner key from the keychain. It never prints or logs
// a key.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/monorithm/mai/cli/v3/internal/version"
)

// ConfigFile is the name of the configuration file in the configuration folder.
const ConfigFile = "config.json"

// Config is the configuration file of the CLI. A missing file or a missing field gives the
// default value.
type Config struct {
	Instance string `json:"instance"`
	Port     int    `json:"port"`
	Image    string `json:"image"`
	DataDir  string `json:"data_dir"`
	Approval string `json:"approval"`
}

// DefaultConfig returns the configuration with each default value.
func DefaultConfig() Config {
	return Config{
		Instance: "mai",
		Port:     8765,
		Image:    "ghcr.io/monorithm/mai:" + version.Version,
		DataDir:  "",
		Approval: "each-bridge",
	}
}

// LoadConfig reads config.json in the configuration folder dir.
func LoadConfig(dir string) (Config, error) {
	c := DefaultConfig()
	b, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	var f Config
	if err := json.Unmarshal(b, &f); err != nil {
		return c, fmt.Errorf("api: %s: %w", ConfigFile, err)
	}
	if f.Instance != "" {
		c.Instance = f.Instance
	}
	if f.Port != 0 {
		c.Port = f.Port
	}
	if f.Image != "" {
		c.Image = f.Image
	}
	if f.DataDir != "" {
		c.DataDir = f.DataDir
	}
	if f.Approval != "" {
		c.Approval = f.Approval
	}
	return c, nil
}

// BaseURL is the address of the server for this configuration.
func (c Config) BaseURL() string { return fmt.Sprintf("http://127.0.0.1:%d", c.Port) }
