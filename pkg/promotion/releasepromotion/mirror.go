package releasepromotion

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/util/sets"
)

// mirrorConfig is the rules used to construct the mirroring.rules file for a given
// release stream (where ci-operator jobs promote their images). The mirroring.yaml
// closest to the directory created for a stream is used, which means teams can have
// rules at the release level (ocp/4.5), at the namespace level (ocp), or at the
// root level as defaults.
//
// Example configuration for OpenShift as it is today
//
// # This controls generation of the mirror stanzas for image streams within this directory. Each stream can be published
// # to multiple locations, and each source image can be tagged multiple times
// targets:
// - name: Public Origin Images
//   # The list of output version strings (per input source image).
//   versions:
//   - "{stream}"
//   - "{stream}.0"
//   # If specified, the stream name that will get tagged as latest.
//   version_latest: "4.5"
//   # The destination name for each source image.
//   # - {image} is the name of the image within a stream
//   # - {version} is the destination name as determined by the release (generally '4.5', '4.5.0', and 'latest' if master)
//   patterns:
//   - quay.io/openshift/origin-{image}:{version}
//   # Images in these locations take priority over images here because they include public versions.
//   # - {stream} is inferred from the name of the image stream
//   override_from:
//   - origin/{stream}
//   # Thes names images are never pulled from the source (they can however be provided by the override). They are
//   # string matches within the name. Use this to prevent images that contain private source from being
//   # exposed to a public audience.
//   skip_names:
//   - machine-os
//   - ironic-ipa-downloader
//   - ironic-machine-os-downloader
//   - ironic-static-ip-manager
type mirrorConfig struct {
	// Targets is a list of all possible targets of this release stream.
	Targets []mirrorConfigTarget `json:"targets"`
}

// mirrorConfigTarget defines a singel destination.
type mirrorConfigTarget struct {
	// Name is the user facing name for this rule, used in log output.
	Name string `json:"name"`
	// Versions are strings that define the {version} values a single image can be promoted to.
	// Accepts {stream} to parameterize the value (i.e. ocp/4.5 can use "{stream}" and "{stream}.0").
	// At least one version must be specified.
	Versions []string `json:"versions"`
	// VersionLatest is the name of a release stream that is considered latest (i.e. "4.5" when ocp master is "4.5").
	VersionLatest string `json:"version_latest"`
	// Patterns are the output locations applied per version - so that one image can be promoted to multiple repos.
	// Supports {image} and {version} replacements (image is the name the image is promoted as, i.e. it's "to")
	Patterns []string `json:"patterns"`
	// OverrideFrom defines an image stream that if present replaces the location. Used by OpenShift for Origin images
	// during publication (we have public variants of some images like machine-os-content that have to be used instead
	// of our private images). Supports the {stream} replacement.
	OverrideFrom []string `json:"override_from"`
	// SkipNames are images that are never published from the source stream (but can be published from the override stream).
	// Used to blacklist a private image from being published.
	SkipNames []string `json:"skip_names"`
}

func (t mirrorConfigTarget) IsSkipped(name string) bool {
	for _, skip := range t.SkipNames {
		if strings.Contains(name, skip) {
			return true
		}
	}
	return false
}

func readMirrorConfig(name string) (*mirrorConfig, error) {
	data, err := ioutil.ReadFile(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var config mirrorConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

func loadMirrorConfig(baseDir string, parts []string) (*mirrorConfig, error) {
	config, err := readMirrorConfig(filepath.Join(append(append([]string{baseDir}, parts...), "mirroring.yaml")...))
	if err != nil {
		return nil, err
	}
	if config != nil {
		return config, nil
	}
	if len(parts) == 0 {
		return nil, nil
	}
	return loadMirrorConfig(baseDir, parts[:len(parts)-1])
}

// WriteMirroringRules uses the mirroring.yaml files within a directory hierarchy to
// generate the mirroring rules for that release from a given source cluster to a target
// external registry.
func (m *Verifier) WriteMirroringRules(toDir, commandName string) error {
	// remove any old names under this directory
	if err := filepath.Walk(toDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if name := filepath.Base(info.Name()); name == "mirroring.rules" {
			return os.Remove(path)
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return err
	}

	// write mirroring rules
	for key, target := range m.targets {
		if len(target) == 0 {
			continue
		}
		// find the mirroring config from the current dir up to the release root
		config, err := loadMirrorConfig(toDir, key.Directory())
		if err != nil {
			return err
		}
		if config == nil {
			continue
		}

		dir := filepath.Join(append([]string{toDir}, key.Directory()...)...)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}

		names := sets.NewString()
		for key := range target {
			names.Insert(key)
		}
		streamName := key.Name
		if len(streamName) == 0 {
			streamName = key.Tag
		}
		sourceLocation := key.PullSpec()

		buf := &bytes.Buffer{}
		fmt.Fprintf(buf, "# generated by %s, do not edit\n", commandName)

		for _, name := range names.List() {
			for _, target := range config.Targets {
				if target.IsSkipped(name) {
					continue
				}
				versions := target.Versions
				if target.VersionLatest == streamName {
					versions = append(versions, "latest")
				}

				fmt.Fprintf(buf, "%s", strings.NewReplacer("{stream}", streamName, "{image}", name).Replace(sourceLocation))
				for _, version := range versions {
					for _, pattern := range target.Patterns {
						r := strings.NewReplacer("{version}", version, "{stream}", streamName, "{image}", name)
						s := r.Replace(r.Replace(pattern))
						fmt.Fprintf(buf, " %s", s)
					}
				}
				fmt.Fprintln(buf)
			}
		}
		if err := ioutil.WriteFile(filepath.Join(dir, "mirroring.rules"), buf.Bytes(), 0640); err != nil {
			return err
		}
	}
	return nil
}
