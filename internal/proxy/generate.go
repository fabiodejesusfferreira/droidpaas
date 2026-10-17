// Package proxy gera e aplica a configuração completa do Caddy (TSD §8.5, ADR-05).
package proxy

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

//go:embed pages/*.html
var pagesFS embed.FS

// Pages são os corpos HTML das respostas 404 e 503 do DroidPaaS.
type Pages struct{ NotFound, Unavailable string }

// LoadPages usa as páginas embutidas, a menos que existam <dir>/404.html e <dir>/503.html.
func LoadPages(dir string) Pages {
	read := func(name string) string {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil && dir != "" {
			return string(b)
		}
		b, _ := pagesFS.ReadFile("pages/" + name)
		return string(b)
	}
	return Pages{NotFound: read("404.html"), Unavailable: read("503.html")}
}

type Kind string

const (
	Process Kind = "process" // reverse_proxy para 127.0.0.1:<port>
	Static  Kind = "static"  // file_server em Root
	Stopped Kind = "stopped" // 503
)

type AppRoute struct {
	Name  string
	Hosts []string
	Kind  Kind
	Port  int
	Root  string
}

type Input struct {
	Listen, Admin string
	Apps          []AppRoute
	PanelHostname string // opcional: roteia o painel por hostname
	PanelAddr     string
	Pages         Pages
}

// ---- tipos do JSON do Caddy (a ordem dos campos fixa a saída, para os golden files) ----

type cfg struct {
	Admin struct {
		Listen string `json:"listen"`
	} `json:"admin"`
	Apps struct {
		HTTP struct {
			Servers map[string]server `json:"servers"`
		} `json:"http"`
	} `json:"apps"`
}

type server struct {
	Listen         []string `json:"listen"`
	AutomaticHTTPS struct {
		Disable bool `json:"disable"`
	} `json:"automatic_https"`
	Routes []route `json:"routes"`
	// o cloudflared fala com o Caddy por 127.0.0.1: só assim o X-Forwarded-Proto: https do túnel é preservado
	TrustedProxies trusted `json:"trusted_proxies"`
}

type trusted struct {
	Source string   `json:"source"`
	Ranges []string `json:"ranges"`
}

type route struct {
	Match    []match   `json:"match,omitempty"`
	Handle   []handler `json:"handle"`
	Terminal bool      `json:"terminal,omitempty"`
}

type match struct {
	Host []string `json:"host"`
}

type handler struct {
	Handler    string              `json:"handler"`
	Upstreams  []upstream          `json:"upstreams,omitempty"`
	Root       string              `json:"root,omitempty"`
	StatusCode string              `json:"status_code,omitempty"`
	Body       string              `json:"body,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
}

type upstream struct {
	Dial string `json:"dial"`
}

var htmlHeader = map[string][]string{"Content-Type": {"text/html; charset=utf-8"}}

func staticPage(code, body string) handler {
	return handler{Handler: "static_response", StatusCode: code, Body: body, Headers: htmlHeader}
}

// Generate monta a config completa: painel (opcional) → apps por nome → fallback 404.
func Generate(in Input) ([]byte, error) {
	var c cfg
	c.Admin.Listen = in.Admin
	var s server
	s.Listen = []string{in.Listen}
	s.AutomaticHTTPS.Disable = true // HTTPS fica com o túnel (ADR-06)
	s.TrustedProxies = trusted{Source: "static", Ranges: []string{"127.0.0.1/32", "::1/128"}}

	if in.PanelHostname != "" {
		s.Routes = append(s.Routes, route{
			Match:    []match{{Host: []string{in.PanelHostname}}},
			Handle:   []handler{{Handler: "reverse_proxy", Upstreams: []upstream{{Dial: in.PanelAddr}}}},
			Terminal: true,
		})
	}
	apps := append([]AppRoute(nil), in.Apps...)
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	for _, a := range apps {
		if len(a.Hosts) == 0 {
			continue
		}
		hosts := append([]string(nil), a.Hosts...)
		sort.Strings(hosts)
		r := route{Match: []match{{Host: hosts}}, Terminal: true}
		switch a.Kind {
		case Process:
			r.Handle = []handler{{Handler: "reverse_proxy", Upstreams: []upstream{{Dial: "127.0.0.1:" + itoa(a.Port)}}}}
		case Static:
			r.Handle = []handler{{Handler: "file_server", Root: a.Root}}
		default:
			r.Handle = []handler{staticPage("503", in.Pages.Unavailable)}
		}
		s.Routes = append(s.Routes, r)
	}
	s.Routes = append(s.Routes, route{Handle: []handler{staticPage("404", in.Pages.NotFound)}})

	c.Apps.HTTP.Servers = map[string]server{"dp": s}
	return json.MarshalIndent(c, "", "  ")
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
