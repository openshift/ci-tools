package config

import (
	"io/ioutil"
	"os"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	config1Str = `
secrets:
- from:
    namespace: source-namespace-1
    name: dev-secret-1
  to:
    namespace: target-namespace-2
    name: prod-secret-1
`
	config2Str = `
secrets:
- from:
    namespace: source-namespace-1
    name: dev-secret-1
  to:
    namespace: target-namespace-2
    name: prod-secret-1
- from:
    namespace: source-namespace-3
    name: dev-secret-1
  to:
    namespace: target-namespace-4
    name: prod-secret-1
`
)

var (
	unitUnderTest someTestClass
)

type someTestClass struct {
	config Getter
}

func TestConfig(t *testing.T) {

	content := []byte(config1Str)
	configFile, err := ioutil.TempFile("", "testConfig.*.txt")
	defer func() {
		err := configFile.Close()
		if err != nil {
			t.Errorf("expected no error (configFile.Close) but got one: %v", err)
		}
		err = os.Remove(configFile.Name())
		if err != nil {
			t.Errorf("expected no error (os.Remove) but got one: %v", err)
		}
	}()

	if err != nil {
		t.Errorf("expected no error but got one: %v", err)
	}

	if _, err := configFile.Write(content); err != nil {
		t.Errorf("expected no error (configFile.Write) but got one: %v", err)
	}

	configAgent := &Agent{}
	if err := configAgent.Start(configFile.Name()); err != nil {
		t.Errorf("expected no error (configAgent.Start) but got one: %v", err)
	}

	unitUnderTest = someTestClass{config: configAgent.Config}

	expected := unitUnderTest.config()
	result := &Configuration{
		Secrets: []MirrorConfig{
			{
				From: SecretLocation{Namespace: "source-namespace-1", Name: "dev-secret-1"},
				To:   SecretLocationWithCluster{SecretLocation: SecretLocation{Namespace: "target-namespace-2", Name: "prod-secret-1"}},
			},
		},
	}

	if !reflect.DeepEqual(expected, result) {
		t.Errorf("Unexpected mis-match: %s", diff.ObjectReflectDiff(expected, result))
	}

	content = []byte(config2Str)
	if _, err := configFile.Write(content); err != nil {
		t.Errorf("expected no error (configFile.Write) but got one: %v", err)
	}

	result = &Configuration{
		Secrets: []MirrorConfig{
			{
				From: SecretLocation{Namespace: "source-namespace-1", Name: "dev-secret-1"},
				To:   SecretLocationWithCluster{SecretLocation: SecretLocation{Namespace: "target-namespace-2", Name: "prod-secret-1"}},
			},
			{
				From: SecretLocation{Namespace: "source-namespace-3", Name: "dev-secret-1"},
				To:   SecretLocationWithCluster{SecretLocation: SecretLocation{Namespace: "target-namespace-4", Name: "prod-secret-1"}},
			},
		},
	}

	err = wait.Poll(1*time.Second, 10*time.Second,
		func() (bool, error) {
			expected = unitUnderTest.config()
			if !reflect.DeepEqual(expected, result) {
				return false, nil
			}
			return true, nil
		})
	if err != nil {
		t.Errorf("expected no error (wait.Poll) but got one: %v", err)
	}
}
