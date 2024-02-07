package utils

import (
	"fmt"
	"os"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	pipelineEnvPrefix = "LOCAL_"
	initialEnvPrefix  = "INITIAL_"
	imageEnvPrefix    = "IMAGE_"
	releaseEnvPrefix  = "RELEASE_"

	// ImageFormatEnv is the environment we use to hold the base pull spec
	ImageFormatEnv = "IMAGE_FORMAT"

	OverrideImageEnvPrefix = "OVERRIDE_IMAGE_"
)

var knownPrefixes = map[string]string{
	api.PipelineImageStream:                      pipelineEnvPrefix + imageEnvPrefix,
	api.ReleaseStreamFor(api.InitialReleaseName): initialEnvPrefix + imageEnvPrefix,
	api.ReleaseStreamFor(api.LatestReleaseName):  imageEnvPrefix,
	api.ReleaseImageStream:                       releaseEnvPrefix + imageEnvPrefix,
}

func escapedImageName(name string) string {
	return strings.ToUpper(strings.Replace(name, "-", "_", -1))
}

func unescapedImageName(name string) string {
	return strings.ToLower(strings.Replace(name, "_", "-", -1))
}

func imageFromEnv(stream, envVar string) (string, bool) {
	prefix, known := knownPrefixes[stream]
	if !known {
		// not a stream we know about, can't unfurl
		return "", false
	}
	if !strings.HasPrefix(envVar, knownPrefixes[stream]) {
		// improperly formatted env, does not match stream
		return "", false
	}
	return unescapedImageName(strings.TrimPrefix(envVar, prefix)), true
}

// LinkForEnv determines what step link was required by a user when they
// added a parameter to their template, if any.
func LinkForEnv(envVar string) (api.StepLink, bool) {
	switch {
	case envVar == ImageFormatEnv:
		// this is a special case, as we expose this as a specific API
		// to the user, unlike the rest of these being implicit/computed
		return api.ImagesReadyLink(), true
	case IsPipelineImageEnv(envVar):
		image, ok := imageFromEnv(api.PipelineImageStream, envVar)
		if !ok {
			return nil, false
		}
		return api.InternalImageLink(api.PipelineImageStreamTagReference(image)), true
	case IsStableImageEnv(envVar):
		// we don't know what will produce this parameter,
		// so we assume it will come from the release import
		return api.ReleaseImagesLink(api.LatestReleaseName), true
	case IsInitialImageEnv(envVar):
		return api.ReleaseImagesLink(api.InitialReleaseName), true
	case IsReleaseImageEnv(envVar):
		return api.ReleasePayloadImageLink(ReleaseNameFrom(envVar)), true
	default:
		return nil, false
	}
}

// EnvVarFor determines the environment variable used to
// expose a pull spec for an ImageStreamTag in the test
// namespace to test workloads.
func EnvVarFor(stream, name string) (string, error) {
	if _, ok := knownPrefixes[stream]; !ok {
		return "", fmt.Errorf("stream %q not recognized", stream)
	}
	return validatedEnvVarFor(stream, name), nil
}

// validatedEnvVarFor assumes the caller has checked the validity
// of the stream name and does not error
func validatedEnvVarFor(stream, name string) string {
	return knownPrefixes[stream] + escapedImageName(name)
}

// PipelineImageEnvFor determines the environment variable
// used to expose a pull spec for a pipeline ImageStreamTag
// in the test namespace to test workloads.
func PipelineImageEnvFor(name api.PipelineImageStreamTagReference) string {
	return validatedEnvVarFor(api.PipelineImageStream, string(name))
}

// IsPipelineImageEnv determines if an env var holds a pull
// spec for a tag under the pipeline image stream
func IsPipelineImageEnv(envVar string) bool {
	return strings.HasPrefix(envVar, knownPrefixes[api.PipelineImageStream])
}

// StableImageEnv determines the environment variable
// used to expose a pull spec for a stable ImageStreamTag
// in the test namespace to test workloads.
func StableImageEnv(name string) string {
	return validatedEnvVarFor(api.ReleaseStreamFor(api.LatestReleaseName), name)
}

// IsStableImageEnv determines if an env var holds a pull
// spec for a tag under the stable image stream
func IsStableImageEnv(envVar string) bool {
	return strings.HasPrefix(envVar, knownPrefixes[api.ReleaseStreamFor(api.LatestReleaseName)])
}

// StableImageNameFrom gets an image name from an env name
func StableImageNameFrom(envVar string) string {
	// we know that we will be able to unfurl
	name, _ := imageFromEnv(api.ReleaseStreamFor(api.LatestReleaseName), envVar)
	return name
}

// InitialImageEnv determines the environment variable
// used to expose a pull spec for a initial ImageStreamTag
// in the test namespace to test workloads.
func InitialImageEnv(name string) string {
	return validatedEnvVarFor(api.ReleaseStreamFor(api.InitialReleaseName), name)
}

// IsInitialImageEnv determines if an env var holds a pull
// spec for a tag under the initial image stream
func IsInitialImageEnv(envVar string) bool {
	return strings.HasPrefix(envVar, knownPrefixes[api.ReleaseStreamFor(api.InitialReleaseName)])
}

// ReleaseImageEnv determines the environment variable
// used to expose a pull spec for a release ImageStreamTag
// in the test namespace to test workloads.
func ReleaseImageEnv(name string) string {
	return validatedEnvVarFor(api.ReleaseImageStream, name)
}

// IsReleaseImageEnv determines if an env var holds a pull
// spec for a tag under the release image stream
func IsReleaseImageEnv(envVar string) bool {
	return strings.HasPrefix(envVar, knownPrefixes[api.ReleaseImageStream])
}

// ReleaseNameFrom determines the name of the release payload
// that the pull spec points to.
func ReleaseNameFrom(envVar string) string {
	// we know that we will be able to unfurl
	name, _ := imageFromEnv(api.ReleaseImageStream, envVar)
	return name
}

// GetOverriddenImages finds all occurrences of OVERRIDE_IMAGE_* in env vars,
// unescapes the tag name and maps it to its value
func GetOverriddenImages() map[string]string {
	images := make(map[string]string)
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, OverrideImageEnvPrefix) {
			split := strings.Split(env, "=")
			key, value := split[0], split[1]
			tag := unescapedImageName(strings.TrimPrefix(key, OverrideImageEnvPrefix))
			images[tag] = value
		}
	}
	return images
}

// OverrideImageEnv generates the proper env var name for an image override
func OverrideImageEnv(name string) string {
	return fmt.Sprintf("%s%s", OverrideImageEnvPrefix, escapedImageName(name))
}
