package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// CloudFormationClient is a convenience interface that has been created
// to make unit test easier to write
type CloudFormationClient interface {
	CreateStack(ctx context.Context, params *cloudformation.CreateStackInput, optFns ...func(*cloudformation.Options)) (*cloudformation.CreateStackOutput, error)
	DescribeStacks(context.Context, *cloudformation.DescribeStacksInput, ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error)
}

type EC2Client interface {
	DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeSecurityGroups(context.Context, *ec2.DescribeSecurityGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
}

type CloudFormationClientGetter interface {
	CloudFormationClient(context.Context) (CloudFormationClient, error)
}

type EC2ClientGetter interface {
	EC2Client(context.Context) (EC2Client, error)
}

type EC2ClientGetterFunc func(context.Context) (EC2Client, error)

func (f EC2ClientGetterFunc) EC2Client(ctx context.Context) (EC2Client, error) {
	return f(ctx)
}

var _ EC2Client = &FakeEC2Client{}

type FakeEC2Client struct {
	Subnets        *ec2.DescribeSubnetsOutput
	SecurityGroups *ec2.DescribeSecurityGroupsOutput
}

func (fc *FakeEC2Client) DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return fc.Subnets, nil
}

func (fc *FakeEC2Client) DescribeSecurityGroups(context.Context, *ec2.DescribeSecurityGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return fc.SecurityGroups, nil
}

type ClientGetters struct {
	cloudFormation func(context.Context) (CloudFormationClient, error)
}

func (cg *ClientGetters) CloudFormationClient(ctx context.Context) (CloudFormationClient, error) {
	return cg.cloudFormation(ctx)
}

func CloudFormationClientGetterFunc(f func(context.Context) (CloudFormationClient, error)) *ClientGetters {
	return &ClientGetters{cloudFormation: f}
}
