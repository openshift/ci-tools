package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/genyaml"

	"github.com/openshift/ci-tools/pkg/api"
)

func resolver(dir string) (string, error) {
	return dir, nil
}

func main() {
	files, err := filepath.Glob("./pkg/api/*.go")
	if err != nil {
		logrus.WithError(err).Fatal("Failed to resolve filepath")
	}
	commentMap, err := genyaml.NewCommentMap(resolver, map[string][]byte{}, files...)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct commentMap")
	}
	reference, err := commentMap.GenYaml(genyaml.PopulateStruct(&api.ReleaseBuildConfiguration{}))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to generate reference yaml")
	}

	// We have to go through this hassle because its not possible to escape backticks in a backtick literal string
	reference = strings.ReplaceAll(reference, `"`, `\"`)
	referenceLines := strings.Split(reference, "\n")
	reference = "package webreg\n\nconst ciOperatorReferenceYaml = \"" + strings.Join(referenceLines, "\\n\" +\n\"") + `"`

	if err := os.WriteFile("./pkg/webreg/zz_generated.ci_operator_reference.go", []byte(reference), 0644); err != nil {
		logrus.WithError(err).Fatalf("Failed to write generated file: %v", err)
	}
}
