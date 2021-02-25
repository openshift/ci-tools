package api

import "os"

// prowArtifactsEnv is the directory Prow wants us to put artifacts into for upload
const prowArtifactsEnv string = "ARTIFACTS"

func Artifacts() (string, bool) {
	return os.LookupEnv(prowArtifactsEnv)
}
