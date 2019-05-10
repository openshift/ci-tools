package sentry

func init() {
	AddDefaultOptions(&sdkOption{
		Name:         "SierraSoftworks/sentry-go",
		Version:      version,
		Integrations: []string{},
	})
}

type sdkOption struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Integrations []string `json:"integrations"`
}

func (o *sdkOption) Class() string {
	return "sdk"
}
