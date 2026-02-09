package joblink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/GoogleCloudPlatform/testgrid/util/gcs"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/gcsupload"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"
	gcsutil "sigs.k8s.io/prow/pkg/pod-utils/gcs"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/prowgen"
	"github.com/openshift/ci-tools/pkg/rehearse"
	"github.com/openshift/ci-tools/pkg/slack/events"
)

// job abstracts the types of Prow jobs. Only one field can be non-nil
type job struct {
	metadata   api.Metadata
	presubmit  *config.Presubmit
	postsubmit *config.Postsubmit
	periodic   *config.Periodic
}

// JobGetter knows how to retrieve job details for a job by name
type JobGetter interface {
	JobForName(name string) *job
}

// NewJobGetter exposes a simplified interface from the Prow configuration
func NewJobGetter(config config.Getter) JobGetter {
	return &getter{
		config: config,
	}
}

type getter struct {
	config config.Getter
}

func metadataFromConfig(orgRepo string, job config.JobBase, brancher config.Brancher) api.Metadata {
	parts := strings.Split(orgRepo, "/")
	if len(parts) != 2 {
		return api.Metadata{}
	}
	return api.Metadata{
		Org:     parts[0],
		Repo:    parts[1],
		Branch:  rehearse.BranchFromRegexes(brancher.Branches),
		Variant: rehearse.VariantFromLabels(job.Labels),
	}
}

func (g *getter) JobForName(name string) *job {
	var found job

	for orgRepo, presubmits := range g.config().PresubmitsStatic {
		parts := strings.Split(orgRepo, "/")
		if len(parts) != 2 {
			continue
		}
		for _, presubmit := range presubmits {
			if presubmit.Name == name {
				found.presubmit = &presubmit
				found.metadata = metadataFromConfig(orgRepo, presubmit.JobBase, presubmit.Brancher)
				return &found
			}
		}
	}

	for orgRepo, postsubmits := range g.config().PostsubmitsStatic {
		parts := strings.Split(orgRepo, "/")
		if len(parts) != 2 {
			continue
		}
		for _, postsubmit := range postsubmits {
			if postsubmit.Name == name {
				found.postsubmit = &postsubmit
				found.metadata = metadataFromConfig(orgRepo, postsubmit.JobBase, postsubmit.Brancher)
				return &found
			}
		}
	}

	for _, periodic := range g.config().Periodics {
		if periodic.Name == name {
			found.periodic = &periodic
			if len(periodic.ExtraRefs) > 0 {
				found.metadata = api.Metadata{
					Org:     periodic.ExtraRefs[0].Org,
					Repo:    periodic.ExtraRefs[0].Repo,
					Branch:  periodic.ExtraRefs[0].BaseRef,
					Variant: rehearse.VariantFromLabels(periodic.Labels),
				}
			}
			return &found
		}
	}

	return &found
}

type messagePoster interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
}

// Handler returns a handler that knows how to respond to
// messages that mention job details by adding context to
// them and providing commonly-needed information.
func Handler(client messagePoster, config JobGetter, gcsClient *storage.Client) events.PartialHandler {
	return events.PartialHandlerFunc("joblink", func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
		if callback.Type != slackevents.CallbackEvent {
			return false, nil
		}
		event, ok := callback.InnerEvent.Data.(*slackevents.MessageEvent)
		if !ok {
			return false, nil
		}
		infos, err := extractInfo(callback)
		if err != nil {
			logger.WithError(err).Warn("Failed to parse event data")
			return false, err
		}
		if len(infos) == 0 {
			return false, nil
		}
		blocks, err := contextFor(logger, infos, config, gcsClient)
		if err != nil {
			logger.WithError(err).Warn("Failed to get context")
			return false, err
		}
		if blocks == nil {
			return false, nil
		}
		logger.Info("Handling new message with job links...")
		timestamp := event.TimeStamp
		if event.ThreadTimeStamp != "" {
			timestamp = event.ThreadTimeStamp
		}
		responseChannel, responseTimestamp, err := client.PostMessage(event.Channel, slack.MsgOptionBlocks(blocks...), slack.MsgOptionTS(timestamp))
		if err != nil {
			logger.WithError(err).Warn("Failed to post response to comment")
		} else {
			logger.Infof("Posted response to comment in channel %s at %s", responseChannel, responseTimestamp)
		}
		return true, err
	})
}

func rehearsalFromName(name string) (string, string) {
	var rehearsalPR string
	if trimmed := strings.TrimPrefix(name, "rehearse-"); trimmed != name {
		idx := strings.Index(trimmed, "-")
		rehearsalPR = trimmed[:idx]
		name = trimmed[idx+1:]
	}
	return name, rehearsalPR
}

func contextFor(logger *logrus.Entry, infos []jobInfo, config JobGetter, gcsClient *storage.Client) ([]slack.Block, error) {
	var blocks []slack.Block
	for _, info := range infos {
		logger = logger.WithFields(logrus.Fields{
			"job": info.Name,
			"id":  info.Id,
		})
		name, rehearsalPR := rehearsalFromName(info.Name)
		if rehearsalPR != "" {
			logger.Debugf("Job is a rehearsal of %s for PR %s", name, rehearsalPR)
		}
		job := config.JobForName(name)
		if job == nil {
			continue
		}
		var spec prowapi.ProwJobSpec
		var options *prowapi.GCSConfiguration
		var generated bool
		var err error
		var prefix string
		// we do not want to do a bunch of parsing of URLs to get
		// at the refs that were used to trigger the job, and we
		// can just look up the alias path in GCS anyway so we don't
		// need to do any work to get the right artifact path anyway
		if job.presubmit != nil {
			spec = pjutil.PresubmitSpec(*job.presubmit, prowapi.Refs{})
			options = job.presubmit.DecorationConfig.GCSConfiguration
			generated, err = jobconfig.IsGenerated(job.presubmit.JobBase, prowgen.Generator)
			if err != nil {
				return nil, err
			}
			job.metadata.Variant = rehearse.VariantFromLabels(job.presubmit.Labels)
			prefix = jobconfig.PresubmitPrefix
		} else if job.postsubmit != nil {
			spec = pjutil.PostsubmitSpec(*job.postsubmit, prowapi.Refs{})
			options = job.postsubmit.DecorationConfig.GCSConfiguration
			generated, err = jobconfig.IsGenerated(job.postsubmit.JobBase, prowgen.Generator)
			if err != nil {
				return nil, err
			}
			job.metadata.Variant = rehearse.VariantFromLabels(job.postsubmit.Labels)
			prefix = jobconfig.PostsubmitPrefix
		} else if job.periodic != nil {
			spec = pjutil.PeriodicSpec(*job.periodic)
			options = job.periodic.DecorationConfig.GCSConfiguration
			generated, err = jobconfig.IsGenerated(job.periodic.JobBase, prowgen.Generator)
			if err != nil {
				return nil, err
			}
			job.metadata.Variant = rehearse.VariantFromLabels(job.periodic.Labels)
			prefix = jobconfig.PeriodicPrefix
		} else {
			// should not happen with non-nil job
			logger.Warn("No job was found but a non-nil job was returned.")
			continue
		}
		text := bytes.Buffer{}
		text.WriteString("*" + job.metadata.TestNameFromJobName(name, prefix) + ":*")
		if rehearsalPR != "" {
			text.WriteString("\n- This job is a rehearsal for <https://github.com/openshift/release/pull/" + rehearsalPR + "|PR " + rehearsalPR + ">.")
		}
		if generated {
			text.WriteString("\n - `ci-operator` <https://github.com/openshift/release/tree/main/ci-operator/config/" + job.metadata.RelativePath() + "|config>.")
		} else {
			text.WriteString("\n - This job is not generated from `ci-operator` configuration; DPTP may not be able to support questions for it.")
		}

		if info.Id != "" && rehearsalPR == "" {
			var path string
			dspec := downwardapi.NewJobSpec(spec, info.Id, "")
			if alias := gcsutil.AliasForSpec(&dspec); alias != "" {
				logger = logger.WithField("path", alias)
				logger.Debug("Resolving path from alias.")
				prefix := fmt.Sprintf("gs://%s", options.Bucket)
				// dereference alias to get the path
				var p gcs.Path
				if err := p.Set(fmt.Sprintf("%s/%s", prefix, alias)); err != nil {
					logger.WithError(err).Warn("Could not set path for alias read.")
					continue
				}
				if p.Object() == "" {
					logger.Warn("Alias read found empty object name.")
					continue
				}
				reader, err := gcsClient.Bucket(p.Bucket()).Object(p.Object()).NewReader(context.Background())
				if err != nil {
					logger.WithError(err).Warn("Could not open alias for read.")
					continue
				}
				symlink, err := io.ReadAll(reader)
				if err != nil {
					logger.WithError(err).Warn("Could not read alias.")
					continue
				}
				path = strings.TrimPrefix(string(symlink), prefix)
			} else {
				_, path, _ = gcsupload.PathsForJob(options, &dspec, "")
			}
			logger.WithField("path", path).Debug("Resolved full GCS path.")
			text.WriteString("\n - Job result <https://prow.ci.openshift.org/view/gs/" + options.Bucket + "/" + path + "|link>.")
		}

		blocks = append(blocks, &slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.MarkdownType,
				Text: text.String(),
			},
		})
	}
	if blocks == nil {
		return nil, nil
	}
	return append([]slack.Block{&slack.SectionBlock{
		Type: slack.MBTSection,
		Text: &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: "It looks like you mentioned a job result in your message. Here is some helpful information:",
		},
	}}, blocks...), nil
}

type jobInfo struct {
	// our jobs have globally unique names so we can
	// get away with identifying them with this minimal
	// set of information
	Name, Id string
	Line     int
}

// extractInfo extracts information about any jobs that were
// linked in a comment body on Slack
func extractInfo(event *slackevents.EventsAPIEvent) ([]jobInfo, error) {
	raw, ok := event.Data.(*slackevents.EventsAPICallbackEvent)
	if !ok {
		return nil, errors.New("could not get raw event content")
	}
	type blocks struct {
		Blocks []struct {
			Elements []struct {
				Elements []struct {
					Url string `json:"url"`
				} `json:"elements"`
			} `json:"elements"`
		} `json:"blocks"`
	}
	var data blocks
	if err := json.Unmarshal(*raw.InnerEvent, &data); err != nil {
		return nil, errors.New("could not get blocks from event data")
	}

	infos := map[jobInfo]interface{}{}
	for _, block := range data.Blocks {
		for _, element := range block.Elements {
			for _, subElement := range element.Elements {
				if subElement.Url != "" {
					if url, err := url.Parse(subElement.Url); err == nil {
						if info := infoFromUrl(url); info != nil {
							infos[*info] = nil
						}
					}
				}
			}
		}
	}

	var infoList []jobInfo
	for info := range infos {
		infoList = append(infoList, info)
	}

	return infoList, nil
}

func infoFromUrl(url *url.URL) *jobInfo {
	switch url.Host {
	case api.DomainForService(api.ServiceProw):
		switch strings.Split(url.Path, "/")[1] {
		case "job-history":
			return infoForJobHistory(url)
		case "log":
			return infoForJobLog(url)
		case "view":
			return infoForJobView(url)
		}
	case api.DomainForService(api.ServiceGCSWeb), api.DomainForService(api.ServiceGCSStorage):
		return infoForArtifact(url)
	}
	return nil
}

// infoForJobHistory handles URLs like:
// https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/release-openshift-origin-installer-e2e-gcp-upgrade-4.7?buildId=1234123123
func infoForJobHistory(url *url.URL) *jobInfo {
	parts := strings.Split(url.Path, "/")
	if len(parts) < 1 {
		return nil
	}
	return &jobInfo{
		Name: parts[len(parts)-1],
		Id:   url.Query().Get("buildId"),
	}
}

// infoForJobLog handles URLs like:
// https://prow.ci.openshift.org/log?job=pull-ci-openshift-installer-release-4.6-e2e-metal-ipi&id=1319125780608847872
func infoForJobLog(url *url.URL) *jobInfo {
	return &jobInfo{
		Name: url.Query().Get("job"),
		Id:   url.Query().Get("id"),
	}
}

// infoForJobView handles URLs like:
// https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/pull/openshift_release/12371/rehearse-12371-periodic-ci-kubevirt-kubevirt-master-e2e-nested-virt/1318930182802771968
func infoForJobView(url *url.URL) *jobInfo {
	parts := strings.Split(url.Path, "/")
	if len(parts) < 2 {
		return nil
	}
	var line int
	if fragment := strings.Split(url.Fragment, ":"); len(fragment) == 3 {
		if l, err := strconv.Atoi(fragment[2]); err == nil {
			line = l
		}
	}
	return &jobInfo{
		Name: parts[len(parts)-2],
		Id:   parts[len(parts)-1],
		Line: line,
	}
}

// infoForArtifact handles URLs like:
// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/pr-logs/pull/25585/pull-ci-openshift-origin-master-e2e-aws-disruptive/1319310480841379840/build-log.txt
// https://storage.googleapis.com/test-platform-results/pr-logs/pull/openshift_cluster-ingress-operator/836/pull-ci-openshift-cluster-ingress-operator-master-e2e-aws-operator/1583384716713660416/build-log.txt
func infoForArtifact(url *url.URL) *jobInfo {
	parts := strings.Split(url.Path, "/")
	// the last fully numeric path part before user-provided
	// artifacts will be the job ID
	index := -1
	for i, part := range parts {
		if part == "artifacts" {
			break
		}
		if _, err := strconv.Atoi(part); err == nil {
			index = i
		}
	}
	if index == -1 {
		return nil
	}
	return &jobInfo{
		Name: parts[index-1],
		Id:   parts[index],
	}
}
