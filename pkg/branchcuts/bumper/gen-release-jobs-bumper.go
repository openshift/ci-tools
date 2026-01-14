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
	"k8s.io/apimachinery/pkg/util/errors"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	cioperatorcfg "github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/prowgen"
)

const (
	ciOperatorConfigDir           = "ci-operator/config"
	releaseJobsRegexPatternFormat = `openshift-release-master__(okd|ci|nightly|okd-scos)-%s.*\.yaml`

	ocpReleaseEnvVarName = "OCP_VERSION"

	FileFinderRegexp = "regexp"
	FileFinderSignal = "signal"
)

type getFilesFn func() ([]string, error)
type GeneratedReleaseGatingJobsBumper struct {
	mm               *ocplifecycle.MajorMinor
	getFiles         getFilesFn
	newIntervalValue int
}

func makeGetFilesByRegexp(mm *ocplifecycle.MajorMinor, baseDir string) getFilesFn {
	mmRegexp := fmt.Sprintf("%d\\.%d", mm.Major, mm.Minor)
	getFilesRegexp := regexp.MustCompile(fmt.Sprintf(releaseJobsRegexPatternFormat, mmRegexp))

	return func() ([]string, error) {
		files := make([]string, 0)
		err := filepath.Walk(baseDir, func(path string, info fs.FileInfo, err error) error {
			if getFilesRegexp.Match([]byte(path)) {
				files = append(files, path)
			}
			return nil
		})
		return files, err
	}
}

func makeGetFilesProvidingSignal(currentVersionStream, baseDir string) getFilesFn {
	return func() ([]string, error) {
		var files []string
		return files, cioperatorcfg.OperateOnCIOperatorConfigDir(baseDir, func(cfg *cioperatorapi.ReleaseBuildConfiguration, info *cioperatorcfg.Info) error {
			if versionStream := prowgen.ProvidesSignalForVersion(cfg); versionStream == currentVersionStream {
				files = append(files, filepath.Join(baseDir, info.RelativePath()))
			}
			return nil
		})
	}
}

var _ Bumper[*cioperatorcfg.DataWithInfo] = &GeneratedReleaseGatingJobsBumper{}

func NewGeneratedReleaseGatingJobsBumper(ocpVer, openshiftReleaseDir string, newIntervalValue int, fileFinder string) (*GeneratedReleaseGatingJobsBumper, error) {
	mm, err := ocplifecycle.ParseMajorMinor(ocpVer)
	if err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}

	bumper := &GeneratedReleaseGatingJobsBumper{
		mm:               mm,
		newIntervalValue: newIntervalValue,
	}

	allConfigs := filepath.Join(openshiftReleaseDir, ciOperatorConfigDir)
	switch fileFinder {
	case FileFinderRegexp:
		// .../ci-operator/config/openshift/release
		openshiftReleaseConfigs := filepath.Join(allConfigs, "openshift", "release")
		bumper.getFiles = makeGetFilesByRegexp(mm, openshiftReleaseConfigs)
	case FileFinderSignal:
		bumper.getFiles = makeGetFilesProvidingSignal(ocpVer, allConfigs)
	default:
		return nil, fmt.Errorf("unknown file finder: %s", fileFinder)
	}

	return bumper, nil
}

func (b *GeneratedReleaseGatingJobsBumper) GetFiles() ([]string, error) {
	return b.getFiles()
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
	_ string,
	dataWithInfo *cioperatorcfg.DataWithInfo) (string, error) {
	newVariant, err := ReplaceWithNextVersion(dataWithInfo.Info.Metadata.Variant, b.mm.Major)
	if err != nil {
		return "", err
	}
	dataWithInfo.Info.Metadata.Variant = newVariant

	newBranch, err := ReplaceWithNextVersion(dataWithInfo.Info.Metadata.Branch, b.mm.Major)
	if err != nil {
		return "", err
	}
	dataWithInfo.Info.Metadata.Branch = newBranch

	return dataWithInfo.Info.Metadata.Basename(), nil
}

// BumpContent bumps OCP versions in the given ci-operator config
//
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
	// Bump variant in zz_generated_metadata
	if err := ReplaceWithNextVersionInPlace(&config.Metadata.Variant, major); err != nil {
		return nil, err
	}

	// Bump branch in zz_generated_metadata (e.g., release-4.21 -> release-4.22)
	if err := ReplaceWithNextVersionInPlace(&config.Metadata.Branch, major); err != nil {
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
	var errs []error

	for k, v := range config.BaseImages {
		if err := ReplaceWithNextVersionInPlace(&v.Name, major); err != nil {
			errs = append(errs, err)
			continue
		}

		// only bump tags that are plain versions (tag: 4.21) because stuff like
		// rhel-9-golang-1.24-openshift-4.22 is more tricky and will be handled
		// by other tooling
		mm, err := ocplifecycle.ParseMajorMinor(v.Tag)
		if err == nil && mm.Major == major {
			if err := ReplaceWithNextVersionInPlace(&v.Tag, major); err != nil {
				errs = append(errs, err)
				continue
			}
		}

		config.BaseImages[k] = v
	}

	return errors.NewAggregate(errs)
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

		if err := bumpStepEnvVars(test.MultiStageTestConfiguration.Environment, major); err != nil {
			return err
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

		if test.MultiStageTestConfiguration.Workflow != nil {
			if err := ReplaceWithNextVersionInPlace(test.MultiStageTestConfiguration.Workflow, major); err != nil {
				return err
			}
		}

		config.Tests[i] = test
	}

	return nil
}

func bumpStepEnvVars(env cioperatorapi.TestEnvironment, major int) error {
	var errs []error

	for k, v := range env {
		mm, err := ocplifecycle.ParseMajorMinor(v)
		// value does not look like a version => nothing to bump
		if err != nil {
			continue
		}

		if k == ocpReleaseEnvVarName || mm.Major == major {
			if err := ReplaceWithNextVersionInPlace(&v, major); err != nil {
				errs = append(errs, err)
				continue
			}
			env[k] = v
		}
	}

	return errors.NewAggregate(errs)
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
		if env.Default == nil {
			continue
		}
		// value does not look like a version => nothing to bump
		mm, err := ocplifecycle.ParseMajorMinor(*env.Default)
		if err != nil {
			continue
		}
		if env.Name == ocpReleaseEnvVarName || mm.Major == major {
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
