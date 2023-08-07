package rehearse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/fake"
	coretesting "k8s.io/client-go/testing"
	prowplugins "k8s.io/test-infra/prow/plugins"

	"github.com/openshift/ci-tools/pkg/config"
)

func TestCreateCleanupCMTemplates(t *testing.T) {
	// TODO(nmoraitis,bbcaro): this is an integration test and should be factored better
	testRepoPath := "../../test/integration/pj-rehearse/master"
	testTemplatePath := filepath.Join(config.TemplatesPath, "subdir/test-template.yaml")
	cluster := "cluster"
	ns := "test-namespace"
	contents, err := os.ReadFile(filepath.Join(testRepoPath, testTemplatePath))
	if err != nil {
		t.Fatal(err)
	}
	configUpdaterCfg := prowplugins.ConfigUpdater{
		Maps: map[string]prowplugins.ConfigMapSpec{
			testTemplatePath: {
				Name:     "prow-job-test-template",
				Clusters: map[string][]string{cluster: {ns}},
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
		listRestrictions := deleteAction.GetListRestrictions()

		if !reflect.DeepEqual(listRestrictions.Labels, expectedListRestricitons.Labels) {
			t.Fatalf("Labels:\nExpected:%#v\nFound: %#v", expectedListRestricitons.Labels, listRestrictions.Labels)
		}

		return true, nil, nil
	})
	client := cs.CoreV1().ConfigMaps(ns)
	pr := 1234
	SHA := "SOMESHA"
	cmManager := NewCMManager(cluster, ns, client, configUpdaterCfg, pr, testRepoPath, logrus.NewEntry(logrus.New()))
	ciTemplates, err := NewConfigMaps([]string{testTemplatePath}, "template", SHA, pr, configUpdaterCfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := cmManager.Create(ciTemplates); err != nil {
		t.Fatalf("CreateCMTemplates() returned error: %v", err)
	}
	cms, err := client.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	expected := []v1.ConfigMap{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-1234-SOMESHA-template-prow-job-test-template",
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
	if err := cmManager.Clean(); err != nil {
		t.Fatalf("Clean() returned error: %v", err)
	}
}

func TestCreateClusterProfiles(t *testing.T) {
	dir, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	profiles := []string{
		filepath.Join(config.ClusterProfilesPath, "profile0", "file"),
		filepath.Join(config.ClusterProfilesPath, "profile1", "file"),
		filepath.Join(config.ClusterProfilesPath, "unchanged", "file"),
	}
	for _, p := range profiles {
		path := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(path), 0775); err != nil {
			t.Fatal(err)
		}
		content := []byte(p + " content")
		if err := os.WriteFile(path, content, 0664); err != nil {
			t.Fatal(err)
		}
	}
	profiles = profiles[:2]
	cluster := "cluster"
	ns := "test"
	pr := 1234
	SHA := "SOMESHA"
	configUpdaterCfg := prowplugins.ConfigUpdater{
		Maps: map[string]prowplugins.ConfigMapSpec{
			filepath.Join(config.ClusterProfilesPath, "profile0", "file"): {
				Name:     "profile0",
				Clusters: map[string][]string{cluster: {ns}},
			},
			filepath.Join(config.ClusterProfilesPath, "profile1", "file"): {
				Name:     "profile1",
				Clusters: map[string][]string{cluster: {ns}},
			},
			filepath.Join(config.ClusterProfilesPath, "unchanged", "file"): {
				Name:     "unchanged",
				Clusters: map[string][]string{cluster: {ns}},
			},
		},
	}
	configUpdaterCfg.SetDefaults()
	cs := fake.NewSimpleClientset()
	client := cs.CoreV1().ConfigMaps(ns)
	m := NewCMManager(cluster, ns, client, configUpdaterCfg, pr, dir, logrus.NewEntry(logrus.New()))
	ciProfiles, err := NewConfigMaps(profiles, "cluster-profile", SHA, pr, configUpdaterCfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Create(ciProfiles); err != nil {
		t.Fatal(err)
	}
	cms, err := client.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(cms.Items, func(i, j int) bool {
		return cms.Items[i].Name < cms.Items[j].Name
	})
	expected := []v1.ConfigMap{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-1234-SOMESHA-cluster-profile-profile0",
			Namespace: ns,
			Labels: map[string]string{
				createByRehearse:  "true",
				rehearseLabelPull: strconv.Itoa(pr),
			},
		},
		Data: map[string]string{"file": "cluster/test-deploy/profile0/file content"},
	}, {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rehearse-1234-SOMESHA-cluster-profile-profile1",
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

func TestNewConfigMaps(t *testing.T) {
	cuCfg := prowplugins.ConfigUpdater{
		Maps: map[string]prowplugins.ConfigMapSpec{
			"path/to/a/template.yaml": {
				Name: "a-template-configmap",
			},
			"path/to/a/cluster-profile/*.yaml": {
				Name: "a-cluster-profile-configmap",
			},
		},
	}

	testCases := []struct {
		description string
		paths       []string

		expectCMS   ConfigMaps
		expectError error
	}{
		{
			description: "no paths",
		},
		{
			description: "paths not hitting any configured pattern",
			paths: []string{
				"path/not/covered/by/any/pattern.yaml",
			},
			expectError: fmt.Errorf("path not covered by any config-updater pattern: path/not/covered/by/any/pattern.yaml"),
		},
		{
			description: "path hitting a pattern",
			paths: []string{
				"path/to/a/template.yaml",
			},
			expectCMS: ConfigMaps{
				Paths:           sets.New[string]("path/to/a/template.yaml"),
				Names:           map[string]string{"a-template-configmap": "rehearse-1234-SOMESHA-test-a-template-configmap"},
				ProductionNames: sets.New[string]("a-template-configmap"),
				Patterns:        sets.New[string]("path/to/a/template.yaml"),
			},
		},
		{
			description: "multiple paths hitting one pattern",
			paths: []string{
				"path/to/a/cluster-profile/vars.yaml",
				"path/to/a/cluster-profile/vars-origin.yaml",
			},
			expectCMS: ConfigMaps{
				Paths:           sets.New[string]("path/to/a/cluster-profile/vars.yaml", "path/to/a/cluster-profile/vars-origin.yaml"),
				Names:           map[string]string{"a-cluster-profile-configmap": "rehearse-1234-SOMESHA-test-a-cluster-profile-configmap"},
				ProductionNames: sets.New[string]("a-cluster-profile-configmap"),
				Patterns:        sets.New[string]("path/to/a/cluster-profile/*.yaml"),
			},
		},
		{
			description: "multiple paths hitting multiple patterns",
			paths: []string{
				"path/to/a/cluster-profile/vars.yaml",
				"path/to/a/cluster-profile/vars-origin.yaml",
				"path/to/a/template.yaml",
			},
			expectCMS: ConfigMaps{
				Paths: sets.New[string](
					"path/to/a/cluster-profile/vars.yaml",
					"path/to/a/cluster-profile/vars-origin.yaml",
					"path/to/a/template.yaml",
				),
				Names: map[string]string{
					"a-cluster-profile-configmap": "rehearse-1234-SOMESHA-test-a-cluster-profile-configmap",
					"a-template-configmap":        "rehearse-1234-SOMESHA-test-a-template-configmap",
				},
				ProductionNames: sets.New[string]("a-cluster-profile-configmap", "a-template-configmap"),
				Patterns:        sets.New[string]("path/to/a/cluster-profile/*.yaml", "path/to/a/template.yaml"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(*testing.T) {
			cms, err := NewConfigMaps(tc.paths, "test", "SOMESHA", 1234, cuCfg)

			if (tc.expectError == nil) != (err == nil) {
				t.Fatalf("Did not return error as expected:\n%s", cmp.Diff(tc.expectError, err))
			} else if tc.expectError != nil && err != nil && tc.expectError.Error() != err.Error() {
				t.Fatalf("Expected different error:\n%s", cmp.Diff(tc.expectError.Error(), err.Error()))
			}

			if err == nil {
				if diffCms := cmp.Diff(tc.expectCMS, cms); diffCms != "" {
					t.Errorf("Output differs from expected:\n%s", diffCms)
				}
			}
		})
	}
}
