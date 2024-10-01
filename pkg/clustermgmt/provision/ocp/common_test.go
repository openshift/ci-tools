package ocp

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
)

type testParams struct {
	name        string
	ci          *clusterinstall.ClusterInstall
	runCmdErr   error
	wantCmdArgs []string
	wantErr     error
}

func buildCmdFunc(t *testing.T, wantArgs []string) func(ctx context.Context, program string, args ...string) *exec.Cmd {
	return func(ctx context.Context, program string, args ...string) *exec.Cmd {
		if program != "openshift-install" {
			t.Errorf("expected program \"openshift-install\" but got %q", program)
		}
		if diff := cmp.Diff(wantArgs, args); diff != "" {
			t.Errorf("program args don't match %s", diff)
		}
		return &exec.Cmd{}
	}
}

func runCmdFunc(e error) func(*exec.Cmd) error {
	return func(c *exec.Cmd) error { return e }
}

func TestRun(t *testing.T) {
	for _, tc := range []struct {
		params  testParams
		newStep func(t *testing.T, tc testParams) clustermgmt.Step
	}{
		{
			params: testParams{
				name:        "Create install-config: success",
				ci:          &clusterinstall.ClusterInstall{InstallBase: "/cluster-base"},
				wantCmdArgs: []string{"create", "install-config", "--log-level=debug", "--dir=/cluster-base/ocp-install-base"},
			},
			newStep: func(t *testing.T, tc testParams) clustermgmt.Step {
				return NewCreateInstallConfigStep(logrus.NewEntry(logrus.StandardLogger()),
					func() (*clusterinstall.ClusterInstall, error) { return tc.ci, nil },
					buildCmdFunc(t, tc.wantCmdArgs),
					runCmdFunc(tc.runCmdErr))
			},
		},
		{
			params: testParams{
				name:        "Create install-config: failure",
				ci:          &clusterinstall.ClusterInstall{InstallBase: "/cluster-base"},
				runCmdErr:   errors.New("fail"),
				wantCmdArgs: []string{"create", "install-config", "--log-level=debug", "--dir=/cluster-base/ocp-install-base"},
				wantErr:     errors.New("create install-config: fail"),
			},
			newStep: func(t *testing.T, tc testParams) clustermgmt.Step {
				return NewCreateInstallConfigStep(logrus.NewEntry(logrus.StandardLogger()),
					func() (*clusterinstall.ClusterInstall, error) { return tc.ci, nil },
					buildCmdFunc(t, tc.wantCmdArgs),
					runCmdFunc(tc.runCmdErr))
			},
		},
		{
			params: testParams{
				name:        "Create manifests: success",
				ci:          &clusterinstall.ClusterInstall{InstallBase: "/cluster-base"},
				wantCmdArgs: []string{"create", "manifests", "--log-level=debug", "--dir=/cluster-base/ocp-install-base"},
			},
			newStep: func(t *testing.T, tc testParams) clustermgmt.Step {
				return NewCreateManifestsStep(logrus.NewEntry(logrus.StandardLogger()),
					func() (*clusterinstall.ClusterInstall, error) { return tc.ci, nil },
					buildCmdFunc(t, tc.wantCmdArgs),
					runCmdFunc(tc.runCmdErr))
			},
		},
		{
			params: testParams{
				name:        "Create manifests: failure",
				ci:          &clusterinstall.ClusterInstall{InstallBase: "/cluster-base"},
				runCmdErr:   errors.New("fail"),
				wantCmdArgs: []string{"create", "manifests", "--log-level=debug", "--dir=/cluster-base/ocp-install-base"},
				wantErr:     errors.New("create manifests: fail"),
			},
			newStep: func(t *testing.T, tc testParams) clustermgmt.Step {
				return NewCreateManifestsStep(logrus.NewEntry(logrus.StandardLogger()),
					func() (*clusterinstall.ClusterInstall, error) { return tc.ci, nil },
					buildCmdFunc(t, tc.wantCmdArgs),
					runCmdFunc(tc.runCmdErr))
			},
		},
		{
			params: testParams{
				name:        "Create cluster: success",
				ci:          &clusterinstall.ClusterInstall{InstallBase: "/cluster-base"},
				wantCmdArgs: []string{"create", "cluster", "--log-level=debug", "--dir=/cluster-base/ocp-install-base"},
			},
			newStep: func(t *testing.T, tc testParams) clustermgmt.Step {
				return NewCreateClusterStep(logrus.NewEntry(logrus.StandardLogger()),
					func() (*clusterinstall.ClusterInstall, error) { return tc.ci, nil },
					buildCmdFunc(t, tc.wantCmdArgs),
					runCmdFunc(tc.runCmdErr))
			},
		},
		{
			params: testParams{
				name:        "Create cluster: failure",
				ci:          &clusterinstall.ClusterInstall{InstallBase: "/cluster-base"},
				runCmdErr:   errors.New("fail"),
				wantCmdArgs: []string{"create", "cluster", "--log-level=debug", "--dir=/cluster-base/ocp-install-base"},
				wantErr:     errors.New("create cluster: fail"),
			},
			newStep: func(t *testing.T, tc testParams) clustermgmt.Step {
				return NewCreateClusterStep(logrus.NewEntry(logrus.StandardLogger()),
					func() (*clusterinstall.ClusterInstall, error) { return tc.ci, nil },
					buildCmdFunc(t, tc.wantCmdArgs),
					runCmdFunc(tc.runCmdErr))
			},
		},
	} {
		t.Run(tc.params.name, func(t *testing.T) {
			t.Parallel()

			step := tc.newStep(t, tc.params)
			err := step.Run(context.TODO())

			if err != nil && tc.params.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tc.params.wantErr != nil {
				t.Fatalf("want err %v but nil", tc.params.wantErr)
			}
			if err != nil && tc.params.wantErr != nil {
				if tc.params.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tc.params.wantErr.Error(), err.Error())
				}
			}
		})
	}
}
