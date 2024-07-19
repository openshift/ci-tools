package steps

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/codebuild"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/sirupsen/logrus"
)

type sourceAwsStep struct {
	config                  api.SourceStepConfiguration
	resources               api.ResourceConfiguration
	client                  BuildClient
	podClient               kubernetes.PodClient
	jobSpec                 *api.JobSpec
	cloneAuthConfig         *CloneAuthConfig
	pullSecret              *corev1.Secret
	awsCodeBuildProjectName string
}

func (s *sourceAwsStep) Inputs() (api.InputDefinition, error) {
	return s.jobSpec.Inputs(), nil
}

func (*sourceAwsStep) Validate() error { return nil }

func (s *sourceAwsStep) Run(ctx context.Context) error {
	return results.ForReason("cloning_source").ForError(s.run(ctx))
}

func (s *sourceAwsStep) run(ctx context.Context) error {
	fromDigest, err := resolvePipelineImageStreamTagReference(ctx, s.client, s.config.From, s.jobSpec)
	if err != nil {
		return err
	}
	return createAwsBuild(s.config, s.jobSpec, s.resources, s.pullSecret, fromDigest, s.awsCodeBuildProjectName)
}

func (s *sourceAwsStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From)}
}

func (s *sourceAwsStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *sourceAwsStep) Provides() api.ParameterMap {
	return api.ParameterMap{
		utils.PipelineImageEnvFor(s.config.To): utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.PipelineImageStream, string(s.config.To)),
	}
}

func (s *sourceAwsStep) Name() string { return s.config.TargetName() }

func (s *sourceAwsStep) Description() string {
	return fmt.Sprintf("Clone the correct source code into an image and tag it as %s", s.config.To)
}

func (s *sourceAwsStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func SourceAwsStep(
	config api.SourceStepConfiguration,
	resources api.ResourceConfiguration,
	buildClient BuildClient,
	podClient kubernetes.PodClient,
	jobSpec *api.JobSpec,
	cloneAuthConfig *CloneAuthConfig,
	pullSecret *corev1.Secret,
	awsCodeBuildProjectName string,
) api.Step {
	return &sourceAwsStep{
		config:                  config,
		resources:               resources,
		client:                  buildClient,
		podClient:               podClient,
		jobSpec:                 jobSpec,
		cloneAuthConfig:         cloneAuthConfig,
		pullSecret:              pullSecret,
		awsCodeBuildProjectName: awsCodeBuildProjectName,
	}
}

func createAwsBuild(config api.SourceStepConfiguration, jobSpec *api.JobSpec, resources api.ResourceConfiguration, pullSecret *corev1.Secret, fromDigest string, awsCodeBuildProjectName string) error {
	sess, err := session.NewSession()
	if err != nil {
		logrus.Errorf("Error creating session: %v", err)
		return err
	}

	svc := codebuild.New(sess)
	buildTask, err := svc.StartBuild(&codebuild.StartBuildInput{
		ProjectName:            aws.String(awsCodeBuildProjectName),
		SourceLocationOverride: aws.String(jobSpec.Refs.RepoLink),
		SourceVersion:          aws.String(jobSpec.Refs.BaseSHA),
	})
	if err != nil {
		logrus.Errorf("Error starting build for project %q: %v", awsCodeBuildProjectName, err)
		return err
	}
	fmt.Printf("Started build for project %q\n", awsCodeBuildProjectName)

	describeBuildInput := &codebuild.BatchGetBuildsInput{
		Ids: []*string{buildTask.Build.Id},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			logrus.Errorf("Timeout waiting for build to complete for project %q", awsCodeBuildProjectName)
			return fmt.Errorf("timeout waiting for build to complete for project %q", awsCodeBuildProjectName)
		default:
			builds, err := svc.BatchGetBuilds(describeBuildInput)
			if err != nil {
				logrus.Errorf("Error describing build for project %q: %v", awsCodeBuildProjectName, err)
				return err
			}

			if builds.Builds == nil || len(builds.Builds) == 0 {
				logrus.Errorf("Got nil builds")
				return fmt.Errorf("got nil builds")
			}

			build := builds.Builds[0]
			if build.BuildStatus != nil && *build.BuildStatus == codebuild.StatusTypeSucceeded {
				logrus.Infof("Build succeeded for project %q", awsCodeBuildProjectName)
				return nil
			} else if build.BuildStatus != nil && *build.BuildStatus == codebuild.StatusTypeFailed {
				logrus.Errorf("Build failed for project %q", awsCodeBuildProjectName)
				return fmt.Errorf("build failed for project %q", awsCodeBuildProjectName)
			}

			time.Sleep(30 * time.Second)
		}
	}
}
