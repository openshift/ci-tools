package steps

import (
	"bytes"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"testing"

	apiimagev1 "github.com/openshift/api/image/v1"
	fakeimageclientset "github.com/openshift/client-go/image/clientset/versioned/fake"
	"k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

var subs = []api.PullSpecSubstitution{
	{
		PullSpec: "quay.io/openshift/origin-metering-ansible-operator:4.6",
		With:     "metering-ansible-operator",
	},
	{
		PullSpec: "quay.io/openshift/origin-metering-reporting-operator:4.6",
		With:     "metering-reporting-operator",
	},
	{
		PullSpec: "quay.io/openshift/origin-metering-presto:4.6",
		With:     "metering-presto",
	},
	{
		PullSpec: "quay.io/openshift/origin-metering-hive:4.6",
		With:     "metering-hive",
	},
	{
		PullSpec: "quay.io/openshift/origin-metering-hadoop:4.6",
		With:     "metering-hadoop",
	},
	{
		PullSpec: "quay.io/openshift/origin-ghostunnel:4.6",
		With:     "ghostunnel",
	},
}

func TestReplaceCommand(t *testing.T) {
	temp, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("Failed to create temporary directory for unit test: %v", err)
	}
	if err := exec.Command("cp", "-a", "testdata/4.6", temp).Run(); err != nil {
		t.Fatalf("Failed to copy testdata to tempdir: %v", err)
	}
	for _, sub := range subs {
		if err := exec.Command("bash", "-c", replaceCommand(temp, sub.PullSpec, "stable:"+sub.With)).Run(); err != nil {
			t.Fatalf("Failed to run replace command `bash -c \"%s\"`: %v", replaceCommand(temp, sub.PullSpec, sub.With), err)
		}
	}
	files, err := ioutil.ReadDir(filepath.Join(temp, "4.6"))
	if err != nil {
		t.Fatalf("Failed to read directory: %v", err)
	}
	for _, file := range files {
		updatedFilename := filepath.Join(temp, "4.6", file.Name())
		updated, err := ioutil.ReadFile(updatedFilename)
		if err != nil {
			t.Fatalf("Failed to read file %s: %v", updatedFilename, err)
		}
		expectedFilename := filepath.Join("testdata/4.6-expected", file.Name())
		expected, err := ioutil.ReadFile(expectedFilename)
		if err != nil {
			t.Fatalf("Failed to read file %s: %v", expectedFilename, err)
		}
		if !bytes.Equal(updated, expected) {
			t.Errorf("Updated file %s not equal to expected file %s;\nValue of updated file: %s", updatedFilename, expectedFilename, string(updated))
		}
	}
}

func TestBundleSourceDockerfile(t *testing.T) {
	var expectedDockerfile = `
FROM pipeline:src
RUN ["bash", "-c", "find manifests/deploy/4.6 -type f -exec sed -i 's?quay.io/openshift/origin-metering-ansible-operator:4.6?some-reg/target-namespace/stable:metering-ansible-operator?g' {} +"]
RUN ["bash", "-c", "find manifests/deploy/4.6 -type f -exec sed -i 's?quay.io/openshift/origin-metering-reporting-operator:4.6?some-reg/target-namespace/stable:metering-reporting-operator?g' {} +"]
RUN ["bash", "-c", "find manifests/deploy/4.6 -type f -exec sed -i 's?quay.io/openshift/origin-metering-presto:4.6?some-reg/target-namespace/stable:metering-presto?g' {} +"]
RUN ["bash", "-c", "find manifests/deploy/4.6 -type f -exec sed -i 's?quay.io/openshift/origin-metering-hive:4.6?some-reg/target-namespace/stable:metering-hive?g' {} +"]
RUN ["bash", "-c", "find manifests/deploy/4.6 -type f -exec sed -i 's?quay.io/openshift/origin-metering-hadoop:4.6?some-reg/target-namespace/stable:metering-hadoop?g' {} +"]
RUN ["bash", "-c", "find manifests/deploy/4.6 -type f -exec sed -i 's?quay.io/openshift/origin-ghostunnel:4.6?some-reg/target-namespace/stable:ghostunnel?g' {} +"]
`

	fakeClientSet := ciopTestingClient{
		imagecs: fakeimageclientset.NewSimpleClientset(&apiimagev1.ImageStream{
			ObjectMeta: v1.ObjectMeta{
				Namespace: "target-namespace",
				Name:      api.StableImageStream,
			},
			Status: apiimagev1.ImageStreamStatus{
				PublicDockerImageRepository: "some-reg/target-namespace/stable",
			},
		}),
		t: t,
	}

	s := bundleSourceStep{
		config: api.BundleSourceStepConfiguration{
			ContextDir:        "manifests/deploy",
			OperatorManifests: "4.6",
			Substitute:        subs,
		},
		jobSpec:     &api.JobSpec{},
		imageClient: fakeClientSet.ImageV1(),
	}
	s.jobSpec.SetNamespace("target-namespace")
	generatedDockerfile, err := s.bundleSourceDockerfile()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if expectedDockerfile != generatedDockerfile {
		t.Errorf("Generated bundle source dockerfile does not equal expected; generated dockerfile: %s", generatedDockerfile)
	}
}
