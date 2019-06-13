package sentry

import "encoding/json"

// Modules allows you to specify the versions of various modules
// used by your application.
func Modules(moduleVersions map[string]string) Option {
	if moduleVersions == nil {
		return nil
	}

	return &modulesOption{moduleVersions}
}

type modulesOption struct {
	moduleVersions map[string]string
}

func (o *modulesOption) Class() string {
	return "modules"
}

func (o *modulesOption) Merge(old Option) Option {
	if old, ok := old.(*modulesOption); ok {
		moduleVersions := make(map[string]string, len(old.moduleVersions))
		for k, v := range old.moduleVersions {
			moduleVersions[k] = v
		}

		for k, v := range o.moduleVersions {
			moduleVersions[k] = v
		}

		return &modulesOption{moduleVersions}
	}

	return o
}

func (o *modulesOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.moduleVersions)
}
