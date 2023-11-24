package bumper

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	cioperatorcfg "github.com/openshift/ci-tools/pkg/config"
)

const (
	releaseJobsRegexPatternFormat = `openshift-release-master__(okd|ci|nightly|okd-scos)-%s.*\.yaml`
	ocpReleaseEnvVarName          = "OCP_VERSION"
)

type GeneratedReleaseGatingJobsBumper struct {
	mm               *ocplifecycle.MajorMinor
	getFilesRegexp   *regexp.Regexp
	jobsDir          string
	newIntervalValue int
}

var _ Bumper[*cioperatorcfg.DataWithInfo] = &GeneratedReleaseGatingJobsBumper{}

func NewGeneratedReleaseGatingJobsBumper(ocpVer, jobsDir string, newIntervalValue int) (*GeneratedReleaseGatingJobsBumper, error) {
	mm, err := ocplifecycle.ParseMajorMinor(ocpVer)
	if err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}
	mmRegexp := fmt.Sprintf("%d\\.%d", mm.Major, mm.Minor)
	getFilesRegexp := regexp.MustCompile(fmt.Sprintf(releaseJobsRegexPatternFormat, mmRegexp))
	return &GeneratedReleaseGatingJobsBumper{
		mm,
		getFilesRegexp,
		jobsDir,
		newIntervalValue,
	}, nil
}

func (b *GeneratedReleaseGatingJobsBumper) GetFiles() ([]string, error) {
	files := make([]string, 0)
	err := filepath.Walk(b.jobsDir, func(path string, info fs.FileInfo, err error) error {
		if b.getFilesRegexp.Match([]byte(path)) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func (b *GeneratedReleaseGatingJobsBumper) Unmarshall(file string) (*cioperatorcfg.DataWithInfo, error) {
	cfgDataByFilename, err := cioperatorcfg.LoadDataByFilename(file)
	if err != nil {
		return nil, err
	}

	filename := path.Base(file)
	dataWithInfo, ok := cfgDataByFilename[filename]
	if !ok {
		logrus.WithError(err).Errorf("failed to get config %s", file)
		return nil, fmt.Errorf("can't get data %s", filename)
	}
	return &dataWithInfo, nil
}

func (b *GeneratedReleaseGatingJobsBumper) BumpFilename(
	filename string,
	dataWithInfo *cioperatorcfg.DataWithInfo) (string, error) {
	newVariant, err := ReplaceWithNextVersion(dataWithInfo.Info.Metadata.Variant, b.mm.Major)
	if err != nil {
		return "", err
	}
	dataWithInfo.Info.Metadata.Variant = newVariant
	return dataWithInfo.Info.Metadata.Basename(), nil
}

// Candidate bumping fields:
// .base_images.*.name
// .releases.*.{release,candidate}.version
// .releases.*.prerelease.version_bounds.{lower,upper}
// .tests[].steps.test[].env[].default
func (b *GeneratedReleaseGatingJobsBumper) BumpContent(dataWithInfo *cioperatorcfg.DataWithInfo) (*cioperatorcfg.DataWithInfo, error) {
	major := b.mm.Major
	config := &dataWithInfo.Configuration
	if err := bumpBaseImages(config, major); err != nil {
		return nil, err
	}

	if err := bumpReleases(config, major); err != nil {
		return nil, err
	}

	if err := bumpTests(config, major); err != nil {
		return nil, err
	}

	if err := ReplaceWithNextVersionInPlace(&config.Metadata.Variant, major); err != nil {
		return nil, err
	}

	if config.Tests != nil {
		for i := 0; i < len(config.Tests); i++ {
			if config.Tests[i].Interval != nil {
				*config.Tests[i].Interval = strconv.Itoa(b.newIntervalValue) + "h"
			}
		}
	}

	return dataWithInfo, nil
}

func bumpBaseImages(config *cioperatorapi.ReleaseBuildConfiguration, major int) error {
	if config.BaseImages == nil {
		return nil
	}

	bumpedImages := make(map[string]cioperatorapi.ImageStreamTagReference)
	for k := range config.BaseImages {
		image := config.BaseImages[k]
		if err := ReplaceWithNextVersionInPlace(&image.Name, major); err != nil {
			return err
		}
		bumpedImages[k] = image
	}
	config.BaseImages = bumpedImages
	return nil
}

func bumpReleases(config *cioperatorapi.ReleaseBuildConfiguration, major int) error {
	if config.Releases == nil {
		return nil
	}

	bumpedReleases := make(map[string]cioperatorapi.UnresolvedRelease)
	for k := range config.Releases {
		release := config.Releases[k]
		if release.Release != nil {
			if err := ReplaceWithNextVersionInPlace(&release.Release.Version, major); err != nil {
				return err
			}
		}
		if release.Candidate != nil {
			if err := ReplaceWithNextVersionInPlace(&release.Candidate.Version, major); err != nil {
				return err
			}
		}
		if release.Prerelease != nil {
			if err := ReplaceWithNextVersionInPlace(&release.Prerelease.VersionBounds.Upper, major); err != nil {
				return err
			}
			if err := ReplaceWithNextVersionInPlace(&release.Prerelease.VersionBounds.Lower, major); err != nil {
				return err
			}
		}
		bumpedReleases[k] = release
	}
	config.Releases = bumpedReleases
	return nil
}

func bumpTests(config *cioperatorapi.ReleaseBuildConfiguration, major int) error {
	if config.Tests == nil {
		return nil
	}

	for i := 0; i < len(config.Tests); i++ {
		test := config.Tests[i]

		if test.MultiStageTestConfiguration == nil {
			continue
		}

		if err := bumpTestSteps(test.MultiStageTestConfiguration.Pre, major); err != nil {
			return err
		}
		if err := bumpTestSteps(test.MultiStageTestConfiguration.Test, major); err != nil {
			return err
		}
		if err := bumpTestSteps(test.MultiStageTestConfiguration.Post, major); err != nil {
			return err
		}

		if err := ReplaceWithNextVersionInPlace(test.MultiStageTestConfiguration.Workflow, major); err != nil {
			return err
		}

		config.Tests[i] = test
	}

	return nil
}

func bumpTestSteps(tests []cioperatorapi.TestStep, major int) error {
	if tests == nil {
		return nil
	}
	for i := 0; i < len(tests); i++ {
		multistageTest := tests[i]
		if err := bumpTestStepEnvVars(multistageTest, major); err != nil {
			return err
		}
		tests[i] = multistageTest
	}
	return nil
}

func bumpTestStepEnvVars(multistageTest cioperatorapi.TestStep, major int) error {
	if multistageTest.LiteralTestStep == nil || multistageTest.LiteralTestStep.Environment == nil {
		return nil
	}
	for i := 0; i < len(multistageTest.Environment); i++ {
		env := multistageTest.Environment[i]
		if env.Name == ocpReleaseEnvVarName {
			if err := ReplaceWithNextVersionInPlace(env.Default, major); err != nil {
				return err
			}
		}
		multistageTest.Environment[i] = env
	}
	return nil
}

func (b *GeneratedReleaseGatingJobsBumper) Marshall(dataWithInfo *cioperatorcfg.DataWithInfo, bumpedFilename, dir string) error {
	absolutePath := path.Join(dir, bumpedFilename)
	dirNoOrgRepo := strings.TrimSuffix(absolutePath, dataWithInfo.Info.Metadata.RelativePath())
	if err := dataWithInfo.CommitTo(dirNoOrgRepo); err != nil {
		logrus.WithError(err).Errorf("error saving config %s", dirNoOrgRepo)
		return fmt.Errorf("commit to %s failed: %w", dirNoOrgRepo, err)
	}
	return nil
}
