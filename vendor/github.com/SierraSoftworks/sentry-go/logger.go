package sentry

import (
	"encoding/json"
)

func init() {
	AddDefaultOptions(Logger("root"))
}

// Logger allows you to configure the hostname reported to Sentry
// with an event.
func Logger(logger string) Option {
	return &loggerOption{logger}
}

type loggerOption struct {
	logger string
}

func (o *loggerOption) Class() string {
	return "logger"
}

func (o *loggerOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.logger)
}
