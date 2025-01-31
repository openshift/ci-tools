package aws

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"

	authv1 "k8s.io/api/authentication/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"

	"github.com/openshift/ci-tools/cmd/cluster-init/runtime"
	httpruntime "github.com/openshift/ci-tools/cmd/cluster-init/runtime/http"
)

var (
	tokenExpirationTime    int64 = 600
	tokenAudiences               = []string{"openshift"}
	credentialsRequestNS         = "openshift-cloud-credential-operator"
	credentialsRequestName       = "cluster-init"
)

type staticTokenRetriever string

func (str staticTokenRetriever) GetIdentityToken() ([]byte, error) {
	return []byte(str), nil
}

type GetConfigOptions struct {
	Region          string
	STSClientGetter func() stscreds.AssumeRoleWithWebIdentityAPIClient
}

// ConfigFromDefaults let the aws sdk to figure out how to construct a configuration
// by leveraging the default values.
func ConfigFromDefaults() ConfigGetter {
	return newConfigGetter(false, nil, nil)
}

// ConfigFromCluster constructs a configuration by getting the credentials from the cluster.
func ConfigFromCluster(kubeClient kubernetes.Interface, ctrlClient ctrlruntimeclient.Client) ConfigGetter {
	return newConfigGetter(true, kubeClient, ctrlClient)
}

func newConfigGetter(clusterCreds bool, kubeClient kubernetes.Interface, ctrlClient ctrlruntimeclient.Client) ConfigGetter {
	return func(ctx context.Context, opts GetConfigOptions) (*aws.Config, error) {
		loadOpts := make([]func(*awsconfig.LoadOptions) error, 0)

		client := &http.Client{Transport: http.DefaultTransport}
		if runtime.IsIntegrationTest() {
			client.Transport = httpruntime.ReplayTransport(client.Transport)
			loadOpts = append(loadOpts, awsconfig.WithRetryMaxAttempts(1))
		}
		loadOpts = append(loadOpts, awsconfig.WithHTTPClient(client))

		if clusterCreds {
			credsProvider, err := credentialsProvider(ctx, opts.STSClientGetter, kubeClient, ctrlClient)
			if err != nil {
				return nil, fmt.Errorf("credentials provider: %w", err)
			}
			loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(credsProvider))
			loadOpts = append(loadOpts, awsconfig.WithRegion(opts.Region))
		}

		config, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return nil, fmt.Errorf("load aws config: %w", err)
		}

		return &config, nil
	}
}

func credentialsProvider(ctx context.Context,
	stsClientGetter func() stscreds.AssumeRoleWithWebIdentityAPIClient,
	kubeClient kubernetes.Interface,
	ctrlClient ctrlruntimeclient.Client) (aws.CredentialsProvider, error) {
	rootSecretExists := true
	awsCreds, err := kubeClient.CoreV1().Secrets("kube-system").Get(ctx, "aws-creds", v1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			rootSecretExists = false
		} else {
			return nil, fmt.Errorf("get secret/aws-creds: %w", err)
		}
	}

	if rootSecretExists {
		key, ok := awsCreds.Data["aws_access_key_id"]
		if !ok {
			return nil, errors.New("secret/aws-creds: aws_access_key_id not found")
		}
		secret, ok := awsCreds.Data["aws_secret_access_key"]
		if !ok {
			return nil, errors.New("secret/aws-creds: aws_secret_access_key not found")
		}

		return credentials.NewStaticCredentialsProvider(string(key), string(secret), ""), nil
	}

	roleARN, err := roleARN(ctx, kubeClient, ctrlClient)
	if err != nil {
		return nil, err
	}

	webIdentityToken, err := webIdentityToken(ctx, kubeClient)
	if err != nil {
		return nil, err
	}

	return stscreds.NewWebIdentityRoleProvider(stsClientGetter(), roleARN, staticTokenRetriever(webIdentityToken)), nil
}

func roleARN(ctx context.Context, kubeClient kubernetes.Interface, ctrlClient ctrlruntimeclient.Client) (string, error) {
	credReq := cloudcredentialv1.CredentialsRequest{}
	if err := ctrlClient.Get(ctx, types.NamespacedName{Namespace: credentialsRequestNS, Name: credentialsRequestName}, &credReq); err != nil {
		return "", fmt.Errorf("get credentials request %s/%s: %w", credentialsRequestNS, credentialsRequestName, err)
	}

	if !credReq.Status.Provisioned {
		return "", fmt.Errorf("credentials request %s/%s not provisioned yet", credentialsRequestNS, credentialsRequestName)
	}

	secretRef := credReq.Spec.SecretRef
	credSecret, err := kubeClient.CoreV1().Secrets(secretRef.Namespace).Get(ctx, secretRef.Name, v1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get secret %s/%s: %w", secretRef.Namespace, secretRef.Name, err)
	}

	credentials, ok := credSecret.Data["credentials"]
	if !ok {
		return "", fmt.Errorf("credentials in secret %s/%s not found", secretRef.Namespace, secretRef.Name)
	}

	for _, line := range strings.Split(string(credentials), "\n") {
		if strings.HasPrefix(line, "role_arn") {
			lineSplit := strings.Split(line, "=")
			if len(lineSplit) != 2 {
				return "", fmt.Errorf("role_arn field doesn't match the pattern: ${key} = ${value}")
			}
			return strings.Trim(lineSplit[1], " "), nil
		}
	}

	return "", errors.New("role ARN not found")
}

func webIdentityToken(ctx context.Context, kubeClient kubernetes.Interface) (string, error) {
	tokenReq, err := kubeClient.CoreV1().ServiceAccounts("ci").CreateToken(ctx, "cluster-init", &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{ExpirationSeconds: &tokenExpirationTime, Audiences: tokenAudiences},
	}, v1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create cluster-init token: %w", err)
	}

	if tokenReq != nil {
		return (*tokenReq).Status.Token, nil
	}

	return "", errors.New("token request is nil")
}
