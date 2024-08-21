package aws_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cloudformationtypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go/aws"
	smithy "github.com/aws/smithy-go"
	"github.com/openshift/ci-tools/pkg/clustermgmt"
	provisionaws "github.com/openshift/ci-tools/pkg/clustermgmt/provision/aws"
	"github.com/sirupsen/logrus"
)

type fakeCloudFormationClient struct {
	onCreateStack   func() (*cloudformation.CreateStackOutput, error)
	onDescribeStack func() (*cloudformation.DescribeStacksOutput, error)
}

func (fc *fakeCloudFormationClient) CreateStack(
	ctx context.Context,
	params *cloudformation.CreateStackInput,
	optFns ...func(*cloudformation.Options)) (*cloudformation.CreateStackOutput, error) {
	return fc.onCreateStack()
}

func (fc *fakeCloudFormationClient) DescribeStacks(
	ctx context.Context,
	input *cloudformation.DescribeStacksInput,
	optFns ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error) {
	return fc.onDescribeStack()
}

func newFakeCloudFormationClient(onCreateStack func() (*cloudformation.CreateStackOutput, error),
	onDescribeStack func() (*cloudformation.DescribeStacksOutput, error),
) *fakeCloudFormationClient {
	return &fakeCloudFormationClient{onCreateStack: onCreateStack, onDescribeStack: onDescribeStack}
}

func TestRun(t *testing.T) {
	for _, tc := range []struct {
		name            string
		ci              *clustermgmt.ClusterInstall
		onCreateStack   func() (*cloudformation.CreateStackOutput, error)
		onDescribeStack func() (*cloudformation.DescribeStacksOutput, error)
		wantErr         error
	}{
		{
			name: "Create stack successfully",
			ci: &clustermgmt.ClusterInstall{Provision: clustermgmt.Provision{
				AWS: &clustermgmt.AWSProvision{CloudFormationTemplates: []clustermgmt.AWSCloudFormationTemplate{
					{StackName: "s1"},
				}}}},
			onCreateStack: func() (*cloudformation.CreateStackOutput, error) {
				return &cloudformation.CreateStackOutput{}, nil
			},
			onDescribeStack: func() (*cloudformation.DescribeStacksOutput, error) {
				return &cloudformation.DescribeStacksOutput{Stacks: []cloudformationtypes.Stack{
					{StackName: aws.String("s1"), StackStatus: cloudformationtypes.StackStatusCreateComplete},
				}}, nil
			},
		},
		{
			name: "Fail to create stack",
			ci: &clustermgmt.ClusterInstall{Provision: clustermgmt.Provision{
				AWS: &clustermgmt.AWSProvision{CloudFormationTemplates: []clustermgmt.AWSCloudFormationTemplate{
					{StackName: "s1"},
				}}}},
			onCreateStack: func() (*cloudformation.CreateStackOutput, error) {
				return &cloudformation.CreateStackOutput{}, nil
			},
			onDescribeStack: func() (*cloudformation.DescribeStacksOutput, error) {
				return &cloudformation.DescribeStacksOutput{Stacks: []cloudformationtypes.Stack{
					{StackName: aws.String("s1"), StackStatus: cloudformationtypes.StackStatusCreateComplete},
				}}, &smithy.GenericAPIError{Message: "stack s1 failed"}
			},
			wantErr: errors.New("stack s1 failed: exceeded max wait time for StackCreateComplete waiter"),
		},
		{
			name: "A stack exists, skip",
			ci: &clustermgmt.ClusterInstall{Provision: clustermgmt.Provision{
				AWS: &clustermgmt.AWSProvision{CloudFormationTemplates: []clustermgmt.AWSCloudFormationTemplate{
					{StackName: "s1"},
				}}}},
			onCreateStack: func() (*cloudformation.CreateStackOutput, error) {
				return nil, &cloudformationtypes.AlreadyExistsException{}
			},
			onDescribeStack: func() (*cloudformation.DescribeStacksOutput, error) {
				return &cloudformation.DescribeStacksOutput{Stacks: []cloudformationtypes.Stack{
					{StackName: aws.String("s1"), StackStatus: cloudformationtypes.StackStatusCreateComplete},
				}}, nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfClientGetter := func() (provisionaws.CloudFormationClient, error) {
				return newFakeCloudFormationClient(tc.onCreateStack, tc.onDescribeStack), nil
			}
			wait := 5 * time.Millisecond
			step := provisionaws.NewCreateAWSStacksStep(logrus.NewEntry(logrus.StandardLogger()),
				func() (*clustermgmt.ClusterInstall, error) { return tc.ci, nil },
				cfClientGetter, &wait, func(path string) (string, error) { return "", nil })

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
