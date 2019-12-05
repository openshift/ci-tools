package main

import (
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"k8s.io/test-infra/boskos/client"
	"os"
	"testing"
)

func TestClientCode(t *testing.T) {
	file, err := ioutil.TempFile("", "test")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil {
			t.Fatalf("Failed to remove temp file: %v", err)
		}
	}()
	if err := ioutil.WriteFile(file.Name(), []byte("secret"), 0755); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	c, err := client.NewClient("test", "https://boskos-ci.svc.ci.openshift.org",
		"ci", file.Name())
	if err != nil {
		t.Fatalf("failed to create Boskos client: %v", err)
	}
	logrus.WithField("c.HasResource()", c.HasResource()).Info("tested")
}
