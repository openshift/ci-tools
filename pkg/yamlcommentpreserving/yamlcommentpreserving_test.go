package yamlcommentpreserving

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestReadModifyWritePreservesComments(t *testing.T) {
	originalDoc := []byte(`
# The binary build commands makes manager
binary_build_commands: make manager
tests:
- as: test-unit
  # We cargoculted this, maybe it is important
  commands: |
    unset GOFLAGS
    make test-unit
  container:
    from: src
  # This is our e2e test
- as: test-e2e
  steps:
    cluster_profile: aws
    test:
    - as: test-e2e
      cli: latest
      commands: |
        unset GOFLAGS
        make test-e2e
      from: src
      resources:
        requests:
          cpu: 100m
    workflow: ipi-aws
zz_generated_metadata:
  branch: master
  org: 3scale
  repo: 3scale-operator
`)

	// There is a bunch of weirdness still:
	// * Map ordering is totally off - Round tripping into and from yaml.Node keeps the original map order, so this is fixable
	// * The `this is our e2e test` comment is indended wrong: Likely an upstream bug,m needs to be HeadComment of the following element
	expected := `zz_generated_metadata:
    org: 3scale
    repo: 3scale-operator
    branch: master
# The binary build commands makes manager
binary_build_commands: make manager
tests:
    - as: test-unit-some-suffix
      # We cargoculted this, maybe it is important
      commands: |
        unset GOFLAGS
        make test-unit
      container:
        from: src
        # This is our e2e test
    - as: test-e2e-some-suffix
      steps:
        cluster_profile: aws
        test:
            - as: test-e2e
              from: src
              commands: |
                unset GOFLAGS
                make test-e2e
              resources:
                requests:
                    cpu: 100m
              cli: latest
        workflow: ipi-aws
`

	var config api.ReleaseBuildConfiguration
	writer, err := Unmarshal(originalDoc, &config)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	for idx, test := range config.Tests {
		config.Tests[idx].As = test.As + "-some-suffix"
	}

	raw, err := writer.Marshal(config)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if diff := cmp.Diff(expected, string(raw)); diff != "" {
		t.Errorf("expected differs from actual: %s", diff)
	}
}
