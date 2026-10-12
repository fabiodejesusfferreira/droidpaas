// Package config carrega e valida o droidd.toml (TSD §6.2).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Duration aceita strings como "15m" ou "30s" no TOML.
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	*d = Duration(v)
	return err
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

func (d Duration) D() time.Duration { return time.Duration(d) }

type Config struct {
	Paths struct {
		Home string `toml:"home"`
	} `toml:"paths"`
	HTTP struct {
		PanelAddr      string   `toml:"panel_addr"`
		TrustedProxies []string `toml:"trusted_proxies"`
		SecureCookies  string   `toml:"secure_cookies"`
	} `toml:"http"`
	Proxy struct {
		Listen        string `toml:"listen"`
		Admin         string `toml:"admin"`
		BaseDomain    string `toml:"base_domain"`
		PanelHostname string `toml:"panel_hostname"`
	} `toml:"proxy"`
	Runit struct {
		ServiceDir string `toml:"service_dir"`
		Prefix     string `toml:"prefix"`
	} `toml:"runit"`
	Ports struct {
		RangeStart int `toml:"range_start"`
		RangeEnd   int `toml:"range_end"`
	} `toml:"ports"`
	Build struct {
		MaxConcurrent int      `toml:"max_concurrent"`
		Timeout       Duration `toml:"timeout"`
		Nice          int      `toml:"nice"` // sem efeito no Android 13 (PoC V-09)
		KeepLogsDays  int      `toml:"keep_logs_days"`
	} `toml:"build"`
	Deploy struct {
		KeepReleases   int      `toml:"keep_releases"`
		HealthTimeout  Duration `toml:"health_timeout"`
		HealthInterval Duration `toml:"health_interval"`
	} `toml:"deploy"`
	Git struct {
		SSHEnabled     bool   `toml:"ssh_enabled"`
		HTTPEnabled    bool   `toml:"http_enabled"`
		AutoCreateApps bool   `toml:"auto_create_apps"`
		AuthorizedKeys string `toml:"authorized_keys"`
	} `toml:"git"`
	Reconcile struct {
		Interval           Duration `toml:"interval"`
		CrashLoopWindow    Duration `toml:"crash_loop_window"`
		CrashLoopThreshold int      `toml:"crash_loop_threshold"`
	} `toml:"reconcile"`
	Logging struct {
		Level  string `toml:"level"`
		Format string `toml:"format"`
	} `toml:"logging"`
}

// Default devolve os valores do TSD v0.2 (portas do proxy fora das comuns, §2.5).
func Default() Config {
	var c Config
	c.Paths.Home = "~/droidpaas"
	c.HTTP.PanelAddr = "127.0.0.1:8090"
	c.HTTP.TrustedProxies = []string{"127.0.0.1"}
	c.HTTP.SecureCookies = "auto"
	c.Proxy.Listen = "127.0.0.1:18080"
	c.Proxy.Admin = "127.0.0.1:12019"
	c.Runit.ServiceDir = "$PREFIX/var/service"
	c.Runit.Prefix = "dp-"
	c.Ports.RangeStart, c.Ports.RangeEnd = 20000, 29999
	c.Build.MaxConcurrent = 1
	c.Build.Timeout = Duration(15 * time.Minute)
	c.Build.Nice = 10
	c.Build.KeepLogsDays = 30
	c.Deploy.KeepReleases = 5
	c.Deploy.HealthTimeout = Duration(30 * time.Second)
	c.Deploy.HealthInterval = Duration(time.Second)
	c.Git.SSHEnabled, c.Git.HTTPEnabled = true, true
	c.Git.AuthorizedKeys = "~/.ssh/authorized_keys"
	c.Reconcile.Interval = Duration(10 * time.Second)
	c.Reconcile.CrashLoopWindow = Duration(60 * time.Second)
	c.Reconcile.CrashLoopThreshold = 5
	c.Logging.Level, c.Logging.Format = "info", "json"
	return c
}

// Load lê o arquivo sobre os defaults, expande ~/$PREFIX/$HOME e valida.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := toml.NewDecoder(strings.NewReader(string(b))).DisallowUnknownFields().Decode(&c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	c.expand()
	return c, c.Validate()
}

// SetHome define Paths.Home expandindo ~ e variáveis e tornando o caminho absoluto.
func (c *Config) SetHome(h string) {
	h = expand(h)
	if abs, err := filepath.Abs(h); err == nil {
		h = abs
	}
	c.Paths.Home = h
}

func expand(s string) string {
	home, _ := os.UserHomeDir()
	s = strings.ReplaceAll(s, "$PREFIX", os.Getenv("PREFIX"))
	s = os.ExpandEnv(s)
	if s == "~" || strings.HasPrefix(s, "~/") {
		s = filepath.Join(home, strings.TrimPrefix(s, "~"))
	}
	return s
}

func (c *Config) expand() {
	c.Paths.Home = expand(c.Paths.Home)
	c.Runit.ServiceDir = expand(c.Runit.ServiceDir)
	c.Git.AuthorizedKeys = expand(c.Git.AuthorizedKeys)
}

func (c Config) Validate() error {
	var errs []error
	if c.Paths.Home == "" || !filepath.IsAbs(c.Paths.Home) {
		errs = append(errs, errors.New("paths.home deve ser um caminho absoluto"))
	}
	if c.Ports.RangeStart < 1024 || c.Ports.RangeEnd > 65535 || c.Ports.RangeStart >= c.Ports.RangeEnd {
		errs = append(errs, errors.New("ports.range_* inválido (1024 <= start < end <= 65535)"))
	}
	if c.Build.MaxConcurrent < 1 {
		errs = append(errs, errors.New("build.max_concurrent deve ser >= 1"))
	}
	if c.Deploy.KeepReleases < 1 {
		errs = append(errs, errors.New("deploy.keep_releases deve ser >= 1"))
	}
	if c.Proxy.Listen == c.Proxy.Admin || c.Proxy.Listen == c.HTTP.PanelAddr || c.Proxy.Admin == c.HTTP.PanelAddr {
		errs = append(errs, errors.New("http.panel_addr, proxy.listen e proxy.admin devem ser distintos"))
	}
	switch c.HTTP.SecureCookies {
	case "auto", "always", "never":
	default:
		errs = append(errs, errors.New("http.secure_cookies deve ser auto|always|never"))
	}
	return errors.Join(errs...)
}

// Paths derivados de $DP_HOME (TSD §6.1).
func (c Config) Dir(parts ...string) string {
	return filepath.Join(append([]string{c.Paths.Home}, parts...)...)
}
func (c Config) DBPath() string     { return c.Dir("data", "droidpaas.db") }
func (c Config) SocketPath() string { return c.Dir("run", "droidd.sock") }
func (c Config) KeyPath() string    { return c.Dir("etc", "secrets", "master.key") }
func (c Config) File() string       { return c.Dir("etc", "droidd.toml") }
