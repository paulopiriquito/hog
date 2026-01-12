package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/go-viper/mapstructure/v2"
	"github.com/paulopiriquito/hog/pkg/pluginlogger"
)

var pluginName = "hog-static-content"

var HandlerRegisterer = registerer(pluginName)

type registerer string

func (r registerer) RegisterHandlers(f func(
	name string,
	handler func(context.Context, map[string]interface{}, http.Handler) (http.Handler, error),
)) {
	f(string(r), r.registerHandlers)
}

// The static content plugin will look for this configuration:
/*
	   "extra_config": {
	       "plugin/http-server": {
	           "name":["hog-static-content"],
	           "hog-static-content": {
	               "static": [{
						"path-prefix": "/*",
						"service-host": "http://web-example"
						"keep-unsafe-headers": false
					}],
					"service-gateway": {
						"path-prefix": ["/api/*"],
					}
	           }
	       }
	   }
*/
func (r registerer) registerHandlers(_ context.Context, extra map[string]interface{}, h http.Handler) (http.Handler, error) {
	logger.Debug(fmt.Sprintf("Loading static-content plugin config"))
	config, err := loadPluginConfig(extra)
	if err != nil {
		return nil, errors.Join(errors.New("failed to load plugin config"), err)
	}

	logger.Debug(fmt.Sprintf("Registering static content routes"))
	for _, s := range config.Static {
		logger.Debug(fmt.Sprintf("The plugin is now serving static content on the path %s", s.PathPrefix))
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handleStaticContent(config, writer, request, h)
	}), nil
}

func main() {

}

type PluginConfig struct {
	Static         []StaticConfig       `mapstructure:"static"`
	ServiceGateway ServiceGatewayConfig `mapstructure:"service-gateway"`
	Auth           AuthConfig           `mapstructure:"auth"`
}

type StaticConfig struct {
	PathPrefix        string `mapstructure:"path-prefix"`
	ServiceHost       string `mapstructure:"service-host"`
	KeepUnsafeHeaders bool   `mapstructure:"keep-unsafe-headers"`
	Auth              bool   `mapstructure:"auth"`
}

type ServiceGatewayConfig struct {
	PathPrefix []string `mapstructure:"path-prefix"`
}

type AuthConfig struct {
	SessionCookieName string `mapstructure:"session-cookie-name"`
	SessionKey        string `mapstructure:"session-key"`
	SimpleAuthUrl     string `mapstructure:"simple-auth-url"`
}

func loadPluginConfig(cfg map[string]interface{}) (PluginConfig, error) {
	var pc PluginConfig
	err := mapstructure.Decode(cfg[pluginName], &pc)
	if err != nil {
		return pc, fmt.Errorf("failed to decode config: %w", err)
	}

	// Environment variables take precedence over config file
	// Priority: ENV VAR > Config File > Defaults

	// Session Cookie Name
	if envCookieName := os.Getenv("AUTH_COOKIE_NAME"); envCookieName != "" {
		pc.Auth.SessionCookieName = envCookieName
		logger.Debug(fmt.Sprintf("Using AUTH_COOKIE_NAME from environment: %s", envCookieName))
	} else if pc.Auth.SessionCookieName == "" {
		pc.Auth.SessionCookieName = "auth_session"
		logger.Debug(fmt.Sprintf("Using default session cookie name: %s", pc.Auth.SessionCookieName))
	}

	// Session Key
	if envSessionKey := os.Getenv("AUTH_COOKIE_KEY"); envSessionKey != "" {
		pc.Auth.SessionKey = envSessionKey
		logger.Debug("Using AUTH_COOKIE_KEY from environment")
	}

	// Simple Auth URL default
	if pc.Auth.SimpleAuthUrl == "" {
		pc.Auth.SimpleAuthUrl = "/oauth/simple-auth"
	}

	return pc, nil
}

// This logger is replaced by the RegisterLogger method to load the one from KrakenD
var logger pluginlogger.Logger = pluginlogger.NoopLogger{}

func (registerer) RegisterLogger(v interface{}) {
	pluginlogger.RegisterLogger(&logger, v, string(HandlerRegisterer))
}
