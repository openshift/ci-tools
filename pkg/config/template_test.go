package config

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	templateapi "github.com/openshift/api/template/v1"
	templatescheme "github.com/openshift/client-go/template/clientset/versioned/scheme"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/fake"
	coretesting "k8s.io/client-go/testing"
)

const templatesPath = "../../test/pj-rehearse-integration/master/ci-operator/templates"

func TestGetTemplates(t *testing.T) {
	expectCiTemplates := getBaseCiTemplates(t)
	if templates, err := getTemplates(templatesPath); err != nil {
		t.Fatalf("getTemplates() returned error: %v", err)
	} else if !equality.Semantic.DeepEqual(templates, expectCiTemplates) {
		t.Fatalf("Diff found %s", diff.ObjectReflectDiff(expectCiTemplates, templates))
	}
}

func TestCreateCleanupCMTemplates(t *testing.T) {
	expectedCmNames := sets.NewString()
	ciTemplates := getBaseCiTemplates(t)

	for key, template := range ciTemplates {

		templateData, err := GetTemplateData(template)

		templateName := GetTemplateName(key)
		if err != nil {
			t.Fatalf("couldn't get data from template %s: %v", templateName, err)
		}
		expectedCmNames.Insert(GetTempCMName(templateName, key, templateData))
	}

	expectedCmLabels := map[string]string{
		createByRehearse:  "true",
		rehearseLabelPull: "1234",
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
	cs.Fake.PrependReactor("create", "configmaps", func(action coretesting.Action) (handled bool, ret runtime.Object, err error) {
		createAction := action.(coretesting.CreateAction)
		cm := createAction.GetObject().(*v1.ConfigMap)

		if !expectedCmNames.Has(cm.ObjectMeta.Name) {
			t.Fatalf("Configmap name:\nExpected one of: %v\nFound: %s", expectedCmNames, cm.ObjectMeta.Name)
		}

		if !reflect.DeepEqual(cm.ObjectMeta.Labels, expectedCmLabels) {
			t.Fatalf("Configmap labels\nExpected: %#v\nFound: %#v", expectedCmLabels, cm.ObjectMeta.Labels)
		}

		return true, nil, nil
	})
	cs.Fake.PrependReactor("delete-collection", "configmaps", func(action coretesting.Action) (handled bool, ret runtime.Object, err error) {
		deleteAction := action.(coretesting.DeleteCollectionAction)
		listRestricitons := deleteAction.GetListRestrictions()

		if !reflect.DeepEqual(listRestricitons.Labels, expectedListRestricitons.Labels) {
			t.Fatalf("Labels:\nExpected:%#v\nFound: %#v", expectedListRestricitons.Labels, listRestricitons.Labels)
		}

		return true, nil, nil
	})

	cmManager := NewTemplateCMManager(cs.CoreV1().ConfigMaps("test-namespace"), 1234, logrus.NewEntry(logrus.New()), ciTemplates)

	if err := cmManager.CreateCMTemplates(); err != nil {
		t.Fatalf("CreateCMTemplates() returned error: %v", err)
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

	var expectedTemplate *templateapi.Template
	if obj, _, err := templatescheme.Codecs.UniversalDeserializer().Decode(contents, nil, nil); err == nil {
		if template, ok := obj.(*templateapi.Template); ok {
			expectedTemplate = template
		}
	}

	return CiTemplates{
		"test-template.yaml": expectedTemplate,
	}
}

func TestGenClusterProfileCM(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	profile := ClusterProfile{
		Name:     "test-profile",
		TreeHash: "abcdef0123456789abcdef0123456789abcdef01",
	}
	profilePath := filepath.Join(dir, profile.Name)
	if err := os.Mkdir(profilePath, 0775); err != nil {
		t.Fatal(err)
	}
	files := []string{"vars.yaml", "vars-origin.yaml"}
	for _, f := range files {
		if err := ioutil.WriteFile(filepath.Join(profilePath, f), []byte(f+" content"), 0664); err != nil {
			t.Fatal(err)
		}
	}
	cm, err := genClusterProfileCM(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	name := "rehearse-cluster-profile-test-profile-abcde"
	if n := cm.ObjectMeta.Name; n != name {
		t.Errorf("unexpected name: want %q, got %q", name, n)
	}
	for _, f := range files {
		e, d := f+" content", cm.Data[f]
		if d != e {
			t.Errorf("unexpected value for key %q: want %q, got %q", f, e, d)
		}
	}
	if t.Failed() {
		t.Logf("full CM content: %s", cm.Data)
	}
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
	}
	for _, p := range profiles {
		if err := os.Mkdir(filepath.Join(dir, p.Name), 0775); err != nil {
			t.Fatal(err)
		}
	}
	pr := 1234
	cs := fake.NewSimpleClientset()
	client := cs.CoreV1().ConfigMaps("test")
	m := NewTemplateCMManager(client, pr, logrus.NewEntry(logrus.New()), CiTemplates{})
	if err := m.CreateClusterProfiles(dir, profiles); err != nil {
		t.Fatal(err)
	}
	cms, err := client.List(metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range cms.Items {
		names = append(names, p.Name)
	}
	expected := []string{
		"rehearse-cluster-profile-profile0-e92d4",
		"rehearse-cluster-profile-profile1-a8c99",
	}
	if !reflect.DeepEqual(expected, names) {
		t.Fatalf("want %s, got %s", expected, names)
	}
	for _, cm := range cms.Items {
		if cm.Labels[createByRehearse] != "true" {
			t.Fatalf("%q doesn't have label %s=true", cm.Name, createByRehearse)
		}
		if cm.Labels[rehearseLabelPull] != strconv.Itoa(pr) {
			t.Fatalf("%q doesn't have label %s=%d", cm.Name, rehearseLabelPull, pr)
		}
	}
}
