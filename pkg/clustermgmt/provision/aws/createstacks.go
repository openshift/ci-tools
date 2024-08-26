package aws

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cloudformationtypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
)

var (
	createStackCompleteWaitTimeDef time.Duration = 10 * time.Minute
)

// CloudFormationClient is a convenience interface that has been created
// to make unit test easier to write
type CloudFormationClient interface {
	CreateStack(ctx context.Context, params *cloudformation.CreateStackInput, optFns ...func(*cloudformation.Options)) (*cloudformation.CreateStackOutput, error)
	DescribeStacks(context.Context, *cloudformation.DescribeStacksInput, ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error)
}

type CloudFormationClientGetter func() (CloudFormationClient, error)
type TemplateResolver func(path string) (string, error)

type createAWSStacksStep struct {
	log                         *logrus.Entry
	getClusterInstall           clustermgmt.ClusterInstallGetter
	getCFClient                 CloudFormationClientGetter
	createStackCompleteWaitTime *time.Duration
	templateResolver            TemplateResolver
}

func (s *createAWSStacksStep) Name() string {
	return "create-aws-stacks"
}

func (s *createAWSStacksStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", "provision: aws: create stacks")

	ci, err := s.getClusterInstall()
	if err != nil {
		return fmt.Errorf("get cluster install: %w", err)
	}

	if ci.Provision.AWS == nil {
		log.Info("No AWS provision stanza")
		return nil
	}
	if len(ci.Provision.AWS.CloudFormationTemplates) == 0 {
		log.Info("No cloud formations templates stanza")
		return nil
	}

	client, err := s.getCFClient()
	if err != nil {
		return fmt.Errorf("get cloud formation client: %w", err)
	}

	if err := s.createStacks(ctx, log, client, ci.Provision.AWS.CloudFormationTemplates); err != nil {
		return err
	}
	if err := waitForStacksToComplete(ctx, log, client, ci.Provision.AWS.CloudFormationTemplates, *s.createStackCompleteWaitTime); err != nil {
		return err
	}

	return nil
}

func (s *createAWSStacksStep) createStacks(ctx context.Context,
	log *logrus.Entry,
	client CloudFormationClient,
	templates []clustermgmt.AWSCloudFormationTemplate) error {
	for _, t := range templates {
		log := log.WithField("stack", t.StackName)

		params := make([]cloudformationtypes.Parameter, len(t.Parameters))
		for i, p := range t.Parameters {
			params[i] = cloudformationtypes.Parameter{ParameterKey: &p.Key, ParameterValue: &p.Value}
		}
		capabilities := make([]cloudformationtypes.Capability, len(t.Capabilities))
		for i, s := range t.Capabilities {
			capabilities[i] = cloudformationtypes.Capability(s)
		}

		templateBody, err := s.templateResolver(t.TemplateBody)
		if err != nil {
			return fmt.Errorf("read template body file %s: %w", t.TemplateBody, err)
		}

		log.Info("Creating stack")
		_, err = client.CreateStack(ctx, &cloudformation.CreateStackInput{
			StackName:    &t.StackName,
			TemplateBody: aws.String(templateBody),
			Parameters:   params,
			Capabilities: capabilities,
		})

		if err != nil {
			aee := &cloudformationtypes.AlreadyExistsException{}
			if errors.As(err, &aee) {
				log.Warn("Stack exists already, skipping")
			} else {
				return fmt.Errorf("create stack %s: %w", t.StackName, err)
			}
		}
	}
	return nil
}

func waitForStacksToComplete(ctx context.Context, log *logrus.Entry, client CloudFormationClient,
	templates []clustermgmt.AWSCloudFormationTemplate, wait time.Duration) error {
	waiter := cloudformation.NewStackCreateCompleteWaiter(client)
	waiters, wCtx := errgroup.WithContext(ctx)
	waiters.SetLimit(len(templates))
	for _, t := range templates {
		waiters.Go(func() error {
			log := log.WithField("stack", t.StackName)
			log.Info("Waiting to complete")
			if err := waiter.Wait(wCtx, &cloudformation.DescribeStacksInput{StackName: &t.StackName},
				wait); err != nil {
				return fmt.Errorf("stack %s failed: %w", t.StackName, err)
			}
			log.Info("Created successfully")
			return nil
		})
	}
	return waiters.Wait()
}

func resolveTemplate(path string) (string, error) {
	t, err := os.ReadFile(path)
	return string(t), err
}

func NewCreateAWSStacksStep(log *logrus.Entry,
	getClusterInstall clustermgmt.ClusterInstallGetter,
	getCFClient CloudFormationClientGetter,
	createStackCompleteWaitTime *time.Duration,
	templateResolver TemplateResolver) *createAWSStacksStep {
	if createStackCompleteWaitTime == nil {
		createStackCompleteWaitTime = &createStackCompleteWaitTimeDef
	}
	if templateResolver == nil {
		templateResolver = resolveTemplate
	}
	return &createAWSStacksStep{
		log:                         log,
		getClusterInstall:           getClusterInstall,
		getCFClient:                 getCFClient,
		createStackCompleteWaitTime: createStackCompleteWaitTime,
		templateResolver:            templateResolver,
	}
}
