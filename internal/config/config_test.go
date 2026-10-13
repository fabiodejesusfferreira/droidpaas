package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	c := Default()
	c.SetHome(t.TempDir())
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Proxy.Listen != "127.0.0.1:18080" || c.Proxy.Admin != "127.0.0.1:12019" {
		t.Fatalf("portas do proxy: %s %s", c.Proxy.Listen, c.Proxy.Admin)
	}
}

func TestLoadOverridesAndExpands(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "droidd.toml")
	os.WriteFile(p, []byte("[paths]\nhome = \""+dir+"\"\n[build]\ntimeout = \"2m\"\n[http]\npanel_addr = \"127.0.0.1:9000\"\n"), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Build.Timeout.D() != 2*time.Minute || c.HTTP.PanelAddr != "127.0.0.1:9000" {
		t.Fatalf("override não aplicado: %+v", c.Build)
	}
	if c.Deploy.KeepReleases != 5 { // default preservado
		t.Fatal("default perdido")
	}
	if got := c.DBPath(); got != filepath.Join(dir, "data", "droidpaas.db") {
		t.Fatal(got)
	}
}

func TestLoadRejectsUnknownAndInvalid(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"campo desconhecido": "[paths]\nhome = \"" + dir + "\"\nfoo = 1\n",
		"portas iguais":      "[paths]\nhome = \"" + dir + "\"\n[proxy]\nlisten = \"127.0.0.1:8090\"\n",
		"cookies":            "[paths]\nhome = \"" + dir + "\"\n[http]\nsecure_cookies = \"talvez\"\n",
		"home relativo":      "[paths]\nhome = \"relativo\"\n",
	} {
		p := filepath.Join(dir, "x.toml")
		os.WriteFile(p, []byte(body), 0o600)
		if _, err := Load(p); err == nil {
			t.Errorf("%s: esperava erro", name)
		}
	}
}

func TestExpandTildeAndPrefix(t *testing.T) {
	t.Setenv("PREFIX", "/p")
	home, _ := os.UserHomeDir()
	if got := expand("~/x"); got != filepath.Join(home, "x") {
		t.Fatal(got)
	}
	if got := expand("$PREFIX/var/service"); got != "/p/var/service" {
		t.Fatal(got)
	}
	if strings.Contains(expand("$HOME"), "$") {
		t.Fatal("HOME não expandido")
	}
}

func TestExpandMethodResolvesPrefix(t *testing.T) {
	t.Setenv("PREFIX", "/p")
	c := Default()
	c.SetHome(t.TempDir())
	c.Expand()
	if c.Runit.ServiceDir != "/p/var/service" {
		t.Fatalf("service_dir = %q (o init precisa do caminho expandido para achar o SVDIR)", c.Runit.ServiceDir)
	}
	if strings.Contains(c.Git.AuthorizedKeys, "~") {
		t.Fatalf("authorized_keys = %q", c.Git.AuthorizedKeys)
	}
}
