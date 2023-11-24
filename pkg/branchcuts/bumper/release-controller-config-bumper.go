package bumper

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	releaseControllerConfigRegexPatternFormat = `(release-ocp-%s|release-okd-%s).*\.json`
	jsonIndent                                = "  "
)

type ReleaseControllerConfigBumper struct {
	mm             *ocplifecycle.MajorMinor
	getFilesRegexp *regexp.Regexp
	jobsDir        string
}

var _ Bumper[*ReleaseConfig] = &ReleaseControllerConfigBumper{}

func NewReleaseControllerConfigBumper(ocpVer, jobsDir string) (*ReleaseControllerConfigBumper, error) {
	mm, err := ocplifecycle.ParseMajorMinor(ocpVer)
	if err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}
	mmRegexp := fmt.Sprintf("%d\\.%d", mm.Major, mm.Minor)
	getFilesRegexp := regexp.MustCompile(fmt.Sprintf(releaseControllerConfigRegexPatternFormat, mmRegexp, mmRegexp))
	return &ReleaseControllerConfigBumper{
		mm,
		getFilesRegexp,
		jobsDir,
	}, nil
}

func (b *ReleaseControllerConfigBumper) GetFiles() ([]string, error) {
	files := make([]string, 0)
	filesInfo, err := os.ReadDir(b.jobsDir)
	if err != nil {
		return files, err
	}
	for _, file := range filesInfo {
		filename := file.Name()
		if b.getFilesRegexp.Match([]byte(filename)) {
			absPath := filepath.Join(b.jobsDir, filename)
			files = append(files, absPath)
		}
	}

	return files, err
}

func (b *ReleaseControllerConfigBumper) Unmarshall(file string) (*ReleaseConfig, error) {
	raw, err := gzip.ReadFileMaybeGZIP(file)
	if err != nil {
		return nil, fmt.Errorf("failed read file %s: %w", file, err)
	}
	releaseConfig := ReleaseConfig{}
	releaseConfig.Check = make(map[string]ReleaseCheck)
	releaseConfig.Verify = make(map[string]ReleaseVerification)
	releaseConfig.Publish = make(map[string]ReleasePublish)

	if err := json.Unmarshal(raw, &releaseConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal file %s: %w", file, err)
	}
	return &releaseConfig, nil
}

func (b *ReleaseControllerConfigBumper) BumpFilename(filename string, _ *ReleaseConfig) (string, error) {
	currentRelease := fmt.Sprintf("%d.%d", b.mm.Major, b.mm.Minor)
	futureRelease := fmt.Sprintf("%d.%d", b.mm.Major, b.mm.Minor+1)
	bumpedFilename := strings.ReplaceAll(filename, currentRelease, futureRelease)
	return bumpedFilename, nil
}

/*
Candidate bumping fields:

	.message
	.mirrorPrefix
	.name
	.overrideCLIImage
	.check.*.consistentImages.parent
	.publish.*
		.verifyBugs.previousReleaseTag
			.name
			.tag
		.imageStreamRef.name
		.tagRef.name
	.verify
		.* (this represents the name of the job)
		.*.prowJob.name
*/
func (b *ReleaseControllerConfigBumper) BumpContent(releaseConfig *ReleaseConfig) (*ReleaseConfig, error) {
	if err := ReplaceWithNextVersionInPlace(&releaseConfig.Message, b.mm.Major); err != nil {
		return releaseConfig, err
	}
	if err := ReplaceWithNextVersionInPlace(&releaseConfig.MirrorPrefix, b.mm.Major); err != nil {
		return releaseConfig, err
	}
	if err := ReplaceWithNextVersionInPlace(&releaseConfig.Name, b.mm.Major); err != nil {
		return releaseConfig, err
	}
	if err := ReplaceWithNextVersionInPlace(&releaseConfig.OverrideCLIImage, b.mm.Major); err != nil {
		return releaseConfig, err
	}

	if err := bumpReleaseCheck(releaseConfig, b.mm.Major); err != nil {
		return releaseConfig, err
	}
	if err := bumpReleasePublish(releaseConfig, b.mm.Major); err != nil {
		return releaseConfig, err
	}
	if err := bumpReleaseVerification(releaseConfig, b.mm.Major); err != nil {
		return releaseConfig, err
	}

	return releaseConfig, nil
}

func bumpReleaseCheck(releaseConfig *ReleaseConfig, currentMajor int) error {
	if releaseConfig.Check == nil {
		return nil
	}
	check := make(map[string]ReleaseCheck)
	for k, v := range releaseConfig.Check {
		if v.ConsistentImages != nil {
			if err := ReplaceWithNextVersionInPlace(&v.ConsistentImages.Parent, currentMajor); err != nil {
				return err
			}
		}
		check[k] = v
	}
	releaseConfig.Check = check
	return nil
}

func bumpReleasePublish(releaseConfig *ReleaseConfig, currentMajor int) error {
	if releaseConfig.Publish == nil {
		return nil
	}
	publish := make(map[string]ReleasePublish)
	for k, v := range releaseConfig.Publish {
		if v.VerifyBugs != nil && v.VerifyBugs.PreviousReleaseTag != nil {
			if err := ReplaceWithNextVersionInPlace(&v.VerifyBugs.PreviousReleaseTag.Name, currentMajor); err != nil {
				return err
			}
			if err := ReplaceWithNextVersionInPlace(&v.VerifyBugs.PreviousReleaseTag.Tag, currentMajor); err != nil {
				return err
			}
		}
		if v.ImageStreamRef != nil {
			if err := ReplaceWithNextVersionInPlace(&v.ImageStreamRef.Name, currentMajor); err != nil {
				return err
			}
		}
		if v.TagRef != nil {
			if err := ReplaceWithNextVersionInPlace(&v.TagRef.Name, currentMajor); err != nil {
				return err
			}
		}
		publish[k] = v
	}
	releaseConfig.Publish = publish
	return nil
}

func bumpReleaseVerification(releaseConfig *ReleaseConfig, currentMajor int) error {
	if releaseConfig.Verify == nil {
		return nil
	}
	verify := make(map[string]ReleaseVerification)
	for k, v := range releaseConfig.Verify {
		if v.ProwJob != nil {
			if err := ReplaceWithNextVersionInPlace(&v.ProwJob.Name, currentMajor); err != nil {
				return err
			}
		}
		newKey, err := ReplaceWithNextVersion(k, currentMajor)
		if err != nil {
			return err
		}
		verify[newKey] = v
	}
	releaseConfig.Verify = verify
	return nil
}

func (b *ReleaseControllerConfigBumper) Marshall(releaseConfig *ReleaseConfig,
	bumpedFilename, dir string) error {
	absolutePath := path.Join(dir, bumpedFilename)
	raw, err := json.MarshalIndent(releaseConfig, "", jsonIndent)
	if err != nil {
		return fmt.Errorf("failed to marshall release config")
	}
	if err := os.WriteFile(absolutePath, raw, 0666); err != nil {
		return fmt.Errorf("failed to write file %s", absolutePath)
	}
	return nil
}
