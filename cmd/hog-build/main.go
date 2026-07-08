// Command hog-build composes a custom hog binary from a Gateway.plugins manifest.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/paulopiriquito/hog/internal/hogbuild"
)

type replaceFlag []hogbuild.Replace

func (r *replaceFlag) String() string { return "" }
func (r *replaceFlag) Set(v string) error {
	i := strings.Index(v, "=")
	if i < 0 {
		return fmt.Errorf("--replace must be <importpath>=<localdir>, got %q", v)
	}
	*r = append(*r, hogbuild.Replace{Path: v[:i], With: v[i+1:]})
	return nil
}

func main() {
	cfg := flag.String("config", "", "path to the gateway config file or directory (the plugin manifest)")
	out := flag.String("o", "hog", "output binary path")
	hogSrc := flag.String("hog-source", os.Getenv("HOG_SOURCE"), "path to the hog module source (pinned via replace); default $HOG_SOURCE")
	tags := flag.String("tags", "", "go build tags")
	goBin := flag.String("go", "go", "path to the go toolchain")
	keep := flag.Bool("keep", false, "keep the temp build dir for debugging")
	var replaces replaceFlag
	flag.Var(&replaces, "replace", "override a module with a local dir: <importpath>=<localdir> (repeatable)")
	flag.Parse()

	if *cfg == "" {
		fmt.Fprintln(os.Stderr, "hog-build: --config is required")
		os.Exit(2)
	}
	if *hogSrc == "" {
		fmt.Fprintln(os.Stderr, "hog-build: --hog-source (or $HOG_SOURCE) is required")
		os.Exit(2)
	}
	err := hogbuild.Build(context.Background(), *cfg, hogbuild.Options{
		Output: *out, HogSource: *hogSrc, Replaces: replaces, Tags: *tags, GoBin: *goBin, Keep: *keep,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "hog-build: wrote %s\n", *out)
}
