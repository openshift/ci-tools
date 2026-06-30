package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"k8s.io/apimachinery/pkg/util/sets"
)

const maxSlackIssues = 5

type branchIssueKey struct {
	org, repo, source, target string
}

type branchIssue struct {
	key       branchIssueKey
	reason    string
	sourceSHA string
	targetSHA string
	firstSeen time.Time
	lastSeen  time.Time
	count     int
	lastError string
}

type branchIssueStore struct {
	mu     sync.RWMutex
	issues map[branchIssueKey]branchIssue
	now    func() time.Time
}

func newBranchIssueStore() *branchIssueStore {
	return &branchIssueStore{issues: map[branchIssueKey]branchIssue{}, now: time.Now}
}

func (s *branchIssueStore) upsert(issue branchIssue) {
	if s == nil {
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.issues[issue.key]; ok {
		issue.firstSeen = existing.firstSeen
		issue.count = existing.count + 1
	} else {
		issue.firstSeen = now
		issue.count = 1
	}
	issue.lastSeen = now
	s.issues[issue.key] = issue
}

func (s *branchIssueStore) resolve(key branchIssueKey) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.issues, key)
}

func (s *branchIssueStore) prune(desired map[repoKey]sets.Set[string]) {
	if s == nil {
		return
	}
	valid := map[branchIssueKey]struct{}{}
	for key, targets := range desired {
		for target := range targets {
			valid[branchIssueKey{org: key.org, repo: key.repo, source: key.source, target: target}] = struct{}{}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.issues {
		if _, ok := valid[key]; !ok {
			delete(s.issues, key)
		}
	}
}

func (s *branchIssueStore) snapshot() []branchIssue {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	issues := make([]branchIssue, 0, len(s.issues))
	for _, issue := range s.issues {
		issues = append(issues, issue)
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].lastSeen.Equal(issues[j].lastSeen) {
			return issues[i].key.String() < issues[j].key.String()
		}
		return issues[i].lastSeen.After(issues[j].lastSeen)
	})
	return issues
}

func (k branchIssueKey) String() string {
	return fmt.Sprintf("%s/%s:%s->%s", k.org, k.repo, k.source, k.target)
}

func (k branchIssueKey) compareURL() string {
	return fmt.Sprintf("https://github.com/%s/%s/compare/%s...%s", k.org, k.repo, k.target, k.source)
}

type slackPoster interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
}

type slackDigestPublisher struct {
	client  slackPoster
	channel string
	store   *branchIssueStore
}

func newSlackDigestPublisher(client slackPoster, channel string, store *branchIssueStore) *slackDigestPublisher {
	return &slackDigestPublisher{client: client, channel: channel, store: store}
}

func (p *slackDigestPublisher) publish(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	default:
	}
	issues := p.store.snapshot()
	if len(issues) == 0 {
		return nil
	}
	blocks := p.blocks(issues)
	responseChannel, responseTimestamp, err := p.client.PostMessage(
		p.channel,
		slack.MsgOptionText(fallbackDigestText(issues), false),
		slack.MsgOptionBlocks(blocks...),
		slack.MsgOptionDisableLinkUnfurl(),
	)
	if err != nil {
		return fmt.Errorf("post Slack digest: %w", err)
	}
	logrus.WithFields(logrus.Fields{"channel": responseChannel, "timestamp": responseTimestamp, "issues": len(issues)}).Info("posted repo brancher failure digest")
	return nil
}

func (p *slackDigestPublisher) blocks(issues []branchIssue) []slack.Block {
	shown := issues
	if len(shown) > maxSlackIssues {
		shown = shown[:maxSlackIssues]
	}
	blocks := []slack.Block{
		slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, "Repo brancher fast-forward failures", false, false)),
		slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*%d active failure(s).* Showing the %d most recent.", len(issues), len(shown)), false, false), nil, nil),
	}
	for i, issue := range shown {
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, formatIssue(i+1, issue), false, false), nil, nil))
	}
	if len(issues) > len(shown) {
		blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("Showing %d of %d active failures. Search repo-brancher-controller logs for `branch fast-forward requires manual intervention` for the full list.", len(shown), len(issues)), false, false)))
	}
	return blocks
}

func formatIssue(index int, issue branchIssue) string {
	lines := []string{
		fmt.Sprintf("*%d. <%s|%s/%s>*: `%s` -> `%s`", index, issue.key.compareURL(), issue.key.org, issue.key.repo, issue.key.source, issue.key.target),
		fmt.Sprintf("reason: `%s`; first seen: %s; last seen: %s; attempts: %d", issue.reason, issue.firstSeen.UTC().Format(time.RFC3339), issue.lastSeen.UTC().Format(time.RFC3339), issue.count),
	}
	if issue.sourceSHA != "" || issue.targetSHA != "" {
		lines = append(lines, fmt.Sprintf("source sha: `%s`; target sha: `%s`", shortSHA(issue.sourceSHA), shortSHA(issue.targetSHA)))
	}
	if issue.lastError != "" {
		lines = append(lines, fmt.Sprintf("last error: `%s`", truncate(issue.lastError, 300)))
	}
	return strings.Join(lines, "\n")
}

func fallbackDigestText(issues []branchIssue) string {
	return fmt.Sprintf("Repo brancher has %d active fast-forward failure(s). Showing the %d most recent in Slack blocks.", len(issues), min(len(issues), maxSlackIssues))
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func runSlackDigest(ctx context.Context, interval time.Duration, publisher *slackDigestPublisher) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := publisher.publish(ctx); err != nil {
				logrus.WithError(err).Error("publish repo brancher failure digest")
			}
		}
	}
}
