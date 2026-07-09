package configschema

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// repoRoot returns the module root, two directories above this file
// (internal/configschema/schema_test.go -> internal -> repo root).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// compileSchema compiles the embedded JSON Schema as Draft 2020-12.
func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(JSON()))
	if err != nil {
		t.Fatalf("unmarshal schema json: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem://hog.schema.json", doc); err != nil {
		t.Fatalf("add schema resource: %v", err)
	}
	sch, err := c.Compile("mem://hog.schema.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return sch
}

func TestSchemaCompiles(t *testing.T) {
	sch := compileSchema(t)
	if sch == nil {
		t.Fatal("compiled schema is nil")
	}
}

// splitDocs splits a multi-document YAML stream on lines that are exactly
// "---", mirroring the `---` document separators used throughout HOG's
// example/fixture configs.
func splitDocs(data []byte) [][]byte {
	lines := strings.Split(string(data), "\n")
	var docs [][]byte
	var cur []string
	flush := func() {
		docs = append(docs, []byte(strings.Join(cur, "\n")))
		cur = nil
	}
	for _, ln := range lines {
		if strings.TrimRight(ln, "\r") == "---" {
			flush()
			continue
		}
		cur = append(cur, ln)
	}
	flush()
	return docs
}

// toJSONCompatible round-trips v through encoding/json so map/slice/scalar
// types match exactly what a JSON Schema validator expects (plain
// map[string]any, []any, float64, string, bool, nil) rather than any
// YAML-decoder-specific representation.
func toJSONCompatible(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal to json: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal from json: %v", err)
	}
	return out
}

func TestExampleConfigsValidate(t *testing.T) {
	sch := compileSchema(t)
	root := repoRoot(t)

	paths := []string{
		filepath.Join(root, "build", "static", "gateway.yaml"),
		filepath.Join(root, "website", "docs", "examples", "full-config.yaml"),
		filepath.Join(root, "tests", "e2e", "config", "gateway.yaml"),
		filepath.Join(root, "tests", "e2e", "config-static", "gateway.yaml"),
	}

	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			for i, raw := range splitDocs(data) {
				if strings.TrimSpace(string(raw)) == "" {
					continue
				}
				var doc any
				if err := yaml.Unmarshal(raw, &doc); err != nil {
					t.Fatalf("%s: doc %d: yaml unmarshal: %v", path, i, err)
				}
				if doc == nil {
					continue
				}
				m, ok := doc.(map[string]any)
				if !ok {
					t.Fatalf("%s: doc %d: not a mapping (%T)", path, i, doc)
				}
				kind, _ := m["kind"].(string)
				if kind == "" {
					continue // blank/empty document
				}
				jsonDoc := toJSONCompatible(t, doc)
				if err := sch.Validate(jsonDoc); err != nil {
					t.Errorf("%s: doc %d (kind=%s, name=%v): schema validation failed: %v", path, i, kind, m["metadata"], err)
				}
			}
		})
	}
}

func TestBadConfigRejected(t *testing.T) {
	sch := compileSchema(t)

	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "invalid access.auth enum value",
			yaml: `
apiVersion: hog.dev/v1
kind: Route
metadata:
  name: bad-auth
spec:
  match: /bad-auth
  handler:
    type: static
    dir: /srv/web
  access:
    auth: maybe
`,
		},
		{
			name: "typo'd access key rejected by additionalProperties:false",
			yaml: `
apiVersion: hog.dev/v1
kind: Route
metadata:
  name: bad-typo
spec:
  match: /bad-typo
  handler:
    type: static
    dir: /srv/web
  access:
    auth: required
    authorise:
      - staff
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc any
			if err := yaml.Unmarshal([]byte(tt.yaml), &doc); err != nil {
				t.Fatalf("yaml unmarshal: %v", err)
			}
			jsonDoc := toJSONCompatible(t, doc)
			if err := sch.Validate(jsonDoc); err == nil {
				t.Fatalf("expected schema validation to fail, but it passed")
			}
		})
	}
}

func TestServedCopyInSync(t *testing.T) {
	root := repoRoot(t)
	internalCopy, err := os.ReadFile(filepath.Join(root, "internal", "configschema", "hog.schema.json"))
	if err != nil {
		t.Fatalf("read internal copy: %v", err)
	}
	websiteCopy, err := os.ReadFile(filepath.Join(root, "website", "docs", "hog.schema.json"))
	if err != nil {
		t.Fatalf("read website copy: %v", err)
	}
	if !bytes.Equal(internalCopy, websiteCopy) {
		t.Fatal("internal/configschema/hog.schema.json and website/docs/hog.schema.json have diverged; keep them byte-identical")
	}
}
