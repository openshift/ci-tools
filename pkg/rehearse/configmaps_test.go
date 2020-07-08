package rehearse

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

	prowgithub "k8s.io/test-infra/prow/github"
	prowplugins "k8s.io/test-infra/prow/plugins"

	"github.com/openshift/ci-tools/pkg/config"
)

func TestValidateConfigMaps(t *testing.T) {
	config := prowplugins.ConfigUpdater{
		Maps: map[string]prowplugins.ConfigMapSpec{
			"templates/dir/file":           {Namespace: "ns", Name: "cm0"},
			"cluster/test-deploy/dir/file": {Namespace: "ns", Name: "cm1"},
		},
	}
	changes := []prowgithub.PullRequestChange{
		{Filename: "templates/dir/file", SHA: "00000000"},
		{Filename: "cluster/test-deploy/dir/file", SHA: "11111111"},
	}
	client := fake.NewSimpleClientset().CoreV1().ConfigMaps("ns")
	config.SetDefaults()
	manager := NewTemplateCMManager("ns", client, config, 0, "/", logrus.NewEntry(logrus.New()))
	if err := manager.validateChanges(changes); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	err := manager.validateChanges(append(changes, prowgithub.PullRequestChange{
		Filename: "404", SHA: "11111111",
	}))
	if err == nil {
		t.Fatal("unexpected success")
	}
	expected := "no entry in `updateconfig` matches \"404\""
	if err.Error() != expected {
		t.Errorf("unexpected error: %v", diff.ObjectDiff(expected, err.Error()))
	}
}

func TestCreateCleanupCMTemplates(t *testing.T) {
	// TODO(nmoraitis,bbcaro): this is an integration test and should be factored better
	testRepoPath := "../../test/integration/pj-rehearse/master"
	testTemplatePath := filepath.Join(config.TemplatesPath, "subdir/test-template.yaml")
	ns := "test-namespace"
	ciTemplates := []config.ConfigMapSource{{
		Filename: testTemplatePath,
		SHA:      "hd9sxk615lkcwx2kj226g3r3lvwkftyjif2pczm5dq3l0h13p35t",
	}}
	contents, err := ioutil.ReadFile(filepath.Join(testRepoPath, testTemplatePath))
	if err != nil {
		t.Fatal(err)
	}
	configUpdaterCfg := prowplugins.ConfigUpdater{
		Maps: map[string]prowplugins.ConfigMapSpec{
			testTemplatePath: {
				Name:       "prow-job-test-template",
				Namespaces: []string{ns},
			},
		},
	}
	configUpdaterCfg.SetDefaults()
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
	cmManager := NewTemplateCMManager(ns, client, configUpdaterCfg, 1234, testRepoPath, logrus.NewEntry(logrus.New()))
	if err := cmManager.CreateCMTemplates(ciTemplates); err != nil {
		t.Fatalf("CreateCMTemplates() returned error: %v", err)
	}
	cms, err := client.List(metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	expected := []v1.ConfigMap{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-template-test-template-hd9sxk61",
			Namespace: ns,
			Labels: map[string]string{
				createByRehearse:  "true",
				rehearseLabelPull: "1234",
			},
		},
		Data: map[string]string{
			"test-template.yaml": string(contents),
		},
	}}
	if !equality.Semantic.DeepEqual(expected, cms.Items) {
		t.Fatal(diff.ObjectDiff(expected, cms.Items))
	}
	if err := cmManager.CleanupCMTemplates(); err != nil {
		t.Fatalf("CleanupCMTemplates() returned error: %v", err)
	}
}

func TestCreateClusterProfiles(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	profiles := []config.ConfigMapSource{{
		SHA:      "e92d4a5996a8a977bd7916b65488371331681f9d",
		Filename: filepath.Join(config.ClusterProfilesPath, "profile0"),
	}, {
		SHA:      "a8c99ffc996128417ef1062f9783730a8c864586",
		Filename: filepath.Join(config.ClusterProfilesPath, "profile1"),
	}, {
		SHA:      "8012ff51a005eaa8ed8f4c08ccdce580f462fff6",
		Filename: filepath.Join(config.ClusterProfilesPath, "unchanged"),
	}}
	for _, p := range profiles {
		path := filepath.Join(dir, p.Filename)
		if err := os.MkdirAll(path, 0775); err != nil {
			t.Fatal(err)
		}
		content := []byte(filepath.Base(p.Filename) + " content")
		if err := ioutil.WriteFile(filepath.Join(path, "file"), content, 0664); err != nil {
			t.Fatal(err)
		}
	}
	profiles = profiles[:2]
	ns := "test"
	pr := 1234
	configUpdaterCfg := prowplugins.ConfigUpdater{
		Maps: map[string]prowplugins.ConfigMapSpec{
			filepath.Join(config.ClusterProfilesPath, "profile0", "file"): {
				Name:       ClusterProfilePrefix + "profile0",
				Namespaces: []string{ns},
			},
			filepath.Join(config.ClusterProfilesPath, "profile1", "file"): {
				Name:       ClusterProfilePrefix + "profile1",
				Namespaces: []string{ns},
			},
			filepath.Join(config.ClusterProfilesPath, "unchanged", "file"): {
				Name:       ClusterProfilePrefix + "unchanged",
				Namespaces: []string{ns},
			},
		},
	}
	configUpdaterCfg.SetDefaults()
	cs := fake.NewSimpleClientset()
	client := cs.CoreV1().ConfigMaps(ns)
	m := NewTemplateCMManager(ns, client, configUpdaterCfg, pr, dir, logrus.NewEntry(logrus.New()))
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
			Name:      "rehearse-cluster-profile-profile0-e92d4a59",
			Namespace: ns,
			Labels: map[string]string{
				createByRehearse:  "true",
				rehearseLabelPull: strconv.Itoa(pr),
			},
		},
		Data: map[string]string{"file": "profile0 content"},
	}, {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-cluster-profile-profile1-a8c99ffc",
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
