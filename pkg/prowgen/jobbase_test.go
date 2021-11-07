package prowgen

import (
	"testing"

	ciop "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestProwJobBaseBuilder(t *testing.T) {
	defaultInfo := &ProwgenInfo{
		Metadata: ciop.Metadata{
			Org:    "org",
			Repo:   "repo",
			Branch: "branch",
		},
	}
	t.Parallel()
	testCases := []struct {
		name string

		inputs         ciop.InputConfiguration
		images         []ciop.ProjectDirectoryImageBuildStepConfiguration
		binCommand     string
		testBinCommand string

		podSpecBuilder CiOperatorPodSpecGenerator
		info           *ProwgenInfo
		prefix         string
	}{
		{
			name:           "default job without further configuration",
			info:           defaultInfo,
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name:           "job with configured prefix",
			info:           defaultInfo,
			prefix:         "prefix",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name: "job with a variant",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "vorg", Repo: "vrepo", Branch: "vbranch", Variant: "variant"},
			},
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name: "job with latest release that is a candidate: has `job-release` label",
			info: defaultInfo,
			inputs: ciop.InputConfiguration{
				Releases: map[string]ciop.UnresolvedRelease{ciop.LatestReleaseName: {Candidate: &ciop.Candidate{Version: "THIS"}}},
			},
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name: "job with not a latest release that is a candidate: does not have `job-release` label",
			info: defaultInfo,
			inputs: ciop.InputConfiguration{
				Releases: map[string]ciop.UnresolvedRelease{ciop.InitialReleaseName: {Candidate: &ciop.Candidate{Version: "THIS"}}},
			},
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name: "job with latest release that is not a candidate: does not have `job-release` label",
			info: defaultInfo,
			inputs: ciop.InputConfiguration{
				Releases: map[string]ciop.UnresolvedRelease{ciop.LatestReleaseName: {Release: &ciop.Release{Version: "THIS"}}},
			},
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name:           "job with no builds outside of openshift/release@master: does not have `no-builds` label",
			info:           defaultInfo,
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name: "job with no builds in openshift/release@master: does have `no-builds` label",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "openshift", Repo: "release", Branch: "master"},
			},
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name: "job with a buildroot in of openshift/release@master: does not have `no-builds` label",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "openshift", Repo: "release", Branch: "master"},
			},
			inputs: ciop.InputConfiguration{
				BuildRootImage: &ciop.BuildRootImageConfiguration{
					ProjectImageBuild: &ciop.ProjectDirectoryImageBuildInputs{},
				},
			},
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name: "job with binary build in openshift/release@master: does not have `no-builds` label",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "openshift", Repo: "release", Branch: "master"},
			},
			binCommand:     "make",
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name: "job with test binary build in of openshift/release@master: does not have `no-builds` label",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "openshift", Repo: "release", Branch: "master"},
			},
			testBinCommand: "make test",
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name: "job with image builds in of openshift/release@master: does not have `no-builds` label",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "openshift", Repo: "release", Branch: "master"},
			},
			images:         []ciop.ProjectDirectoryImageBuildStepConfiguration{{From: "base", To: "image"}},
			prefix:         "default",
			podSpecBuilder: newFakePodSpecBuilder(),
		},
		{
			name:           "default job without further configuration, including podspec",
			info:           defaultInfo,
			prefix:         "default",
			podSpecBuilder: NewCiOperatorPodSpecGenerator(),
		},
		{
			name: "job with a variant, including podspec",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "vorg", Repo: "vrepo", Branch: "vbranch", Variant: "variant"},
			},
			prefix:         "default",
			podSpecBuilder: NewCiOperatorPodSpecGenerator(),
		},
		{
			name: "private job without cloning, including podspec",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "vorg", Repo: "vrepo", Branch: "vbranch"},
				Config:   config.Prowgen{Private: true},
			},
			prefix:         "default",
			podSpecBuilder: NewCiOperatorPodSpecGenerator(),
		},
		{
			name: "private job with cloning, including podspec",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "vorg", Repo: "vrepo", Branch: "vbranch"},
				Config:   config.Prowgen{Private: true},
			},
			prefix: "default",
			inputs: ciop.InputConfiguration{
				BuildRootImage: &ciop.BuildRootImageConfiguration{FromRepository: true},
			},
			podSpecBuilder: NewCiOperatorPodSpecGenerator(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			t.Parallel()
			ciopconfig := &ciop.ReleaseBuildConfiguration{
				InputConfiguration:      tc.inputs,
				Images:                  tc.images,
				BinaryBuildCommands:     tc.binCommand,
				TestBinaryBuildCommands: tc.testBinCommand,
				Metadata:                tc.info.Metadata,
			}
			b := NewProwJobBaseBuilder(ciopconfig, tc.info, tc.podSpecBuilder).Build(tc.prefix)
			testhelper.CompareWithFixture(t, b)
		})
	}
}

func TestGenerateJobBase(t *testing.T) {
	var testCases = []struct {
		testName              string
		name                  string
		info                  *ProwgenInfo
		canonicalGoRepository string
		rehearsable           bool
	}{
		{
			testName: "no special options",
			name:     "test",
			info:     &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
		},
		{
			testName:    "rehearsable",
			name:        "test",
			info:        &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			rehearsable: true,
		},
		{
			testName: "config variant",
			name:     "test",
			info:     &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "whatever"}},
		},
		{
			testName:              "path alias",
			name:                  "test",
			canonicalGoRepository: "/some/where",
			info:                  &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "whatever"}},
		},
		{
			testName: "hidden job for private repos",
			name:     "test",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: true},
			},
		},
		{
			testName: "expose job for private repos with public results",
			name:     "test",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: true, Expose: true},
			},
		},
		{
			testName: "expose option set but not private",
			name:     "test",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: false, Expose: true},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			jobBaseGen := NewProwJobBaseBuilder(&ciop.ReleaseBuildConfiguration{CanonicalGoRepository: &testCase.canonicalGoRepository}, testCase.info, newFakePodSpecBuilder()).Rehearsable(testCase.rehearsable).Name(testCase.name)
			testhelper.CompareWithFixture(t, jobBaseGen.Build("pull"))
		})
	}
}
