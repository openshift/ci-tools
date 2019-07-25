// +build go1.12

package sentry

import (
	"runtime/debug"
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if ok {
		mods := map[string]string{}
		for _, mod := range info.Deps {
			mods[mod.Path] = mod.Version
		}

		AddDefaultOptions(Modules(mods))
	}
}
