package sentry

import (
	"encoding/json"
)

// Release allows you to configure the application release version
// reported to Sentry with an event.
func Release(version string) Option {
	return &releaseOption{version}
}

type releaseOption struct {
	version string
}

func (o *releaseOption) Class() string {
	return "release"
}

func (o *releaseOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.version)
}
