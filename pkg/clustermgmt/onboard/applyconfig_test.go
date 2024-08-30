package onboard

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
	"github.com/sirupsen/logrus"
)

func buildCmdFunc(t *testing.T, wantArgs []string) func(ctx context.Context, program string, args ...string) *exec.Cmd {
	return func(ctx context.Context, program string, args ...string) *exec.Cmd {
		if program != "applyconfig" {
			t.Errorf("expected program \"applyconfig\" but got %q", program)
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

func TestApplyConfig(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ci          *clustermgmt.ClusterInstall
		runCmdErr   error
		wantCmdArgs []string
		wantErr     error
	}{
		{
			name: "Run successfully",
			ci: &clustermgmt.ClusterInstall{
				ClusterName: "build99",
				InstallBase: "/install/base",
				Onboard:     clustermgmt.Onboard{ReleaseRepo: "/release/repo"},
			},
			wantCmdArgs: []string{
				"--config-dir=/release/repo/clusters/build-clusters/build99",
				"--as=",
				"--kubeconfig=/install/base/ocp-install-base/auth/kubeconfig",
				"--confirm=true",
			},
		},
		{
			name: "Run failed",
			ci: &clustermgmt.ClusterInstall{
				ClusterName: "build99",
				InstallBase: "/install/base",
				Onboard:     clustermgmt.Onboard{ReleaseRepo: "/release/repo"},
			},
			wantCmdArgs: []string{
				"--config-dir=/release/repo/clusters/build-clusters/build99",
				"--as=",
				"--kubeconfig=/install/base/ocp-install-base/auth/kubeconfig",
				"--confirm=true",
			},
			runCmdErr: errors.New("failed to apply config fake.yaml"),
			wantErr:   errors.New("applyconfig: failed to apply config fake.yaml"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			step := NewApplyConfigStep(logrus.NewEntry(logrus.StandardLogger()),
				func() (*clustermgmt.ClusterInstall, error) { return tc.ci, nil },
				buildCmdFunc(t, tc.wantCmdArgs), runCmdFunc(tc.runCmdErr),
			)
			err := step.Run(context.TODO())

			if err != nil && tc.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tc.wantErr != nil {
				t.Fatalf("want err %v but nil", tc.wantErr)
			}
			if err != nil && tc.wantErr != nil {
				if tc.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tc.wantErr.Error(), err.Error())
				}
			}
		})
	}
}
