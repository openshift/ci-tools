package config

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/kubernetes/fake"
	coretesting "k8s.io/client-go/testing"

	prowplugins "k8s.io/test-infra/prow/plugins"
)

const testRepoPath = "../../test/pj-rehearse-integration/master"

var templatesPath = filepath.Join(testRepoPath, "/ci-operator/templates")

func TestGetTemplates(t *testing.T) {
	expectCiTemplates := getBaseCiTemplates(t)
	if templates, err := getTemplates(templatesPath); err != nil {
		t.Fatalf("getTemplates() returned error: %v", err)
	} else if !equality.Semantic.DeepEqual(templates, expectCiTemplates) {
		t.Fatalf("Diff found %s", diff.ObjectReflectDiff(expectCiTemplates, templates))
	}
}

func TestCreateCleanupCMTemplates(t *testing.T) {
	ns := "test-namespace"
	ciTemplates := getBaseCiTemplates(t)
	configUpdaterCfg := prowplugins.ConfigUpdater{
		Maps: map[string]prowplugins.ConfigMapSpec{
			"ci-operator/templates/test-template.yaml": {
				Name:       "cluster-launch-test-template",
				Namespaces: []string{ns},
			},
		},
	}
	createByRehearseReq, err := labels.NewRequirement(createByRehearse, selection.Equals, []string{"true"})
	if err != nil {
		t.Fatal(err)
	}

	rehearseLabelPullReq, err := labels.NewRequirement(rehearseLabelPull, selection.Equals, []string{"1234"})
	if err != nil {
		t.Fatal(err)
	}

	selector := labels.NewSelector().Add(*createByRehearseReq).Add(*rehearseLabelPullReq)

	expectedListRestricitons := coretesting.ListRestrictions{
		Labels: selector,
	}

	cs := fake.NewSimpleClientset()
	cs.Fake.PrependReactor("delete-collection", "configmaps", func(action coretesting.Action) (bool, runtime.Object, error) {
		deleteAction := action.(coretesting.DeleteCollectionAction)
		listRestricitons := deleteAction.GetListRestrictions()

		if !reflect.DeepEqual(listRestricitons.Labels, expectedListRestricitons.Labels) {
			t.Fatalf("Labels:\nExpected:%#v\nFound: %#v", expectedListRestricitons.Labels, listRestricitons.Labels)
		}

		return true, nil, nil
	})
	client := cs.CoreV1().ConfigMaps(ns)
	cmManager := NewTemplateCMManager(client, configUpdaterCfg, 1234, testRepoPath, logrus.NewEntry(logrus.New()))
	if err := cmManager.CreateCMTemplates(ciTemplates); err != nil {
		t.Fatalf("CreateCMTemplates() returned error: %v", err)
	}
	cms, err := client.List(metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	expected := []v1.ConfigMap{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-hd9sxk61-test-template",
			Namespace: ns,
			Labels: map[string]string{
				createByRehearse:  "true",
				rehearseLabelPull: "1234",
			},
		},
		Data: map[string]string{
			"test-template.yaml": string(ciTemplates["test-template.yaml"]),
		},
	}}
	if !equality.Semantic.DeepEqual(expected, cms.Items) {
		t.Fatal(diff.ObjectDiff(expected, cms.Items))
	}
	if err := cmManager.CleanupCMTemplates(); err != nil {
		t.Fatalf("CleanupCMTemplates() returned error: %v", err)
	}
}

func getBaseCiTemplates(t *testing.T) CiTemplates {
	testTemplatePath := filepath.Join(templatesPath, "test-template.yaml")
	contents, err := ioutil.ReadFile(testTemplatePath)
	if err != nil {
		t.Fatalf("could not read file %s for template: %v", testTemplatePath, err)
	}
	return CiTemplates{"test-template.yaml": contents}
}

func TestCreateClusterProfiles(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	profiles := []ClusterProfile{
		{Name: "profile0", TreeHash: "e92d4a5996a8a977bd7916b65488371331681f9d"},
		{Name: "profile1", TreeHash: "a8c99ffc996128417ef1062f9783730a8c864586"},
		{Name: "unchanged", TreeHash: "8012ff51a005eaa8ed8f4c08ccdce580f462fff6"},
	}
	for _, p := range profiles {
		path := filepath.Join(dir, ClusterProfilesPath, p.Name)
		if err := os.MkdirAll(path, 0775); err != nil {
			t.Fatal(err)
		}
		if err := ioutil.WriteFile(filepath.Join(path, "file"), []byte(p.Name+" content"), 0664); err != nil {
			t.Fatal(err)
		}
	}
	profiles = profiles[:2]
	ns := "test"
	pr := 1234
	configUpdaterCfg := prowplugins.ConfigUpdater{
		Maps: map[string]prowplugins.ConfigMapSpec{
			filepath.Join(ClusterProfilesPath, "profile0", "file"): {
				Name:       "cluster-profile-profile0",
				Namespaces: []string{ns},
			},
			filepath.Join(ClusterProfilesPath, "profile1", "file"): {
				Name:       "cluster-profile-profile1",
				Namespaces: []string{ns},
			},
			filepath.Join(ClusterProfilesPath, "unchanged", "file"): {
				Name:       "cluster-profile-unchanged",
				Namespaces: []string{ns},
			},
		},
	}
	cs := fake.NewSimpleClientset()
	client := cs.CoreV1().ConfigMaps(ns)
	m := NewTemplateCMManager(client, configUpdaterCfg, pr, dir, logrus.NewEntry(logrus.New()))
	if err := m.CreateClusterProfiles(profiles); err != nil {
		t.Fatal(err)
	}
	cms, err := client.List(metav1.ListOptions{})
	sort.Slice(cms.Items, func(i, j int) bool {
		return cms.Items[i].Name < cms.Items[j].Name
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := []v1.ConfigMap{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-cluster-profile-profile0-e92d4",
			Namespace: ns,
			Labels: map[string]string{
				createByRehearse:  "true",
				rehearseLabelPull: strconv.Itoa(pr),
			},
		},
		Data: map[string]string{"file": "profile0 content"},
	}, {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-cluster-profile-profile1-a8c99",
			Namespace: ns,
			Labels: map[string]string{
				createByRehearse:  "true",
				rehearseLabelPull: strconv.Itoa(pr),
			},
		},
		Data: map[string]string{"file": "profile1 content"},
	}}
	if !equality.Semantic.DeepEqual(expected, cms.Items) {
		t.Fatal(diff.ObjectDiff(expected, cms.Items))
	}
}
