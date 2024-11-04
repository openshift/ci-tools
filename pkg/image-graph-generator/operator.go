package imagegraphgenerator

import (
	"fmt"
	"path/filepath"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

const (
	ReleaseAPPCIClusterPath      = "clusters/app.ci/"
	ReleaseMirrorMappingsPath    = "core-services/image-mirroring/"
	ReleaseCIOperatorConfigsPath = "ci-operator/config/"
)

type Operator struct {
	c               Client
	organizations   map[string]string
	repositories    map[string]string
	branches        map[string]string
	images          map[string]string
	buildConfigs    []buildv1.BuildConfig
	imageStreams    []imagev1.ImageStream
	releaseRepoPath string
}

func NewOperator(c Client, releaseRepoPath string) *Operator {
	return &Operator{
		c:               c,
		organizations:   make(map[string]string),
		repositories:    make(map[string]string),
		branches:        make(map[string]string),
		images:          make(map[string]string),
		releaseRepoPath: releaseRepoPath,
	}
}

func (o *Operator) Load() error {
	if err := o.loadImages(); err != nil {
		return fmt.Errorf("couldn't get all images: %w", err)
	}

	if err := o.loadOrganizations(); err != nil {
		return fmt.Errorf("couldn't get organizations: %w", err)
	}

	if err := o.loadRepositories(); err != nil {
		return fmt.Errorf("couldn't get repositories: %w", err)
	}

	if err := o.loadBranches(); err != nil {
		return fmt.Errorf("couldn't get branches: %w", err)
	}

	if err := o.loadManifests(filepath.Join(o.releaseRepoPath, ReleaseAPPCIClusterPath)); err != nil {
		return fmt.Errorf("couldn't load manifests: %w", err)
	}
	return nil
}

func (o *Operator) callback(c *api.ReleaseBuildConfiguration, i *config.Info) error {
	if i.Org == "openshift-priv" {
		return nil
	}

	if err := o.AddBranchRef(i.Org, i.Repo, i.Branch); err != nil {
		return err
	}
	branchID := o.Branches()[fmt.Sprintf("%s/%s:%s", i.Org, i.Repo, i.Branch)]

	if c.PromotionConfiguration == nil {
		return nil
	}

	configProwgen := &config.Prowgen{}
	orgProwgenConfig, err := config.LoadProwgenConfig(i.OrgPath)
	if err != nil {
		return err
	}

	repoProwgenConfig, err := config.LoadProwgenConfig(i.RepoPath)
	if err != nil {
		return err
	}

	if repoProwgenConfig != nil {
		configProwgen = repoProwgenConfig
	}

	if orgProwgenConfig != nil {
		configProwgen.MergeDefaults(orgProwgenConfig)
	}

	var errs []error
	for _, target := range api.PromotionTargets(c.PromotionConfiguration) {
		excludedImages := sets.New[string](target.ExcludedImages...)

		for _, image := range c.Images {
			if !excludedImages.Has(string(image.To)) {
				multiArch := false
				if len(image.AdditionalArchitectures) > 0 {
					multiArch = true
				}
				if err := o.UpdateImage(image, c.BaseImages, target, branchID, multiArch); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (o *Operator) OperateOnCIOperatorConfigs() error {
	if err := config.OperateOnCIOperatorConfigDir(filepath.Join(o.releaseRepoPath, ReleaseCIOperatorConfigsPath), o.callback); err != nil {
		return err
	}
	return nil
}
