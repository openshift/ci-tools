package sentry

import (
	"encoding/json"
	"os"
)

func init() {
	hostname, err := os.Hostname()
	if err != nil {
		return
	}

	AddDefaultOptions(ServerName(hostname))
}

// ServerName allows you to configure the hostname reported to Sentry
// with an event.
func ServerName(hostname string) Option {
	return &serverNameOption{hostname}
}

type serverNameOption struct {
	hostname string
}

func (o *serverNameOption) Class() string {
	return "server_name"
}

func (o *serverNameOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.hostname)
}
