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
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

	fs.StringVar(&o.poolDir, "pools", "", "Path to directory containing cluster pool specs (*_clusterpool.yaml files)")
	fs.StringVar(&o.outputDir, "imagesets", "", "Path to directory containing clusterimagesets  (*_clusterimageset.yaml files)")

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

	// key: version_in; value: list of file paths
	poolFilesByVersion := make(map[string][]string)
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
			poolFilesByVersion[version] = append(poolFilesByVersion[version], path)
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Failed to get list of clusterpools setting `version_in`")
	}

	versionToPullspec := make(map[string]string)
	for version := range poolFilesByVersion {
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
			for version := range poolFilesByVersion {
				if version == versionIn {
					if imageset.Spec.ReleaseImage == versionToPullspec[version] {
						isCurrent = true
						delete(poolFilesByVersion, version)
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

	// make as much progress as possible and print list of errors at end of command
	var errs []error

	// any remaining items in autopools/versionToPullspec need to be updated
	for version, pullspec := range versionToPullspec {
		name, err := nameFromPullspec(pullspec, version)
		if err != nil {
			// this shouldn't happen
			errs = append(errs, fmt.Errorf("Failed to generate clusterimageset name for version %s: %w", version, err))
			continue
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
			errs = append(errs, fmt.Errorf("Could not marshal yaml for clusterimageset %s: %w", name, err))
			continue
		}
		if err := ioutil.WriteFile(filepath.Join(o.outputDir, fmt.Sprintf("%s_clusterimageset.yaml", name)), raw, 0644); err != nil {
			errs = append(errs, fmt.Errorf("Failed to write file for clusterimageset %s: %w", name, err))
		}
	}

	// delete old clusterimagesets
	for _, path := range toDelete {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("Failed to delete file %s: %w", path, err))
		}
	}

	// update all clusterpool specs
	for version, files := range poolFilesByVersion {
		imagesetName, err := nameFromPullspec(versionToPullspec[version], version)
		if err != nil {
			// this shouldn't happen
			errs = append(errs, fmt.Errorf("Failed to generate clusterimageset name for version %s: %w", version, err))
			continue
		}
		for _, path := range files {
			raw, err := ioutil.ReadFile(path)
			if err != nil {
				errs = append(errs, fmt.Errorf("Failed to read file %s: %w", path, err))
				continue
			}
			var newClusterPool hivev1.ClusterPool
			if err := yaml.Unmarshal(raw, &newClusterPool); err != nil {
				errs = append(errs, fmt.Errorf("Failed to unmarshal clusterpool %s: %w", path, err))
				continue
			}
			newClusterPool.Spec.ImageSetRef.Name = imagesetName
			newRaw, err := yaml.Marshal(newClusterPool)
			if err != nil {
				errs = append(errs, fmt.Errorf("Failed to remarshal clusterpool %s: %w", path, err))
				continue
			}
			if err := ioutil.WriteFile(path, newRaw, 0644); err != nil {
				errs = append(errs, fmt.Errorf("Failed to write updated file %s: %w", path, err))
			}
		}
	}

	if errs != nil {
		fmt.Println("The following errors occurred:")
		for _, err := range errs {
			fmt.Printf("\t%v\n", err)
		}
		os.Exit(1)
	}
}

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
