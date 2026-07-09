// Package configschema embeds the JSON Schema for HOG configuration YAML.
package configschema

import _ "embed"

//go:embed hog.schema.json
var jsonSchema []byte

// JSON returns the HOG configuration JSON Schema (Draft 2020-12).
func JSON() []byte { return jsonSchema }
