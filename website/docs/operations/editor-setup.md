# Editor setup & schema validation

HOG publishes a [JSON Schema](https://json-schema.org/) for its config YAML
at `https://paulopiriquito.github.io/hog/hog.schema.json`, and bakes the
exact same schema into the `hog` binary — run `hog schema` to print it
offline, no network access required. Point your editor or your CI pipeline
at either copy to get autocomplete, hover documentation, and red-squiggle
validation on `Gateway`, `Route`, `Policy`, and every other resource `kind`
documented in the [configuration reference](configuration.md).

The schema is deliberately **stricter than the HOG runtime**: it flags
unknown or typo'd fields (e.g. `hander:` instead of `handler:`,
`csrf.trustedOrgins:`) that the runtime's decoder silently ignores. Treat a
schema error as "this field does nothing," even if HOG itself would start up
fine.

A HOG config file (typically `gateway.yaml`) is **multi-document** —
`---`-separated YAML documents, one resource per document, as described in
[Loading model](configuration.md#loading-model). The schema describes a
single resource document, not the whole file; editors and validators that
understand multi-document YAML (the VS Code extension below, `check-jsonschema`)
apply it once per document automatically.

## VS Code

Install the [`redhat.vscode-yaml`](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)
extension. Then point it at the schema one of two ways:

**1. An inline directive at the top of the config file:**

```yaml
# yaml-language-server: $schema=https://paulopiriquito.github.io/hog/hog.schema.json
apiVersion: hog.dev/v1
kind: Gateway
metadata:
  name: hog
spec:
  listen: ":8080"
```

**2. A workspace `settings.json` mapping**, which applies to every matching
file without needing a directive in each one:

```json
{
  "yaml.schemas": {
    "https://paulopiriquito.github.io/hog/hog.schema.json": ["**/gateway.yaml", "**/hog/**/*.yaml"]
  }
}
```

Either way you get field completion, hover documentation pulled from the
schema's `description`s, and a red squiggle under unknown keys or values of
the wrong type as you type.

## GoLand / IntelliJ IDEA

**Settings → Languages & Frameworks → Schemas and DTDs → JSON Schema
Mappings**, then **+** to add a mapping:

- **Name**: `HOG`
- **Schema file or URL**: `https://paulopiriquito.github.io/hog/hog.schema.json`
- **Schema version**: `JSON Schema version 2020-12`
- Under **Mappings**, add a file path pattern — `gateway.yaml`, or a whole
  directory if you keep config split across multiple files.

The `# yaml-language-server: $schema=...` inline directive from the VS Code
section above also works in JetBrains IDEs, if you'd rather scope the schema
per-file than through a workspace-wide mapping.

## CI linting

Validate config files in a pipeline with
[`check-jsonschema`](https://check-jsonschema.readthedocs.io/), which
understands multi-document YAML natively:

```bash
pipx run check-jsonschema --schemafile https://paulopiriquito.github.io/hog/hog.schema.json path/to/gateway.yaml
```

A minimal GitHub Actions step doing the same:

```yaml
- name: Validate HOG config against the schema
  run: pipx run check-jsonschema --schemafile https://paulopiriquito.github.io/hog/hog.schema.json path/to/gateway.yaml
```

For hermetic CI — no dependency on GitHub Pages being reachable, and no
drift between the schema you validate against and the `hog` binary you
deploy — vendor the schema from the binary itself and point
`--schemafile` at the local copy:

```bash
hog schema > hog.schema.json
pipx run check-jsonschema --schemafile hog.schema.json path/to/gateway.yaml
```

## Offline

`hog schema` prints the exact schema baked into your HOG binary — the same
document published at the GitHub Pages URL above, with no network access
required. Use it to vendor the schema for hermetic CI (see above), diff it
across HOG versions, or feed it to any other schema-aware tool.
