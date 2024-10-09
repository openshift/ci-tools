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

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	awstypes "github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
)

var (
	createStackCompleteWaitTimeDefault time.Duration = 10 * time.Minute
)

type CloudFormationClientGetter func() (awstypes.CloudFormationClient, error)
type TemplateResolver func(path string) (string, error)

type createAWSStacksStep struct {
	log                         *logrus.Entry
	clusterInstall              *clusterinstall.ClusterInstall
	cfClient                    awstypes.CloudFormationClientGetter
	createStackCompleteWaitTime *time.Duration
	templateResolver            TemplateResolver
}

func (s *createAWSStacksStep) Name() string {
	return "create-aws-stacks"
}

func (s *createAWSStacksStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", "provision: aws: create stacks")

	if s.clusterInstall.Provision.AWS == nil {
		log.Info("No AWS provision stanza")
		return nil
	}
	if len(s.clusterInstall.Provision.AWS.CloudFormationTemplates) == 0 {
		log.Info("No cloud formations templates stanza")
		return nil
	}

	client, err := s.cfClient.CloudFormationClient(ctx)
	if err != nil {
		return fmt.Errorf("get cloud formation client: %w", err)
	}

	if err := s.createStacks(ctx, log, client, s.clusterInstall.Provision.AWS.CloudFormationTemplates); err != nil {
		return err
	}
	if err := waitForStacksToComplete(ctx, log, client, s.clusterInstall.Provision.AWS.CloudFormationTemplates, *s.createStackCompleteWaitTime); err != nil {
		return err
	}

	return nil
}

func (s *createAWSStacksStep) createStacks(ctx context.Context,
	log *logrus.Entry,
	client awstypes.CloudFormationClient,
	templates []awstypes.CloudFormationTemplate) error {
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

func waitForStacksToComplete(ctx context.Context, log *logrus.Entry, client awstypes.CloudFormationClient,
	templates []awstypes.CloudFormationTemplate, wait time.Duration) error {
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
	clusterInstall *clusterinstall.ClusterInstall,
	cfClient awstypes.CloudFormationClientGetter,
	createStackCompleteWaitTime *time.Duration,
	templateResolver TemplateResolver) *createAWSStacksStep {
	if createStackCompleteWaitTime == nil {
		createStackCompleteWaitTime = &createStackCompleteWaitTimeDefault
	}
	if templateResolver == nil {
		templateResolver = resolveTemplate
	}
	return &createAWSStacksStep{
		log:                         log,
		clusterInstall:              clusterInstall,
		cfClient:                    cfClient,
		createStackCompleteWaitTime: createStackCompleteWaitTime,
		templateResolver:            templateResolver,
	}
}
