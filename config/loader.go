package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Load reads config from a single file or a directory of *.yaml / *.yml files.
// Files in a directory are read in lexical filename order (this defines the
// document order used for plugin ordering). All values are ${ENV}-expanded
// using the process environment before decoding.
func Load(path string) ([]Resource, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("config path: %w", err)
	}
	var files []string
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if ext := strings.ToLower(filepath.Ext(e.Name())); ext == ".yaml" || ext == ".yml" {
				files = append(files, filepath.Join(path, e.Name()))
			}
		}
		sort.Strings(files)
	} else {
		files = []string{path}
	}

	var out []Resource
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		expanded, err := ExpandEnv(string(raw), os.LookupEnv)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		rs, err := DecodeAll([]byte(expanded))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out = append(out, rs...)
	}
	return out, nil
}
