package sentry

import (
	"encoding/json"
	"os"
)

func init() {
	AddDefaultOptionProvider(func() Option {
		if env := os.Getenv("ENV"); env != "" {
			return Environment(env)
		}

		if env := os.Getenv("ENVIRONMENT"); env != "" {
			return Environment(env)
		}

		return nil
	})
}

// Environment allows you to configure the name of the environment
// you pass to Sentry with your event. This would usually be something
// like "production" or "staging".
func Environment(env string) Option {
	return &environmentOption{env}
}

type environmentOption struct {
	env string
}

func (o *environmentOption) Class() string {
	return "environment"
}

func (o *environmentOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.env)
}
