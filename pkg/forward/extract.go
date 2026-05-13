package forward

import (
	"fmt"
	"strconv"
	"strings"
)

// Diagnostic describes why a configured header was not emitted.
type Diagnostic struct {
	Header  string
	Claim   string
	Reason  string   // "missing_claim", "wrong_type", "no_matches"
	Samples []string // up to 3 raw values (truncated to 80 chars) when reason is "no_matches"
}

// Result is the output of Apply.
//
// Headers is keyed by HTTP header name and contains every configured entry
// that produced a value; this is what backends receive.
//
// Mapped is keyed by Header.As and only contains entries whose configuration
// set As to a non-empty string. Operators use As to opt entries into the
// SPA-visible projection, keeping the JSON shape free of HTTP-header naming
// conventions (e.g. the X- prefix) and dropping redundant identity-passthrough
// entries that the SPA can already read from the raw IdP claims.
type Result struct {
	Headers     map[string]string // HTTP-header name → comma-joined wire value
	Mapped      map[string]any    // As → scalar string OR []string (mirrors source claim shape)
	Diagnostics []Diagnostic
}

// Apply resolves each configured header against the userinfo map, applies
// mapping rules if present, and returns the resulting wire values, the
// SPA-facing mapped object, and per-header diagnostics for any skipped entries.
func Apply(userinfo map[string]any, cfg Config) Result {
	res := Result{
		Headers: map[string]string{},
		Mapped:  map[string]any{},
	}
	for _, h := range cfg.Headers {
		raw, ok := Resolve(userinfo, h.Claim)
		if !ok {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Header: h.Name, Claim: h.Claim, Reason: "missing_claim",
			})
			continue
		}

		switch v := raw.(type) {
		case string:
			applyScalar(&res, h, v)
		case float64:
			applyScalar(&res, h, formatFloat(v))
		case bool, int, int64:
			applyScalar(&res, h, fmt.Sprintf("%v", v))
		case []any:
			values := make([]string, 0, len(v))
			for _, item := range v {
				if s := stringifyArrayItem(item); s != "" {
					values = append(values, s)
				}
			}
			applyArray(&res, h, values)
		default:
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Header: h.Name, Claim: h.Claim, Reason: "wrong_type",
			})
		}
	}
	return res
}

func applyScalar(res *Result, h Header, value string) {
	if len(h.Mapping) == 0 {
		res.Headers[h.Name] = value
		if h.As != "" {
			res.Mapped[h.As] = value
		}
		return
	}
	for _, r := range h.Mapping {
		if strings.Contains(value, r.From) {
			res.Headers[h.Name] = r.To
			if h.As != "" {
				res.Mapped[h.As] = r.To
			}
			return
		}
	}
	res.Diagnostics = append(res.Diagnostics, Diagnostic{
		Header: h.Name, Claim: h.Claim, Reason: "no_matches",
		Samples: []string{truncate(value, 80)},
	})
}

func applyArray(res *Result, h Header, values []string) {
	if len(h.Mapping) == 0 {
		res.Headers[h.Name] = strings.Join(values, ",")
		if h.As != "" {
			res.Mapped[h.As] = values
		}
		return
	}

	var matched []string
	seen := map[string]bool{}
	var samples []string
	for _, v := range values {
		var hit bool
		for _, r := range h.Mapping {
			if strings.Contains(v, r.From) {
				if !seen[r.To] {
					matched = append(matched, r.To)
					seen[r.To] = true
				}
				hit = true
				break
			}
		}
		if !hit && len(samples) < 3 {
			samples = append(samples, truncate(v, 80))
		}
	}

	if len(matched) == 0 {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Header: h.Name, Claim: h.Claim, Reason: "no_matches",
			Samples: samples,
		})
		return
	}
	res.Headers[h.Name] = strings.Join(matched, ",")
	if h.As != "" {
		res.Mapped[h.As] = matched
	}
}

func stringifyArrayItem(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return formatFloat(s)
	case int, int64, bool:
		return fmt.Sprintf("%v", s)
	default:
		return ""
	}
}

// formatFloat returns a stringified float. Integer-valued floats are formatted
// without a decimal point ("14947156" rather than the default "%v" output of
// "1.4947156e+07"). NaN and Inf cannot satisfy f == float64(int64(f)) so they
// fall through to FormatFloat, which produces "NaN", "+Inf", or "-Inf".
func formatFloat(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
