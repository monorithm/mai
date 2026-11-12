// Package config reads and writes the configuration file of the CLI, config.json in the
// configuration folder. The file holds no secret.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/monorithm/mai/cli/internal/version"
)

// FileName is the name of the configuration file in the configuration folder.
const FileName = "config.json"

// Default values of the fields.
const (
	DefaultInstance = "mai"
	DefaultPort     = 8765
	DefaultApproval = "each-bridge"
	ImageRepository = "ghcr.io/monorithm/mai"
)

// Config is the content of config.json.
type Config struct {
	Instance string `json:"instance"`
	Port     int    `json:"port"`
	Image    string `json:"image"`
	DataDir  string `json:"data_dir"`
	Approval string `json:"approval"`
}

// DefaultImage is the image of the version of the CLI.
func DefaultImage() string { return ImageRepository + ":" + version.Version }

// Defaults returns a Config with the default value in each field.
func Defaults() Config {
	return Config{
		Instance: DefaultInstance,
		Port:     DefaultPort,
		Image:    DefaultImage(),
		DataDir:  "",
		Approval: DefaultApproval,
	}
}

// Path returns the location of config.json in the configuration folder dir.
func Path(dir string) string { return filepath.Join(dir, FileName) }

// fill puts the default value in each empty field.
func (c *Config) fill() {
	d := Defaults()
	if c.Instance == "" {
		c.Instance = d.Instance
	}
	if c.Port == 0 {
		c.Port = d.Port
	}
	if c.Image == "" {
		c.Image = d.Image
	}
	if c.Approval == "" {
		c.Approval = d.Approval
	}
}

// Load reads config.json from the configuration folder dir. A missing file or a missing
// field gives the default value.
func Load(dir string) (Config, error) {
	c := Config{}
	b, err := os.ReadFile(Path(dir))
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Config{}, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	c.fill()
	return c, nil
}

// Save writes c to config.json in the configuration folder dir, with mode 0600.
func Save(dir string, c Config) error {
	c.fill()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path(dir))
}

// Update applies o to the fields of the file in dir and writes the file. A field that the
// file does not hold stays out of the file, so that its default can change with the CLI.
func Update(dir string, o Overrides) error {
	return update(dir, func(raw *Config) { *raw = raw.Apply(o) })
}

// PinImage writes the image field of the file. An empty image removes the pin, so that the
// CLI uses the image of its version.
func PinImage(dir, image string) error {
	return update(dir, func(raw *Config) { raw.Image = image })
}

func update(dir string, change func(*Config)) error {
	raw := Config{}
	b, err := os.ReadFile(Path(dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
	}
	change(&raw)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path(dir))
}

// Overrides are the values of command flags. An empty field keeps the value of the file.
type Overrides struct {
	Instance string
	Port     int
	Image    string
	DataDir  string
}

// Apply returns c with each value of o that is set.
func (c Config) Apply(o Overrides) Config {
	if o.Instance != "" {
		c.Instance = o.Instance
	}
	if o.Port != 0 {
		c.Port = o.Port
	}
	if o.Image != "" {
		c.Image = o.Image
	}
	if o.DataDir != "" {
		c.DataDir = o.DataDir
	}
	return c
}
