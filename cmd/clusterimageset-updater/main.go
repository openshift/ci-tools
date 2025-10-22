package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/kubernetes/scheme"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release/prerelease"
)

const (
	versionLowerLabel  = "version_lower"
	versionUpperLabel  = "version_upper"
	versionStreamLabel = "version_stream"
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

func addSchemes() error {
	if err := hivev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add hivev1 to scheme: %w", err)
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

	if err := addSchemes(); err != nil {
		logrus.WithError(err).Fatal("Failed to set up scheme")
	}

	s := json.NewSerializerWithOptions(json.DefaultMetaFactory, scheme.Scheme,
		scheme.Scheme, json.SerializerOptions{Yaml: true, Pretty: false, Strict: false})

	if err := filepath.WalkDir(o.poolDir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), "_clusterpool.yaml") {
			return nil
		}
		return ensureLabelsOnClusterPool(s, path, path)
	}); err != nil {
		logrus.WithError(err).Fatal("Failed to ensure labels on cluster pools")
	}

	// key: version_in; value: list of file paths
	poolFilesByBounds := make(map[api.VersionBounds][]string)
	if err := filepath.WalkDir(o.poolDir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), "_clusterpool.yaml") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		pool := hivev1.ClusterPool{}
		if err := yaml.Unmarshal(raw, &pool); err != nil {
			return err
		}
		bounds, err := labelsToBounds(pool.Labels)
		if err != nil {
			return fmt.Errorf("pool %s: %w", pool.Name, err)
		}
		if bounds != nil {
			poolFilesByBounds[*bounds] = append(poolFilesByBounds[*bounds], path)
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Failed to get list of clusterpools setting version bounds")
	}

	boundsToPullspec := make(map[api.VersionBounds]string)
	for versionBounds := range poolFilesByBounds {
		release := api.Prerelease{
			ReleaseDescriptor: api.ReleaseDescriptor{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitectureAMD64,
			},
			VersionBounds: versionBounds,
		}
		pullSpec, err := prerelease.ResolvePullSpec(&http.Client{}, release)
		if err != nil {
			logrus.WithError(err).Fatalf("Failed to get pullspec for version range `%s`", versionBounds.Query())
		}
		boundsToPullspec[versionBounds] = pullSpec
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
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		imageset := hivev1.ClusterImageSet{}
		if err := yaml.Unmarshal(raw, &imageset); err != nil {
			return err
		}
		bounds, err := labelsToBounds(imageset.Annotations)
		if err != nil {
			return fmt.Errorf("failed to parse version labels for clusterimageset %s: %w", imageset.Name, err)
		}
		if bounds != nil {
			isCurrent := false
			for poolBounds := range poolFilesByBounds {
				if poolBounds == *bounds {
					isCurrent = imageset.Spec.ReleaseImage == boundsToPullspec[poolBounds]
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
		logrus.WithError(err).Fatal("Failed to get list of clusterpools setting version bounds")
	}

	// make as much progress as possible and print list of errors at end of command
	var errs []error

	// any remaining items in autopools/versionToPullspec need to be updated
	for bounds, pullspec := range boundsToPullspec {
		name := nameFromPullspec(pullspec, bounds)
		clusterimageset := hivev1.ClusterImageSet{
			TypeMeta: v1.TypeMeta{
				Kind:       "ClusterImageSet",
				APIVersion: "hive.openshift.io/v1",
			},
			ObjectMeta: v1.ObjectMeta{
				Name: name,
				Annotations: map[string]string{
					versionLowerLabel: bounds.Lower,
					versionUpperLabel: bounds.Upper,
				},
			},
			Spec: hivev1.ClusterImageSetSpec{
				ReleaseImage: pullspec,
			},
		}
		if bounds.Stream != "" {
			clusterimageset.Annotations[versionStreamLabel] = bounds.Stream
		}
		raw, err := yaml.Marshal(clusterimageset)
		if err != nil {
			errs = append(errs, fmt.Errorf("could not marshal yaml for clusterimageset %s: %w", name, err))
			continue
		}
		if err := os.WriteFile(filepath.Join(o.outputDir, fmt.Sprintf("%s_clusterimageset.yaml", name)), raw, 0644); err != nil {
			errs = append(errs, fmt.Errorf("failed to write file for clusterimageset %s: %w", name, err))
		}
	}

	// delete old clusterimagesets
	for _, path := range toDelete {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("failed to delete file %s: %w", path, err))
		}
	}

	// update all clusterpool specs
	for bounds, files := range poolFilesByBounds {
		imagesetName := nameFromPullspec(boundsToPullspec[bounds], bounds)
		for _, path := range files {
			raw, err := os.ReadFile(path)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to read file %s: %w", path, err))
				continue
			}
			var newClusterPool hivev1.ClusterPool
			if err := yaml.Unmarshal(raw, &newClusterPool); err != nil {
				errs = append(errs, fmt.Errorf("failed to unmarshal clusterpool %s: %w", path, err))
				continue
			}
			newClusterPool.Spec.ImageSetRef.Name = imagesetName
			newRaw, err := yaml.Marshal(newClusterPool)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to remarshal clusterpool %s: %w", path, err))
				continue
			}
			if err := os.WriteFile(path, newRaw, 0644); err != nil {
				errs = append(errs, fmt.Errorf("failed to write updated file %s: %w", path, err))
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

func ensureLabelsOnClusterPool(s *json.Serializer, input, output string) error {
	raw, err := os.ReadFile(input)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", input, err)
	}
	pool := hivev1.ClusterPool{}
	_, _, err = s.Decode(raw, nil, &pool)
	if err != nil {
		return fmt.Errorf("failed to decode from %s: %w", input, err)
	}
	if newPool, modified := ensureLabels(pool); modified {
		logrus.WithField("namespace", newPool.Namespace).WithField("name", newPool.Name).
			Info("Adding labels to the cluster pool")
		buf := new(bytes.Buffer)
		if err := s.Encode(&newPool, buf); err != nil {
			return fmt.Errorf("failed to encode the new pool from %s: %w", input, err)
		}
		if err := os.WriteFile(output, buf.Bytes(), 0644); err != nil {
			return fmt.Errorf("failed to write updated file %s: %w", output, err)
		}
	}
	return nil
}

func ensureLabels(pool hivev1.ClusterPool) (hivev1.ClusterPool, bool) {
	var modified bool
	if value, ok := pool.Labels["owner"]; ok {
		if pool.Spec.Labels == nil {
			pool.Spec.Labels = map[string]string{}
		}
		if pool.Spec.Labels["tp.openshift.io/owner"] != value {
			modified = true
			pool.Spec.Labels["tp.openshift.io/owner"] = value
		}
	} else {
		logrus.WithField("namespace", pool.Namespace).WithField("name", pool.Name).
			Warn("The cluster pool has no owner label")
	}
	return pool, modified
}

func nameFromPullspec(pullspec string, bounds api.VersionBounds) string {
	baseName := pullspec[strings.LastIndex(pullspec, "ocp-release"):]
	// handle names like ocp-release:4.8.3-x86_64, generated by a version_in like ">4.8.0-0 <4.9.0-0"
	baseName = strings.ReplaceAll(baseName, ":", "-")
	// handle names like ocp-release@sha256:..., generated by a version_in like ">4.8.0 <4.9.0"
	baseName = strings.ReplaceAll(baseName, "@", "-")
	// replace the `_` in `x86_64`, as `_` is an invalid character for k8s object names
	baseName = strings.ReplaceAll(baseName, "_", "-")
	return fmt.Sprintf("%s-for-%s-to-%s", baseName, bounds.Lower, bounds.Upper)
}

func labelsToBounds(labels map[string]string) (*api.VersionBounds, error) {
	if labels == nil {
		return nil, nil
	}
	if labels[versionLowerLabel] != "" || labels[versionUpperLabel] != "" {
		bounds := api.VersionBounds{Upper: labels[versionUpperLabel], Lower: labels[versionLowerLabel], Stream: labels[versionStreamLabel]}
		if bounds.Lower == "" {
			return nil, fmt.Errorf("if `version_upper` is set, `version_lower` must also be set")
		}
		if bounds.Upper == "" {
			return nil, fmt.Errorf("if `version_lower` is set, `version_upper` must also be set")
		}
		return &bounds, nil
	}
	return nil, nil
}
