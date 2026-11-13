package uninstall

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Config is the part of config.json that the uninstall reads. A missing file or field gives
// the default value.
type Config struct {
	Instance string `json:"instance"`
	Port     int    `json:"port"`
	Image    string `json:"image"`
	DataDir  string `json:"data_dir"`
}

// ConfigFile is the name of the configuration file in the configuration folder.
const ConfigFile = "config.json"

// DefaultInstance is the instance of a command with no --instance and no config.json.
const DefaultInstance = "mai"

// LoadConfig reads <dir>/config.json and sets the defaults.
func LoadConfig(dir string) (Config, error) {
	c := Config{}
	b, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			return Config{}, err
		}
	}
	if c.Instance == "" {
		c.Instance = DefaultInstance
	}
	if c.Port == 0 {
		c.Port = 8765
	}
	return c, nil
}

// DataVolume is the name of the data volume of an instance.
func DataVolume(instance string) string { return instance + "3-data" }

// SpoolVolume is the name of the spool volume of an instance.
func SpoolVolume(instance string) string { return instance + "3-spool" }
