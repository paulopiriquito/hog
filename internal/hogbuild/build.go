package hogbuild

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Build composes and compiles a custom hog binary per opts. It writes the temp
// module to a scratch dir, resolves plugin modules, and runs `go build`. The
// produced binary is at opts.Output.
func Build(ctx context.Context, configPath string, opts Options) error {
	if opts.HogSource == "" {
		return fmt.Errorf("hogbuild: HogSource (the hog module source) is required")
	}
	if opts.GoBin == "" {
		opts.GoBin = "go"
	}
	// Reject control chars in go.mod-spliced values (prevent go.mod line injection).
	if hasControl(opts.HogSource) {
		return fmt.Errorf("hogbuild: invalid HogSource (control character)")
	}
	for _, rp := range opts.Replaces {
		if hasControl(rp.Path) || hasControl(rp.With) {
			return fmt.Errorf("hogbuild: invalid --replace value (control character)")
		}
	}

	plugins, err := ReadManifest(configPath)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(opts.Output)
	if err != nil {
		return err
	}
	hogSrc, err := filepath.Abs(opts.HogSource)
	if err != nil {
		return err
	}
	opts.HogSource = hogSrc

	opts.Replaces, err = absReplaces(opts.Replaces)
	if err != nil {
		return err
	}
	for _, rp := range opts.Replaces {
		if rp.Path == hogModule {
			return fmt.Errorf("hogbuild: cannot --replace the hog module (%s); use --hog-source", hogModule)
		}
	}

	dir, err := os.MkdirTemp("", "hogbuild-*")
	if err != nil {
		return err
	}
	if opts.Keep {
		fmt.Fprintf(os.Stderr, "hogbuild: temp build dir %s (kept)\n", dir)
	} else {
		defer os.RemoveAll(dir)
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(RenderMain(plugins)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(RenderGoMod(opts)), 0o644); err != nil {
		return err
	}

	env := append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, opts.GoBin, args...)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("hogbuild: go %v: %w", args, err)
		}
		return nil
	}

	for _, p := range plugins {
		if moduleReplaced(p.ImportPath, opts.Replaces) {
			continue
		}
		ver := p.Version
		if ver == "" {
			ver = "latest"
		}
		if err := run("get", "--", p.ImportPath+"@"+ver); err != nil {
			return err
		}
	}
	if err := run("mod", "tidy"); err != nil {
		return err
	}
	args := []string{"build", "-o", out}
	if opts.Tags != "" {
		args = append(args, "-tags", opts.Tags)
	}
	args = append(args, ".")
	return run(args...)
}

func hasControl(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\r' || (r < ' ' && r != '\t') {
			return true
		}
	}
	return false
}

// absReplaces returns a copy of replaces with each local target (With) made
// absolute against the current working directory. Go resolves a relative
// `replace` directory against the generated go.mod's own location (a temp dir),
// not the invocation dir, so a relative --replace would otherwise never resolve.
func absReplaces(replaces []Replace) ([]Replace, error) {
	out := make([]Replace, len(replaces))
	copy(out, replaces)
	for i := range out {
		abs, err := filepath.Abs(out[i].With)
		if err != nil {
			return nil, err
		}
		out[i].With = abs
	}
	return out, nil
}

// moduleReplaced reports whether importPath is at or under a --replaced module path.
func moduleReplaced(importPath string, replaces []Replace) bool {
	for _, rp := range replaces {
		if importPath == rp.Path || strings.HasPrefix(importPath, rp.Path+"/") {
			return true
		}
	}
	return false
}
