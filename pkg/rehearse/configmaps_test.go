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

	prowplugins "k8s.io/test-infra/prow/plugins"

	"github.com/openshift/ci-tools/pkg/config"
)

func TestCreateCleanupCMTemplates(t *testing.T) {
	// TODO(nmoraitis,bbcaro): this is an integration test and should be factored better
	testRepoPath := "../../test/integration/pj-rehearse/master"
	testTemplatePath := filepath.Join(config.TemplatesPath, "subdir/test-template.yaml")
	ns := "test-namespace"
	ciTemplateSources := []config.ConfigMapSource{{
		Path: testTemplatePath,
		SHA:  "hd9sxk615lkcwx2kj226g3r3lvwkftyjif2pczm5dq3l0h13p35t",
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
	ciTemplates, err := NewRehearsalConfigMaps(ciTemplateSources, "template", configUpdaterCfg)
	if err != nil {
		t.Fatal(err)
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

	expectedListRestrictions := coretesting.ListRestrictions{
		Labels: selector,
	}

	cs := fake.NewSimpleClientset()
	cs.Fake.PrependReactor("delete-collection", "configmaps", func(action coretesting.Action) (bool, runtime.Object, error) {
		deleteAction := action.(coretesting.DeleteCollectionAction)
		listRestricitons := deleteAction.GetListRestrictions()

		if !reflect.DeepEqual(listRestricitons.Labels, expectedListRestrictions.Labels) {
			t.Fatalf("Labels:\nExpected:%#v\nFound: %#v", expectedListRestrictions.Labels, listRestricitons.Labels)
		}

		return true, nil, nil
	})
	client := cs.CoreV1().ConfigMaps(ns)
	cmManager := NewTemplateCMManager(ns, client, configUpdaterCfg, 1234, testRepoPath, logrus.NewEntry(logrus.New()))
	if err := cmManager.CreateCMs(ciTemplates); err != nil {
		t.Fatalf("CreateCMTemplates() returned error: %v", err)
	}
	cms, err := client.List(metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	expected := []v1.ConfigMap{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-template-prow-job-test-template-hd9sxk61",
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
	profileSources := []config.ConfigMapSource{{
		SHA:  "e92d4a5996a8a977bd7916b65488371331681f9d",
		Path: filepath.Join(config.ClusterProfilesPath, "profile0", "file"),
	}, {
		SHA:  "a8c99ffc996128417ef1062f9783730a8c864586",
		Path: filepath.Join(config.ClusterProfilesPath, "profile1", "file"),
	}, {
		SHA:  "8012ff51a005eaa8ed8f4c08ccdce580f462fff6",
		Path: filepath.Join(config.ClusterProfilesPath, "unchanged", "file"),
	}}
	for _, p := range profileSources {
		path := filepath.Join(dir, p.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0775); err != nil {
			t.Fatal(err)
		}
		content := []byte(p.Path + " content")
		if err := ioutil.WriteFile(path, content, 0664); err != nil {
			t.Fatal(err)
		}
	}
	profileSources = profileSources[:2]
	ns := "test"
	pr := 1234
	configUpdaterCfg := prowplugins.ConfigUpdater{
		Maps: map[string]prowplugins.ConfigMapSpec{
			filepath.Join(config.ClusterProfilesPath, "profile0", "file"): {
				Name:       "profile0",
				Namespaces: []string{ns},
			},
			filepath.Join(config.ClusterProfilesPath, "profile1", "file"): {
				Name:       "profile1",
				Namespaces: []string{ns},
			},
			filepath.Join(config.ClusterProfilesPath, "unchanged", "file"): {
				Name:       "unchanged",
				Namespaces: []string{ns},
			},
		},
	}
	configUpdaterCfg.SetDefaults()
	profiles, err := NewRehearsalConfigMaps(profileSources, "cluster-profile", configUpdaterCfg)
	if err != nil {
		t.Fatal(err)
	}

	cs := fake.NewSimpleClientset()
	client := cs.CoreV1().ConfigMaps(ns)
	m := NewTemplateCMManager(ns, client, configUpdaterCfg, pr, dir, logrus.NewEntry(logrus.New()))
	if err := m.CreateCMs(profiles); err != nil {
		t.Fatal(err)
	}
	cms, err := client.List(metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(cms.Items, func(i, j int) bool {
		return cms.Items[i].Name < cms.Items[j].Name
	})
	expected := []v1.ConfigMap{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-cluster-profile-profile0-e92d4a59",
			Namespace: ns,
			Labels: map[string]string{
				createByRehearse:  "true",
				rehearseLabelPull: strconv.Itoa(pr),
			},
		},
		Data: map[string]string{"file": "cluster/test-deploy/profile0/file content"},
	}, {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-cluster-profile-profile1-a8c99ffc",
			Namespace: ns,
			Labels: map[string]string{
				createByRehearse:  "true",
				rehearseLabelPull: strconv.Itoa(pr),
			},
		},
		Data: map[string]string{"file": "cluster/test-deploy/profile1/file content"},
	}}
	if !equality.Semantic.DeepEqual(expected, cms.Items) {
		t.Fatal(diff.ObjectDiff(expected, cms.Items))
	}
}
