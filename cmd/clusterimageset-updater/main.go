package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/test-infra/prow/interrupts"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release/prerelease"
)

type options struct {
	poolDir   string
	outputDir string
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.poolDir, "pools", "", "Path to directory containing cluster pool specs")
	fs.StringVar(&o.outputDir, "imagesets", "", "Path to directory containing clusterimagesets")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func (o *options) validate() error {
	if len(o.poolDir) == 0 {
		return errors.New("--pools is not defined")
	}

	if len(o.outputDir) == 0 {
		return errors.New("--imagesets is not defined")
	}
	return nil
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather options")
	}
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid option")
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	// key: version_in; value: list of file paths
	autoPools := make(map[string][]string)
	if err := filepath.WalkDir(o.poolDir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), "_clusterpool.yaml") {
			return nil
		}
		raw, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}
		pool := hivev1.ClusterPool{}
		if err := yaml.Unmarshal(raw, &pool); err != nil {
			return err
		}
		if pool.Labels != nil && pool.Labels["version_in"] != "" {
			version := pool.Labels["version_in"]
			autoPools[version] = append(autoPools[version], path)
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Failed to get list of clusterpools setting `version_in`")
	}

	versionToPullspec := make(map[string]string)
	for version := range autoPools {
		versionBounds, err := api.BoundsFromQuery(version)
		if err != nil {
			logrus.WithError(err).Fatalf("Failed to convert `version_in` of `%s` to bounds", version)
		}
		release := api.Prerelease{
			Product:       api.ReleaseProductOCP,
			Architecture:  api.ReleaseArchitectureAMD64,
			VersionBounds: *versionBounds,
		}
		pullSpec, err := prerelease.ResolvePullSpec(&http.Client{}, release)
		if err != nil {
			logrus.WithError(err).Fatalf("Failed to get pullspec for version range `%s`", version)
		}
		versionToPullspec[version] = pullSpec
	}

	// keep list of outdated or removed cluster image set definitions to delete
	var toDelete []string
	if err := filepath.WalkDir(o.outputDir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), "_clusterimageset.yaml") {
			return nil
		}
		raw, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}
		imageset := hivev1.ClusterImageSet{}
		if err := yaml.Unmarshal(raw, &imageset); err != nil {
			return err
		}
		if imageset.Annotations != nil && imageset.Annotations["version_in"] != "" {
			isCurrent := false
			versionIn := imageset.Annotations["version_in"]
			for version := range autoPools {
				if version == versionIn {
					if imageset.Spec.ReleaseImage == versionToPullspec[version] {
						isCurrent = true
						delete(autoPools, version)
						delete(versionToPullspec, version)
					}
					break
				}
			}
			if !isCurrent {
				toDelete = append(toDelete, path)
				return nil
			}
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Failed to get list of clusterpools setting `version_in`")
	}

	// any remaining items in autopools/versionToPullspec need to be updated
	for version, pullspec := range versionToPullspec {
		name, err := nameFromPullspec(pullspec, version)
		if err != nil {
			// this shouldn't happen
			logrus.WithError(err).Fatalf("Failed to generate clusterimageset name for version %s", version)
		}
		clusterimageset := hivev1.ClusterImageSet{
			ObjectMeta: v1.ObjectMeta{
				Name: name,
				Annotations: map[string]string{
					"version_in": version,
				},
			},
			Spec: hivev1.ClusterImageSetSpec{
				ReleaseImage: pullspec,
			},
		}
		raw, err := yaml.Marshal(clusterimageset)
		if err != nil {
			logrus.WithError(err).Fatalf("Could not marshal yaml for clusterimageset %s", name)
		}
		if err := ioutil.WriteFile(filepath.Join(o.outputDir, fmt.Sprintf("%s_clusterimageset.yaml", name)), raw, 0644); err != nil {
			logrus.WithError(err).Fatalf("Failed to write file for clusterimageset %s", name)
		}
	}

	// delete old clusterimagesets
	for _, path := range toDelete {
		if err := os.Remove(path); err != nil {
			logrus.WithError(err).Fatalf("Failed to delete file %s", err)
		}
	}

	// update all clusterpool specs
	for version, files := range autoPools {
		imagesetName, err := nameFromPullspec(versionToPullspec[version], version)
		if err != nil {
			// this shouldn't happen
			logrus.WithError(err).Fatalf("Failed to generate clusterimageset name for version %s", version)
		}
		for _, path := range files {
			raw, err := ioutil.ReadFile(path)
			if err != nil {
				logrus.WithError(err).Fatalf("Failed to read file %s", path)
			}
			// unmarshalling a remarshalling the clusterpool object would result in all fields being reordered alpabetically
			// and `status` being unnecessarily included. To avoid that, do a simpler regex based replacement
			newFile := imagesetReplacer.ReplaceAll(raw, []byte(fmt.Sprintf("imageSetRef:\n    name: %s\n", imagesetName)))
			if err := ioutil.WriteFile(path, newFile, 0644); err != nil {
				logrus.WithError(err).Fatalf("Failed to write updated file %s", path)
			}
		}
	}
}

var imagesetReplacer = regexp.MustCompile("imageSetRef:\n    name: .*\n")

func nameFromPullspec(pullspec string, version string) (string, error) {
	bounds, err := api.BoundsFromQuery(version)
	if err != nil {
		return "", err
	}
	baseName := pullspec[strings.LastIndex(pullspec, "ocp-release"):]
	// handle names like ocp-release:4.8.3-x86_64, generated by a version_in like ">4.8.0-0 <4.9.0-0"
	baseName = strings.ReplaceAll(baseName, ":", "-")
	// handle names like ocp-release@sha256:..., generated by a version_in like ">4.8.0 <4.9.0"
	baseName = strings.ReplaceAll(baseName, "@", "-")
	return fmt.Sprintf("%s-for-%s-to-%s", baseName, bounds.Lower, bounds.Upper), nil
}
