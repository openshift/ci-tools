package aws

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	awstype "github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
	corev1 "k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	httpruntime "github.com/openshift/ci-tools/cmd/cluster-init/runtime/http"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Provider struct {
	clusterInstall *clusterinstall.ClusterInstall
	awsConfig      *aws.Config
	kubeClient     ctrlruntimeclient.Client
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
		loadOpts := make([]func(*awsconfig.LoadOptions) error, 0)

		if runtime.IsIntegrationTest() {
			c := &http.Client{Transport: httpruntime.ReplayTransport(http.DefaultTransport)}
			loadOpts = append(loadOpts, awsconfig.WithHTTPClient(c))
		}

		if p.kubeClient != nil {
			awsCreds := corev1.Secret{}
			if err := p.kubeClient.Get(ctx, k8stypes.NamespacedName{Namespace: "kube-system", Name: "aws-creds"}, &awsCreds); err != nil {
				return aws.Config{}, fmt.Errorf("get secret/aws-creds: %w", err)
			}

			key, ok := awsCreds.Data["aws_access_key_id"]
			if !ok {
				return aws.Config{}, errors.New("secret/aws-creds: aws_access_key_id not found")
			}
			secret, ok := awsCreds.Data["aws_secret_access_key"]
			if !ok {
				return aws.Config{}, errors.New("secret/aws-creds: aws_secret_access_key not found")
			}

			cred := credentials.NewStaticCredentialsProvider(string(key), string(secret), "")
			loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(cred))
			loadOpts = append(loadOpts, awsconfig.WithRegion(p.clusterInstall.Infrastructure.Status.PlatformStatus.AWS.Region))
		}

		config, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return aws.Config{}, fmt.Errorf("load aws config: %w", err)
		}

		p.awsConfig = &config
	}
	return *p.awsConfig, nil
}

func NewProvider(clusterInstall *clusterinstall.ClusterInstall, kubeClient ctrlruntimeclient.Client) *Provider {
	return &Provider{
		clusterInstall: clusterInstall,
		kubeClient:     kubeClient,
	}
}
