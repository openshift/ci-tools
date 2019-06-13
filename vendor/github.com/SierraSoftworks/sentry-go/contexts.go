package sentry

import (
	"encoding/json"
	"runtime"
	"strings"

	"github.com/matishsiao/goInfo"
)

func init() {
	gi := goInfo.GetInfo()

	AddDefaultOptions(
		RuntimeContext("go", strings.TrimPrefix(runtime.Version(), "go")),
		OSContext(&OSContextInfo{
			Name:          gi.GoOS,
			Version:       gi.OS,
			KernelVersion: gi.Core,
		}),
		DeviceContext(&DeviceContextInfo{
			Architecture: gi.Platform,
			Family:       gi.Kernel,
			Model:        "Unknown",
		}),
	)
}

// OSContextInfo describes the operating system that your application
// is running on.
type OSContextInfo struct {
	Type          string `json:"type,omitempty"`
	Name          string `json:"name"`
	Version       string `json:"version,omitempty"`
	Build         string `json:"build,omitempty"`
	KernelVersion string `json:"kernel_version,omitempty"`
	Rooted        bool   `json:"rooted,omitempty"`
}

// OSContext allows you to set the context describing
// the operating system that your application is running on.
func OSContext(info *OSContextInfo) Option {
	return Context("os", info)
}

// DeviceContextInfo describes the device that your application
// is running on.
type DeviceContextInfo struct {
	Type         string `json:"type,omitempty"`
	Name         string `json:"name"`
	Family       string `json:"family,omitempty"`
	Model        string `json:"model,omitempty"`
	ModelID      string `json:"model_id,omitempty"`
	Architecture string `json:"arch,omitempty"`
	BatteryLevel int    `json:"battery_level,omitempty"`
	Orientation  string `json:"orientation,omitempty"`
}

// DeviceContext allows you to set the context describing the
// device that your application is being executed on.
func DeviceContext(info *DeviceContextInfo) Option {
	return Context("device", info)
}

// RuntimeContext allows you to set the information
// pertaining to the runtime that your program is
// executing on.
func RuntimeContext(name, version string) Option {
	return Context("runtime", map[string]string{
		"name":    name,
		"version": version,
	})
}

// Context allows you to manually set a context entry
// by providing its key and the data to accompany it.
// This is a low-level method and you should be familiar
// with the correct usage of contexts before using it.
// https://docs.sentry.io/clientdev/interfaces/contexts/
func Context(key string, data interface{}) Option {
	return &contextOption{
		contexts: map[string]interface{}{
			key: data,
		},
	}
}

type contextOption struct {
	contexts map[string]interface{}
}

func (o *contextOption) Class() string {
	return "contexts"
}

func (o *contextOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.contexts)
}

func (o *contextOption) Merge(old Option) Option {
	if old, ok := old.(*contextOption); ok {
		ctx := map[string]interface{}{}
		for k, v := range old.contexts {
			ctx[k] = v
		}

		for k, v := range o.contexts {
			ctx[k] = v
		}

		return &contextOption{ctx}
	}

	return o
}
