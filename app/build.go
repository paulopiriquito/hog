// Package app assembles loaded resources into a running http.Handler: it parses
// resources into typed config and builds a per-route middleware chain.
package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"

	"github.com/paulopiriquito/hog/auth"
	"github.com/paulopiriquito/hog/chain"
	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/gateway"
	"github.com/paulopiriquito/hog/idp"
	"github.com/paulopiriquito/hog/registry"
	"github.com/paulopiriquito/hog/route"
	"github.com/paulopiriquito/hog/selector"
	"github.com/paulopiriquito/hog/session"
	"gopkg.in/yaml.v3"
)

// PluginInstance is a configured request- or response-plugin declared by a
// resource. Order is its document index, used to order plugins within a slot.
type PluginInstance struct {
	Name     string
	Type     string
	Selector selector.Selector
	Config   yaml.Node
	Order    int
}

// pluginSpec is the decoded spec of a RequestPlugin/ResponsePlugin resource.
type pluginSpec struct {
	Type     string            `yaml:"type"`
	Selector selector.Selector `yaml:"selector"`
	Config   yaml.Node         `yaml:"config"`
}

// App is the assembled runtime: the request handler plus shared services that
// auth middlewares and endpoints depend on (the active IdP today; session
// manager and state provider in later sub-projects). IdP is nil if none configured.
type App struct {
	Handler http.Handler
	IdP     idp.IdP
	Session session.Manager
}

// Config is the typed, parsed configuration of a HOG instance.
type Config struct {
	Gateway         gateway.Settings
	Routes          []route.Route
	Groups          []route.RouteGroup
	RequestPlugins  []PluginInstance
	ResponsePlugins []PluginInstance
	IdPResources    []config.Resource
}

// Parse converts loaded resources into a typed Config. Document order (the
// slice order) is preserved and recorded on each plugin instance.
func Parse(resources []config.Resource) (Config, error) {
	var cfg Config
	var gatewaySeen bool
	for i, r := range resources {
		switch r.Kind {
		case config.KindGateway:
			if gatewaySeen {
				return Config{}, fmt.Errorf("config must contain exactly one Gateway resource (duplicate %q)", r.Metadata.Name)
			}
			g, err := gateway.FromResource(r)
			if err != nil {
				return Config{}, err
			}
			cfg.Gateway = g
			gatewaySeen = true
		case config.KindRoute:
			rt, err := route.ParseRoute(r)
			if err != nil {
				return Config{}, err
			}
			cfg.Routes = append(cfg.Routes, rt)
		case config.KindRouteGroup:
			g, err := route.ParseGroup(r)
			if err != nil {
				return Config{}, err
			}
			cfg.Groups = append(cfg.Groups, g)
		case config.KindRequestPlugin, config.KindResponsePlugin:
			pi, err := parsePlugin(r, i)
			if err != nil {
				return Config{}, err
			}
			if r.Kind == config.KindRequestPlugin {
				cfg.RequestPlugins = append(cfg.RequestPlugins, pi)
			} else {
				cfg.ResponsePlugins = append(cfg.ResponsePlugins, pi)
			}
		case config.KindIdP:
			cfg.IdPResources = append(cfg.IdPResources, r)
		default:
			return Config{}, fmt.Errorf("unknown resource kind %q (%s)", r.Kind, r.Metadata.Name)
		}
	}
	if !gatewaySeen {
		return Config{}, fmt.Errorf("config must contain exactly one Gateway resource")
	}
	return cfg, nil
}

func parsePlugin(r config.Resource, order int) (PluginInstance, error) {
	var s pluginSpec
	if err := r.Spec.Decode(&s); err != nil {
		return PluginInstance{}, fmt.Errorf("%s %q: %w", r.Kind, r.Metadata.Name, err)
	}
	if s.Type == "" {
		return PluginInstance{}, fmt.Errorf("%s %q: spec.type is required", r.Kind, r.Metadata.Name)
	}
	return PluginInstance{
		Name: r.Metadata.Name, Type: s.Type, Selector: s.Selector, Config: s.Config, Order: order,
	}, nil
}

// Build assembles cfg into an *App: one ServeMux where each route's pattern
// maps to its composed chain (fixed skeleton + matching request-plugins +
// matching response-plugins + terminal), plus the single active IdP (if any).
// reg supplies module instances.
func Build(cfg Config, reg *registry.Registry, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	active, err := buildIdP(cfg.IdPResources, reg)
	if err != nil {
		return nil, err
	}
	idCfg, err := session.ParseIdentity(cfg.Gateway.Identity)
	if err != nil {
		return nil, err
	}
	sess, sessCfg, err := buildSession(cfg.Gateway, idCfg, reg, active)
	if err != nil {
		return nil, err
	}
	// Identity resolution gates. SessionGate is nil-safe (pass-through when no
	// cookie manager); the Bearer gate is available whenever an IdP is configured.
	sessionGate := auth.SessionGate(sess)
	var bearerGate chain.Middleware
	if active != nil {
		bearerGate = auth.BearerGate(active, idCfg, logger)
	}
	groupsAs := "groups"
	if idCfg.Groups != nil && idCfg.Groups.As != "" {
		groupsAs = idCfg.Groups.As
	}
	loginPath := "/auth/login"
	var authCfg auth.Config
	if sess != nil {
		authCfg, err = auth.FromYAML(cfg.Gateway.Auth)
		if err != nil {
			return nil, err
		}
		loginPath = authCfg.LoginPath
	}
	if sess != nil && active == nil {
		logger.Warn("session configured without an IdP: protected routes will redirect to the login path, but the auth endpoints are not mounted",
			"loginPath", loginPath)
	}
	mux := http.NewServeMux()
	seen := make(map[string]string) // match pattern -> first route name using it
	for _, rt := range cfg.Routes {
		if prev, dup := seen[rt.Match]; dup {
			return nil, fmt.Errorf("route %q: duplicate match %q (already used by route %q)", rt.Name, rt.Match, prev)
		}
		seen[rt.Match] = rt.Name
		terminal, err := buildTerminal(rt, reg)
		if err != nil {
			return nil, err
		}
		reqMW, err := resolvePlugins(config.KindRequestPlugin, cfg.RequestPlugins, rt.Labels, reg)
		if err != nil {
			return nil, err
		}
		respMW, err := resolvePlugins(config.KindResponsePlugin, cfg.ResponsePlugins, rt.Labels, reg)
		if err != nil {
			return nil, err
		}
		// Resolve every route's effective policy so an invalid type/auth value
		// fails the build regardless of whether a session manager is configured.
		resolved, rerr := route.Resolve(rt, cfg.Groups)
		if rerr != nil {
			return nil, rerr
		}
		authActive := sess != nil || active != nil
		var gates chain.Gates
		if authActive {
			slot := sessionGate
			if resolved.Type == "service" && bearerGate != nil {
				slot = combine(sessionGate, bearerGate) // cookie (outer) then bearer (inner)
			}
			gates = chain.Gates{
				Session:    slot,
				AuthGate:   auth.AuthGate(resolved.Auth == "required", resolved.Type == "app", loginPath),
				Projection: auth.ProjectionGate(resolved.Projection, groupsAs),
			}
		} else if resolved.Auth == "required" {
			logger.Warn("route requires auth but neither a session nor an IdP is configured; it will be served WITHOUT enforcement",
				"route", rt.Name, "match", rt.Match)
		}
		mws := append([]chain.Middleware{}, chain.Skeleton(logger, gates)...)
		mws = append(mws, reqMW...)
		mws = append(mws, respMW...)
		mux.Handle(rt.Match, chain.Compose(terminal, mws...))
	}
	if active != nil && sess != nil {
		sealer, err := session.NewSealer(sessCfg.Key)
		if err != nil {
			return nil, err
		}
		h := auth.NewHandlers(active, sess, sealer, authCfg, *sessCfg, idCfg)
		mux.HandleFunc(authCfg.LoginPath, h.Login)
		mux.HandleFunc(authCfg.LogoutPath, h.Logout)
		mux.HandleFunc(callbackPath(cfg.IdPResources), h.Callback)
		mux.Handle(sessCfg.InfoPath, session.InfoHandler(sess))
	}
	return &App{Handler: mux, IdP: active, Session: sess}, nil
}

// callbackPath derives the OAuth callback path from the single IdP resource's
// redirectURL (it must match what the IdP redirects to). Default /auth/callback.
func callbackPath(resources []config.Resource) string {
	if len(resources) != 1 {
		return "/auth/callback"
	}
	var spec struct {
		RedirectURL string `yaml:"redirectURL"`
	}
	if err := resources[0].Spec.Decode(&spec); err != nil || spec.RedirectURL == "" {
		return "/auth/callback"
	}
	u, err := url.Parse(spec.RedirectURL)
	if err != nil || u.Path == "" {
		return "/auth/callback"
	}
	return u.Path
}

// combine runs a (outer) then b (inner) as a single Middleware, so the request
// flows a → b → next on the way in.
func combine(a, b chain.Middleware) chain.Middleware {
	return chain.Func(func(next http.Handler) http.Handler { return a.Wrap(b.Wrap(next)) })
}

// buildSession constructs the session Manager: a server-side stateManager when a
// stateProvider block is configured (the IdP is threaded in for silent refresh),
// else the stateless cookie manager. An absent session block ⇒ nil manager (or an
// error if a stateProvider is set without one). A bad session key, an unknown
// stateProvider type, or store-construction failure all fail-fast.
func buildSession(g gateway.Settings, idCfg session.IdentityConfig, reg *registry.Registry, active idp.IdP) (session.Manager, *session.Config, error) {
	if g.Session.Kind == 0 {
		if g.StateProvider.Kind != 0 {
			return nil, nil, fmt.Errorf("stateProvider is configured but no session block is present (the session key encrypts the at-rest record)")
		}
		return nil, nil, nil
	}
	cfg, err := session.FromYAML(g.Session)
	if err != nil {
		return nil, nil, err
	}
	cfg.PassportClaims = idCfg.Claims
	cfg.Groups = idCfg.Groups

	if g.StateProvider.Kind != 0 {
		spc, err := session.ParseStateProvider(g.StateProvider)
		if err != nil {
			return nil, nil, err
		}
		raw, err := reg.Build(config.KindStateProvider, spc.Type, registry.RawConfig{Node: spc.Config})
		if err != nil {
			return nil, nil, fmt.Errorf("stateProvider %q: %w", spc.Type, err)
		}
		store, ok := raw.(session.StateStore)
		if !ok {
			return nil, nil, fmt.Errorf("stateProvider %q is not a session.StateStore", spc.Type)
		}
		m, err := session.NewStateManager(cfg, store, active, spc.RefreshSkew, spc.KeyPrefix)
		if err != nil {
			return nil, nil, fmt.Errorf("stateProvider %q: %w", spc.Type, err)
		}
		return m, &cfg, nil
	}

	m, err := session.NewManager(cfg)
	if err != nil {
		return nil, nil, err
	}
	return m, &cfg, nil
}

// buildIdP instantiates the single active IdP (fail-fast). Zero is allowed; two
// or more is an error for now ("single now, multi-ready").
func buildIdP(resources []config.Resource, reg *registry.Registry) (idp.IdP, error) {
	switch len(resources) {
	case 0:
		return nil, nil
	case 1:
		r := resources[0]
		var spec struct {
			Type string `yaml:"type"`
		}
		if err := r.Spec.Decode(&spec); err != nil {
			return nil, fmt.Errorf("idp %q: %w", r.Metadata.Name, err)
		}
		if spec.Type == "" {
			return nil, fmt.Errorf("idp %q: spec.type is required", r.Metadata.Name)
		}
		m, err := reg.Build(config.KindIdP, spec.Type, registry.RawConfig{Node: r.Spec})
		if err != nil {
			return nil, err
		}
		got, ok := m.(idp.IdP)
		if !ok {
			return nil, fmt.Errorf("idp %q (type %q) is not an idp.IdP", r.Metadata.Name, spec.Type)
		}
		return got, nil
	default:
		return nil, fmt.Errorf("config has %d IdP resources; exactly one is supported for now", len(resources))
	}
}

func buildTerminal(rt route.Route, reg *registry.Registry) (http.Handler, error) {
	m, err := reg.Build(config.KindTerminalHandler, rt.Handler.Type, registry.RawConfig{Node: rt.Handler.Config})
	if err != nil {
		return nil, fmt.Errorf("route %q: %w", rt.Name, err)
	}
	h, ok := m.(http.Handler)
	if !ok {
		return nil, fmt.Errorf("route %q: handler %q is not an http.Handler", rt.Name, rt.Handler.Type)
	}
	return h, nil
}

// resolvePlugins selects the instances whose selector matches the route labels,
// orders them by document order, and instantiates them as middlewares.
func resolvePlugins(kind string, all []PluginInstance, labels map[string]string, reg *registry.Registry) ([]chain.Middleware, error) {
	var matched []PluginInstance
	for _, pi := range all {
		if pi.Selector.Matches(labels) {
			matched = append(matched, pi)
		}
	}
	// Sort by document order. matched is already in document order today (Parse
	// appends in range order), but sorting makes the ordering contract explicit.
	sort.Slice(matched, func(i, j int) bool { return matched[i].Order < matched[j].Order })

	out := make([]chain.Middleware, 0, len(matched))
	for _, pi := range matched {
		m, err := reg.Build(kind, pi.Type, registry.RawConfig{Node: pi.Config})
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", kind, pi.Name, err)
		}
		mw, ok := m.(chain.Middleware)
		if !ok {
			return nil, fmt.Errorf("%s %q (type %q) is not a Middleware", kind, pi.Name, pi.Type)
		}
		out = append(out, mw)
	}
	return out, nil
}
