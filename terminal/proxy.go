package terminal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
)

// proxyConfig is the decoded handler config for `type: reverse-proxy`.
type proxyConfig struct {
	Upstream           string `yaml:"upstream"`
	StripPrefix        string `yaml:"stripPrefix"`
	PreserveHost       bool   `yaml:"preserveHost"`
	ForwardAccessToken bool   `yaml:"forwardAccessToken"`
	ForwardCookies     bool   `yaml:"forwardCookies"`
	Timeout            string `yaml:"timeout"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

// registerProxy registers the built-in `reverse-proxy` terminal handler.
func registerProxy(reg *registry.Registry) {
	reg.Register(config.KindTerminalHandler, "reverse-proxy", func(name string, cfg registry.RawConfig) (any, error) {
		var pc proxyConfig
		if err := cfg.Decode(&pc); err != nil {
			return nil, fmt.Errorf("reverse-proxy %q: %w", name, err)
		}
		if pc.Upstream == "" {
			return nil, fmt.Errorf("reverse-proxy %q: upstream is required", name)
		}
		target, err := url.Parse(pc.Upstream)
		if err != nil || target.Scheme == "" || target.Host == "" {
			return nil, fmt.Errorf("reverse-proxy %q: invalid upstream %q", name, pc.Upstream)
		}
		var timeout time.Duration
		if pc.Timeout != "" {
			if timeout, err = time.ParseDuration(pc.Timeout); err != nil {
				return nil, fmt.Errorf("reverse-proxy %q: timeout: %w", name, err)
			}
		}
		var transport http.RoundTripper = sharedTransport
		if pc.InsecureSkipVerify {
			transport = insecureTransport()
		}
		opts := forwardOptions{forwardAccessToken: pc.ForwardAccessToken, forwardCookies: pc.ForwardCookies}
		rp := &httputil.ReverseProxy{
			Transport:     transport,
			FlushInterval: -1, // immediate flush (SSE/streaming); websockets ride on ReverseProxy upgrade handling
			Rewrite: func(pr *httputil.ProxyRequest) {
				if pc.StripPrefix != "" {
					pr.Out.URL.Path = strings.TrimPrefix(pr.Out.URL.Path, pc.StripPrefix)
					pr.Out.URL.RawPath = ""
				}
				pr.SetURL(target)
				if pc.PreserveHost {
					pr.Out.Host = pr.In.Host
				} else {
					pr.Out.Host = target.Host
				}
				prepareBackendRequest(pr.Out, pr.In, opts)
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				code := http.StatusBadGateway
				if errors.Is(err, context.DeadlineExceeded) {
					code = http.StatusGatewayTimeout
				}
				slog.Error("reverse-proxy: upstream error", "upstream", target.Host, "err", err)
				http.Error(w, "upstream error", code)
			},
		}
		return &proxyHandler{rp: rp, timeout: timeout}, nil
	})
}

// proxyHandler applies the optional per-request timeout, then proxies.
type proxyHandler struct {
	rp      *httputil.ReverseProxy
	timeout time.Duration
}

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.timeout > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
		defer cancel()
		r = r.WithContext(ctx)
	}
	h.rp.ServeHTTP(w, r)
}
