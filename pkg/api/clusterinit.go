package api

import (
	"embed"
)

//go:embed multiarchbuildconfig/v1/ci.openshift.io_multiarchbuildconfigs.yaml
var MultiArchBuildConfigManifest embed.FS
