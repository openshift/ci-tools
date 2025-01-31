package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/google/go-cmp/cmp"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
)

type FakeAssumeRoleWithWebIdentityAPIClient struct {
	Output *sts.AssumeRoleWithWebIdentityOutput
	Err    error
}

func (f *FakeAssumeRoleWithWebIdentityAPIClient) AssumeRoleWithWebIdentity(ctx context.Context, params *sts.AssumeRoleWithWebIdentityInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	f.Output.Credentials.SessionToken = aws.String(*params.RoleArn + "__" + *params.WebIdentityToken)
	return f.Output, f.Err
}

func TestRootCredentials(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		kubeClientObjects   []runtime.Object
		ctrlClientObjects   []ctrlclient.Object
		region              string
		wantConfig          *aws.Config
		wantAccessKeyId     string
		wantSecretAccessKey string
	}{
		{
			name: "Root secret exists",
			kubeClientObjects: []runtime.Object{&corev1.Secret{
				ObjectMeta: v1.ObjectMeta{Namespace: "kube-system", Name: "aws-creds"},
				Data: map[string][]byte{
					"aws_access_key_id":     []byte("foo"),
					"aws_secret_access_key": []byte("bar"),
				},
			}},
			wantAccessKeyId:     "foo",
			wantSecretAccessKey: "bar",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			fakeKubeClient := fake.NewClientset(testCase.kubeClientObjects...)
			fakeCtrlClient := ctrlfake.NewClientBuilder().
				WithObjects(testCase.ctrlClientObjects...).
				Build()

			configGetter := ConfigFromCluster(fakeKubeClient, fakeCtrlClient)
			gotConfig, err := configGetter(context.TODO(), GetConfigOptions{
				Region: testCase.region,
			})
			if err != nil {
				t.Errorf("load config: %s", err)
			}

			gotCreds, err := gotConfig.Credentials.Retrieve(context.TODO())
			if err != nil {
				t.Errorf("retrieve credentials: %s", err)
			}

			if testCase.wantAccessKeyId != gotCreds.AccessKeyID {
				t.Errorf("access key id: want %s but got %s", testCase.wantAccessKeyId, gotCreds.AccessKeyID)
			}

			if testCase.wantSecretAccessKey != gotCreds.SecretAccessKey {
				t.Errorf("access key id: want %s but got %s", testCase.wantSecretAccessKey, gotCreds.SecretAccessKey)
			}
		})
	}
}

func TestSTSCredentials(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := cloudcredentialv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add cloudcredentialv1 to scheme: %s", err)
	}
	if err := authv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add authv1 to scheme: %s", err)
	}

	for _, testCase := range []struct {
		name              string
		kubeClientObjects []runtime.Object
		ctrlClientObjects []ctrlclient.Object
		region            string
		tokenRequest      *authv1.TokenRequest
		assumeRoleOutput  *sts.AssumeRoleWithWebIdentityOutput
		wantConfig        *aws.Config
		wantCred          aws.Credentials
		wantErrLoadConfig error
	}{
		{
			name:   "Get temporary token",
			region: "us-east-2",
			tokenRequest: &authv1.TokenRequest{
				ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "cluster-init"},
				Status:     authv1.TokenRequestStatus{Token: "1234567890"},
			},
			kubeClientObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: v1.ObjectMeta{Namespace: "ci", Name: "cluster-init-secret-creds"},
					Data: map[string][]byte{
						"credentials": []byte("role_arn = arn:aws:iam::123456789000:role/build99-ci-cluster-init"),
					},
				},
			},
			ctrlClientObjects: []ctrlclient.Object{
				&cloudcredentialv1.CredentialsRequest{
					ObjectMeta: v1.ObjectMeta{Namespace: credentialsRequestNS, Name: credentialsRequestName},
					Spec: cloudcredentialv1.CredentialsRequestSpec{
						SecretRef: corev1.ObjectReference{Namespace: "ci", Name: "cluster-init-secret-creds"},
					},
					Status: cloudcredentialv1.CredentialsRequestStatus{Provisioned: true},
				},
			},
			assumeRoleOutput: &sts.AssumeRoleWithWebIdentityOutput{
				Credentials: &types.Credentials{
					AccessKeyId:     aws.String("access-key-id"),
					SecretAccessKey: aws.String("secret-access-key"),
					Expiration:      aws.Time(time.Date(1900, 1, 1, 1, 1, 1, 1, &time.Location{})),
				},
			},
			wantCred: aws.Credentials{
				AccessKeyID:     "access-key-id",
				SecretAccessKey: "secret-access-key",
				SessionToken:    "arn:aws:iam::123456789000:role/build99-ci-cluster-init__1234567890",
				Source:          stscreds.WebIdentityProviderName,
				CanExpire:       true,
				Expires:         time.Date(1900, 1, 1, 1, 1, 1, 1, &time.Location{}),
			},
		},
		{
			name:   "Unprovisioned credentials request",
			region: "us-east-2",
			ctrlClientObjects: []ctrlclient.Object{
				&cloudcredentialv1.CredentialsRequest{
					ObjectMeta: v1.ObjectMeta{Namespace: credentialsRequestNS, Name: credentialsRequestName},
					Spec: cloudcredentialv1.CredentialsRequestSpec{
						SecretRef: corev1.ObjectReference{Namespace: "ci", Name: "cluster-init-secret-creds"},
					},
					Status: cloudcredentialv1.CredentialsRequestStatus{Provisioned: false},
				},
			},
			wantErrLoadConfig: errors.New("credentials provider: credentials request openshift-cloud-credential-operator/cluster-init not provisioned yet"),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			fakeKubeClient := fake.NewClientset(testCase.kubeClientObjects...)
			fakeKubeClient.PrependReactor("create", "serviceaccounts", func(action clientgotesting.Action) (bool, runtime.Object, error) {
				return true, testCase.tokenRequest, nil
			})

			fakeCtrlClient := ctrlfake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(testCase.ctrlClientObjects...).
				Build()

			configGetter := ConfigFromCluster(fakeKubeClient, fakeCtrlClient)
			gotConfig, gotErr := configGetter(context.TODO(), GetConfigOptions{
				Region: testCase.region,
				STSClientGetter: func() stscreds.AssumeRoleWithWebIdentityAPIClient {
					return &FakeAssumeRoleWithWebIdentityAPIClient{Output: testCase.assumeRoleOutput, Err: nil}
				},
			})

			if gotErr != nil && testCase.wantErrLoadConfig == nil {
				t.Fatalf("want err nil but got: %v", gotErr)
			}
			if gotErr == nil && testCase.wantErrLoadConfig != nil {
				t.Fatalf("want err %v but nil", testCase.wantErrLoadConfig)
			}
			if gotErr != nil && testCase.wantErrLoadConfig != nil {
				if testCase.wantErrLoadConfig.Error() != gotErr.Error() {
					t.Fatalf("expect error %q but got %q", testCase.wantErrLoadConfig.Error(), gotErr.Error())
				}
				return
			}

			gotCreds, gotErr := gotConfig.Credentials.Retrieve(context.TODO())
			if gotErr != nil {
				t.Errorf("retrieve credentials: %s", gotErr)
			}

			if diff := cmp.Diff(testCase.wantCred, gotCreds); diff != "" {
				t.Errorf("unexpected cred:\n%s", diff)
			}
		})
	}
}
