package bridge

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// Config is the part of the CLI configuration file that the bridge and the hook use. When
// the file or a field is not there, the default value applies.
type Config struct {
	Instance string `json:"instance"`
	Port     int    `json:"port"`
	Image    string `json:"image"`
	DataDir  string `json:"data_dir"`
}

// DefaultConfig is the configuration when the file has no value.
func DefaultConfig() Config {
	return Config{Instance: "mai", Port: 8765}
}

// LoadConfig reads <configDir>/config.json.
func LoadConfig(configDir string) (Config, error) {
	c := DefaultConfig()
	raw, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	var f Config
	if err := json.Unmarshal(raw, &f); err != nil {
		return c, errors.New("bridge: bad config.json: " + err.Error())
	}
	if f.Instance != "" {
		c.Instance = f.Instance
	}
	if f.Port > 0 && f.Port < 65536 {
		c.Port = f.Port
	}
	if f.Image != "" {
		c.Image = f.Image
	}
	c.DataDir = f.DataDir
	return c, nil
}

// Endpoint is the base URL of the HTTP API of the server.
func (c Config) Endpoint() string { return "http://127.0.0.1:" + strconv.Itoa(c.Port) }
