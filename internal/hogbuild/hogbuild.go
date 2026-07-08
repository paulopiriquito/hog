// Package hogbuild composes a custom hog binary from a Gateway.plugins manifest:
// it renders a temp main module that blank-imports the plugin packages (so their
// init() self-registers) and drives the go toolchain to build it. Build-time only;
// not imported by the runtime.
package hogbuild

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/gateway"
)

const hogModule = "github.com/paulopiriquito/hog"

// Plugin is one manifest entry: a package import path + optional module version.
type Plugin struct {
	ImportPath string
	Version    string // "" ⇒ latest at build time
}

// Replace is a --replace directive (module path ⇒ local dir), for local dev.
type Replace struct {
	Path string
	With string
}

// Options controls a build.
type Options struct {
	Output    string    // -o binary path
	HogSource string    // local hog module source, pinned via replace (required)
	Replaces  []Replace // --replace directives
	Tags      string    // build tags
	GoBin     string    // path to `go` (default: "go")
	Keep      bool      // keep the temp build dir on exit (debug)
}

// ReadManifest loads the config, finds the single Gateway resource, and parses
// its spec.plugins into Plugins (fail-fast on a bad entry or a missing/dup Gateway).
func ReadManifest(configPath string) ([]Plugin, error) {
	resources, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	var settings gateway.Settings
	seen := false
	for _, r := range resources {
		if r.Kind != config.KindGateway {
			continue
		}
		if seen {
			return nil, fmt.Errorf("hogbuild: config has more than one Gateway resource")
		}
		if settings, err = gateway.FromResource(r); err != nil {
			return nil, err
		}
		seen = true
	}
	if !seen {
		return nil, fmt.Errorf("hogbuild: config has no Gateway resource (the plugin manifest)")
	}
	plugins := make([]Plugin, 0, len(settings.Plugins))
	for _, entry := range settings.Plugins {
		p, err := parsePlugin(entry)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, p)
	}
	return plugins, nil
}

// parsePlugin splits "<import-path>[@version]" and validates the import path.
func parsePlugin(entry string) (Plugin, error) {
	entry = strings.TrimSpace(entry)
	path, ver := entry, ""
	if i := strings.LastIndex(entry, "@"); i >= 0 {
		path, ver = entry[:i], entry[i+1:]
	}
	if !validImportPath(path) {
		return Plugin{}, fmt.Errorf("hogbuild: invalid plugin import path %q", entry)
	}
	return Plugin{ImportPath: path, Version: ver}, nil
}

// validImportPath is a conservative check that the path is safe to splice into
// generated Go source and go.mod (the go toolchain does the authoritative check).
// Rejects empty, whitespace/control/quote/backtick chars, and any "."/".." segment,
// and requires a domain-like first segment.
func validImportPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "-") {
		return false
	}
	for _, r := range p {
		if r <= ' ' || r == '"' || r == '`' || r == '\'' || r == '\\' || r == '@' {
			return false
		}
	}
	if !strings.Contains(strings.SplitN(p, "/", 2)[0], ".") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return false
		}
	}
	return true
}

var mainTmpl = template.Must(template.New("main").Parse(`package main

import (
	"{{.Hog}}"
{{- range .Plugins}}
	_ "{{.ImportPath}}"
{{- end}}
)

func main() { hog.Main() }
`))

// RenderMain renders the composed main.go (blank-importing each plugin).
func RenderMain(plugins []Plugin) string {
	var b strings.Builder
	// Execute never errors: the template is fixed and strings.Builder writes never fail.
	_ = mainTmpl.Execute(&b, struct {
		Hog     string
		Plugins []Plugin
	}{hogModule, plugins})
	return b.String()
}

// RenderGoMod renders the temp module's go.mod: pins hog via replace-to-source
// and adds any --replace directives. Plugin requires are added by `go get`/`go mod
// tidy` at build time (Task 2), so they are not written here.
func RenderGoMod(opts Options) string {
	var b strings.Builder
	b.WriteString("module hogbin\n\ngo 1.26\n\n")
	fmt.Fprintf(&b, "replace %s => %s\n", hogModule, modTarget(opts.HogSource))
	for _, rp := range opts.Replaces {
		fmt.Fprintf(&b, "replace %s => %s\n", rp.Path, modTarget(rp.With))
	}
	return b.String()
}

// modTarget quotes a replace target only when it needs it (contains a space,
// tab, or quote), so clean paths stay unquoted.
func modTarget(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return strconv.Quote(s)
	}
	return s
}
