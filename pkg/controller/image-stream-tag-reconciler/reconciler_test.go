package imagestreamtagreconciler

import (
	"io/ioutil"
	"testing"

	"sigs.k8s.io/yaml"

	imagev1 "github.com/openshift/api/image/v1"
)

func TestRefForIST(t *testing.T) {
	rawImageStreamTag, err := ioutil.ReadFile("testdata/imagestreamtag.yaml")
	if err != nil {
		t.Fatalf("failed to read imagestreamtag fixture: %v", err)
	}
	ist := &imagev1.ImageStreamTag{}
	if err := yaml.Unmarshal(rawImageStreamTag, ist); err != nil {
		t.Fatalf("failed to unmarshal imagestreamTag: %v", err)
	}
	ref, err := refForIST(ist)
	if err != nil {
		t.Fatalf("failed to get ref for ist: %v", err)
	}
	if ref.org != "openshift" {
		t.Errorf("expected org to be openshift, was %q", ref.org)
	}
	if ref.repo != "cluster-openshift-apiserver-operator" {
		t.Errorf("expected repo to be images, was %q", ref.repo)
	}
	if ref.branch != "master" {
		t.Errorf("expected branch to be master, was %q", ref.branch)
	}
	if ref.commit != "96d6c74347445e0687267165a1a7d8f2c98dd3a1" {
		t.Errorf("expected commit to be 96d6c74347445e0687267165a1a7d8f2c98dd3a1, was %q", ref.commit)
	}
}
