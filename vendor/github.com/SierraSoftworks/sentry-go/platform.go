package sentry

import (
	"encoding/json"
)

func init() {
	AddDefaultOptions(Platform("go"))
}

// Platform allows you to configure the platform reported to Sentry. This is used
// to customizae portions of the user interface.
func Platform(platform string) Option {
	return &platformOption{platform}
}

type platformOption struct {
	platform string
}

func (o *platformOption) Class() string {
	return "platform"
}

func (o *platformOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.platform)
}
