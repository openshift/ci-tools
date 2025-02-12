package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	awstype "github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
)

type ConfigGetter func(ctx context.Context, opts GetConfigOptions) (*aws.Config, error)

type Provider struct {
	clusterInstall *clusterinstall.ClusterInstall
	awsConfig      *aws.Config
	configGetter   ConfigGetter
}

func (p *Provider) CloudFormationClient(ctx context.Context) (awstype.CloudFormationClient, error) {
	config, err := p.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	return cloudformation.NewFromConfig(config), nil
}

func (p *Provider) EC2Client(ctx context.Context) (awstype.EC2Client, error) {
	config, err := p.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	return ec2.NewFromConfig(config), nil
}

func (p *Provider) loadConfig(ctx context.Context) (aws.Config, error) {
	if p.awsConfig == nil {
		region := ""
		if p.clusterInstall != nil && p.clusterInstall.Infrastructure.Status.PlatformStatus != nil {
			region = p.clusterInstall.Infrastructure.Status.PlatformStatus.AWS.Region
		}
		awsConfig, err := p.configGetter(ctx, GetConfigOptions{
			Region: region,
			STSClientGetter: func() stscreds.AssumeRoleWithWebIdentityAPIClient {
				return sts.New(sts.Options{Region: region})
			},
		})
		if err != nil {
			return aws.Config{}, fmt.Errorf("load aws config: %w", err)
		}
		p.awsConfig = awsConfig
	}
	return *p.awsConfig, nil
}

func NewProvider(clusterInstall *clusterinstall.ClusterInstall, configGetter ConfigGetter) *Provider {
	return &Provider{
		clusterInstall: clusterInstall,
		configGetter:   configGetter,
	}
}
