package forward

import "strings"

// Resolve walks a dotted path through nested maps and returns the leaf value.
// Returns (nil, false) if the path is empty, any segment is missing, or the
// path traverses through a non-map value.
func Resolve(userinfo map[string]any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	var current any = userinfo
	for _, segment := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, exists := m[segment]
		if !exists {
			return nil, false
		}
		current = next
	}
	return current, true
}
