package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
	"github.com/paulopiriquito/hog/telemetry"
)

// maxBackendBytes caps a single backend response read (memory bound).
const maxBackendBytes = 10 << 20 // 10 MiB

// apiConfig is the decoded handler config for `type: api`.
type apiConfig struct {
	Timeout  string          `yaml:"timeout"`
	Backends []backendConfig `yaml:"backends"`
}

type backendConfig struct {
	Group              string `yaml:"group"`
	Upstream           string `yaml:"upstream"`
	Path               string `yaml:"path"`
	Method             string `yaml:"method"`
	Required           *bool  `yaml:"required"`
	ForwardQuery       bool   `yaml:"forwardQuery"`
	ForwardAccessToken bool   `yaml:"forwardAccessToken"`
}

// backend is a validated aggregation target.
type backend struct {
	group        string
	base         *url.URL
	path         string
	method       string
	required     bool
	forwardQuery bool
	opts         forwardOptions
}

// registerAPI registers the built-in `api` (aggregation) terminal handler.
func registerAPI(reg *registry.Registry) {
	reg.Register(config.KindTerminalHandler, "api", func(name string, cfg registry.RawConfig) (any, error) {
		var ac apiConfig
		if err := cfg.Decode(&ac); err != nil {
			return nil, fmt.Errorf("api %q: %w", name, err)
		}
		if len(ac.Backends) == 0 {
			return nil, fmt.Errorf("api %q: at least one backend is required", name)
		}
		var timeout time.Duration
		if ac.Timeout != "" {
			d, err := time.ParseDuration(ac.Timeout)
			if err != nil {
				return nil, fmt.Errorf("api %q: timeout: %w", name, err)
			}
			timeout = d
		}
		seen := map[string]bool{}
		backends := make([]backend, 0, len(ac.Backends))
		for i, b := range ac.Backends {
			if b.Group == "" {
				return nil, fmt.Errorf("api %q: backend[%d]: group is required", name, i)
			}
			if seen[b.Group] {
				return nil, fmt.Errorf("api %q: duplicate group %q", name, b.Group)
			}
			seen[b.Group] = true
			if b.Path == "" {
				return nil, fmt.Errorf("api %q: backend %q: path is required", name, b.Group)
			}
			base, err := url.Parse(b.Upstream)
			if err != nil || base.Scheme == "" || base.Host == "" {
				return nil, fmt.Errorf("api %q: backend %q: invalid upstream %q", name, b.Group, b.Upstream)
			}
			method := b.Method
			if method == "" {
				method = http.MethodGet
			}
			if _, err := http.NewRequest(method, base.String(), nil); err != nil {
				return nil, fmt.Errorf("api %q: backend %q: invalid method %q", name, b.Group, method)
			}
			required := true
			if b.Required != nil {
				required = *b.Required
			}
			backends = append(backends, backend{
				group: b.Group, base: base, path: b.Path, method: method,
				required: required, forwardQuery: b.ForwardQuery,
				opts: forwardOptions{forwardAccessToken: b.ForwardAccessToken},
			})
		}
		return &apiHandler{backends: backends, timeout: timeout}, nil
	})
}

type apiHandler struct {
	backends []backend
	timeout  time.Duration
}

type backendResult struct {
	body    json.RawMessage
	ok      bool
	timeout bool
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.timeout)
		defer cancel()
	}
	results := make([]backendResult, len(h.backends))
	var wg sync.WaitGroup
	for i := range h.backends {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = h.call(ctx, r, h.backends[i])
		}(i)
	}
	wg.Wait()

	merged := make(map[string]json.RawMessage, len(h.backends))
	var omitted []string
	for i, b := range h.backends {
		res := results[i]
		if res.ok {
			merged[b.group] = res.body
			continue
		}
		if b.required {
			code := http.StatusBadGateway
			if res.timeout {
				code = http.StatusGatewayTimeout
			}
			http.Error(w, "upstream error", code)
			return
		}
		omitted = append(omitted, b.group)
	}
	if len(omitted) > 0 {
		w.Header().Set("X-Hog-Partial", strings.Join(omitted, ","))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(merged)
}

// call performs one backend request and classifies the result. A 2xx response
// that is not valid JSON — including an empty body — is treated as a backend
// failure; a required backend must return a JSON document such as null/{}/[].
func (h *apiHandler) call(ctx context.Context, in *http.Request, b backend) backendResult {
	u := *b.base
	sub, ok := substituteParams(b.path, in)
	if !ok {
		// Hostile/path-traversing {param} value — fail this backend closed.
		return backendResult{}
	}
	u.Path = path.Join(b.base.Path, sub)
	if b.forwardQuery {
		u.RawQuery = in.URL.RawQuery
	}
	out, err := http.NewRequestWithContext(ctx, b.method, u.String(), nil)
	if err != nil {
		return backendResult{}
	}
	out = out.WithContext(telemetry.WithBackend(out.Context(), b.group))
	prepareBackendRequest(out, in, b.opts)
	resp, err := backendRoundTripper.RoundTrip(out)
	if err != nil {
		return backendResult{timeout: errors.Is(err, context.DeadlineExceeded)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return backendResult{}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBackendBytes))
	if err != nil || !json.Valid(body) {
		return backendResult{}
	}
	return backendResult{body: json.RawMessage(body), ok: true}
}

// substituteParams replaces {name} tokens in p with r.PathValue(name). It
// returns ok=false if any substituted value is unsafe or empty — an empty value
// means the {param} name was not matched by the router (unknown param), and an
// unsafe value contains a separator or is a "."/".." traversal token. In both
// cases the request is rejected fail-closed rather than reaching an
// operator-unintended backend path.
func substituteParams(p string, r *http.Request) (string, bool) {
	if !strings.Contains(p, "{") {
		return p, true
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(p, '{')
		if i < 0 {
			b.WriteString(p)
			break
		}
		j := strings.IndexByte(p[i:], '}')
		if j < 0 {
			b.WriteString(p)
			break
		}
		b.WriteString(p[:i])
		v := r.PathValue(p[i+1 : i+j])
		if !safePathValue(v) {
			return "", false
		}
		b.WriteString(v)
		p = p[i+j+1:]
	}
	return b.String(), true
}

// safePathValue reports whether v is safe to splice into a single path segment.
// A substituted value must be a non-empty segment that introduces no separator
// or path traversal; an empty value (an unknown/unmatched {param}) fails closed
// so the request never reaches an operator-unintended backend path.
func safePathValue(v string) bool {
	if v == "" {
		return false
	}
	if strings.ContainsAny(v, "/\\") {
		return false
	}
	return v != "." && v != ".."
}
