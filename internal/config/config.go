// Package config resolves ProxyPilot settings.
//
// Precedence (highest first): command-line flag > PROXYPILOT_* env > config.yaml > defaults.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Proxy  ProxyConfig  `yaml:"proxy"`
	API    APIConfig    `yaml:"api"`
	Store  StoreConfig  `yaml:"store"`
	SSL    SSLConfig    `yaml:"ssl"`
	Log    LogConfig    `yaml:"log"`
	Redact RedactConfig `yaml:"redact"`
	// Android tools (adb).
	Android AndroidConfig `yaml:"android"`

	// DataDir is not read from the file; it decides where the file lives.
	DataDir string `yaml:"-"`
	// File is the config file that was loaded ("" when none existed).
	File string `yaml:"-"`
}

type AndroidConfig struct {
	// ADB is the adb binary; empty = $ANDROID_HOME, ~/Library/Android/sdk, then PATH.
	ADB string `yaml:"adb"`
}

type ProxyConfig struct {
	Listen string `yaml:"listen"`
	// DisableH2 keeps intercepted traffic on HTTP/1.1 on both sides.
	DisableH2 bool `yaml:"disable_h2"`
}

type APIConfig struct {
	Listen string `yaml:"listen"`
}

type StoreConfig struct {
	MaxEntries      int `yaml:"max_entries"`
	InlineBodyLimit int `yaml:"inline_body_limit"`
}

type SSLConfig struct {
	// Mode is all | allowlist | off.
	Mode      string   `yaml:"mode"`
	Allowlist []string `yaml:"allowlist"`
	Denylist  []string `yaml:"denylist"`
}

type RedactConfig struct {
	// Headers are masked in addition to the built-in list.
	Headers []string `yaml:"headers"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"` // text | json
}

// DefaultDenylist keeps Apple system services working while intercepting.
var DefaultDenylist = []string{"*.apple.com", "*.icloud.com", "*.mzstatic.com", "*.push.apple.com"}

func Defaults() Config {
	return Config{
		Proxy: ProxyConfig{Listen: "127.0.0.1:9090"},
		API:   APIConfig{Listen: "127.0.0.1:9091"},
		Store: StoreConfig{MaxEntries: 10000, InlineBodyLimit: 256 * 1024},
		SSL:   SSLConfig{Mode: "all", Denylist: append([]string(nil), DefaultDenylist...)},
		Log:   LogConfig{Level: "info", Format: "text"},
	}
}

// Overrides holds values set by command-line flags. nil means "not set".
type Overrides struct {
	ConfigFile  *string
	DataDir     *string
	ProxyListen *string
	APIListen   *string
	LogLevel    *string
	LogFormat   *string
}

// Env abstracts os.LookupEnv so tests can inject values.
type Env func(key string) (string, bool)

// Load resolves the configuration.
func Load(o Overrides, env Env) (Config, error) {
	if env == nil {
		env = os.LookupEnv
	}
	cfg := Defaults()

	dir, err := resolveDataDir(o, env)
	if err != nil {
		return cfg, err
	}
	cfg.DataDir = dir

	file := filepath.Join(dir, "config.yaml")
	explicit := false
	if v, ok := env("PROXYPILOT_CONFIG"); ok && v != "" {
		file, explicit = v, true
	}
	if o.ConfigFile != nil && *o.ConfigFile != "" {
		file, explicit = *o.ConfigFile, true
	}
	if err := readFile(file, &cfg); err != nil {
		if !errors.Is(err, fs.ErrNotExist) || explicit {
			return cfg, fmt.Errorf("config %s: %w", file, err)
		}
	} else {
		cfg.File = file
	}

	if err := applyEnv(&cfg, env); err != nil {
		return cfg, err
	}
	applyOverrides(&cfg, o)
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	switch c.SSL.Mode {
	case "all", "allowlist", "off":
	default:
		return fmt.Errorf("ssl.mode must be all, allowlist or off (got %q)", c.SSL.Mode)
	}
	if c.Store.MaxEntries <= 0 {
		return fmt.Errorf("store.max_entries must be > 0")
	}
	if c.Store.InlineBodyLimit < 0 {
		return fmt.Errorf("store.inline_body_limit must be >= 0")
	}
	return nil
}

func resolveDataDir(o Overrides, env Env) (string, error) {
	if o.DataDir != nil && *o.DataDir != "" {
		return *o.DataDir, nil
	}
	if v, ok := env("PROXYPILOT_HOME"); ok && v != "" {
		return v, nil
	}
	return DefaultDataDir(env)
}

// DefaultDataDir is ~/Library/Application Support/ProxyPilot on macOS and
// $XDG_DATA_HOME/proxypilot (or ~/.local/share/proxypilot) elsewhere.
func DefaultDataDir(env Env) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "ProxyPilot"), nil
	}
	if v, ok := env("XDG_DATA_HOME"); ok && v != "" {
		return filepath.Join(v, "proxypilot"), nil
	}
	return filepath.Join(home, ".local", "share", "proxypilot"), nil
}

func readFile(path string, cfg *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, cfg)
}

func applyEnv(cfg *Config, env Env) error {
	str := func(key string, dst *string) {
		if v, ok := env(key); ok && v != "" {
			*dst = v
		}
	}
	num := func(key string, dst *int) error {
		v, ok := env(key)
		if !ok || v == "" {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = n
		return nil
	}
	str("PROXYPILOT_PROXY_LISTEN", &cfg.Proxy.Listen)
	str("PROXYPILOT_API_LISTEN", &cfg.API.Listen)
	str("PROXYPILOT_SSL_MODE", &cfg.SSL.Mode)
	str("PROXYPILOT_LOG_LEVEL", &cfg.Log.Level)
	str("PROXYPILOT_LOG_FORMAT", &cfg.Log.Format)
	str("PROXYPILOT_ADB", &cfg.Android.ADB)
	if err := num("PROXYPILOT_STORE_MAX_ENTRIES", &cfg.Store.MaxEntries); err != nil {
		return err
	}
	return num("PROXYPILOT_STORE_INLINE_BODY_LIMIT", &cfg.Store.InlineBodyLimit)
}

func applyOverrides(cfg *Config, o Overrides) {
	set := func(src *string, dst *string) {
		if src != nil && *src != "" {
			*dst = *src
		}
	}
	set(o.ProxyListen, &cfg.Proxy.Listen)
	set(o.APIListen, &cfg.API.Listen)
	set(o.LogLevel, &cfg.Log.Level)
	set(o.LogFormat, &cfg.Log.Format)
}

// EnsureDataDir creates the data directory with 0700 permissions.
func (c Config) EnsureDataDir() error {
	return os.MkdirAll(c.DataDir, 0o700)
}
