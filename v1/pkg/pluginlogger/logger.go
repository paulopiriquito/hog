package pluginlogger

import "fmt"

// Logger interface for KrakenD plugin logging
type Logger interface {
	Debug(v ...interface{})
	Info(v ...interface{})
	Warning(v ...interface{})
	Error(v ...interface{})
	Critical(v ...interface{})
	Fatal(v ...interface{})
}

// NoopLogger is an empty logger implementation used before KrakenD registers the real logger
type NoopLogger struct{}

func (n NoopLogger) Debug(_ ...interface{})    {}
func (n NoopLogger) Info(_ ...interface{})     {}
func (n NoopLogger) Warning(_ ...interface{})  {}
func (n NoopLogger) Error(_ ...interface{})    {}
func (n NoopLogger) Critical(_ ...interface{}) {}
func (n NoopLogger) Fatal(_ ...interface{})    {}

// RegisterLogger is a helper to register the KrakenD logger in a plugin
func RegisterLogger(current *Logger, v interface{}, pluginName string) bool {
	l, ok := v.(Logger)
	if !ok {
		return false
	}
	*current = l
	l.Debug(fmt.Sprintf("[PLUGIN: %s] Logger loaded", pluginName))
	return true
}
