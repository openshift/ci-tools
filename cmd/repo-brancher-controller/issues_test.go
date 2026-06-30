package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestIssueStoreResolvesSuccessfulFastForward(t *testing.T) {
	key := repoKey{org: "org", repo: "repo", source: "main"}
	state := newDesiredState()
	state.replace(map[repoKey]sets.Set[string]{key: sets.New("release-5.0")})
	refs := &fakeRefClient{
		err: permanentError{errors.New("not a fast forward")},
		refs: map[string]string{
			refID("org", "repo", "main"):        "new",
			refID("org", "repo", "release-5.0"): "old",
		}}
	issues := newBranchIssueStore()
	c := newController(refs, state, 1)
	c.issues = issues
	defer c.queue.ShutDown()

	if err := c.reconcile(context.Background(), key); err == nil {
		t.Fatal("expected initial reconciliation to fail")
	}
	if got := len(issues.snapshot()); got != 1 {
		t.Fatalf("expected active issue after failure, got %d", got)
	}

	refs.err = nil
	refs.refs[refID("org", "repo", "release-5.0")] = "old"
	if err := c.reconcile(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if got := len(issues.snapshot()); got != 0 {
		t.Fatalf("expected issue to be resolved after successful fast-forward, got %d", got)
	}
}

type recordingSlackClient struct {
	channel string
	options []slack.MsgOption
	err     error
}

func (c *recordingSlackClient) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	c.channel = channelID
	c.options = options
	return channelID, "123.456", c.err
}

func TestSlackDigestShowsFiveMostRecentIssues(t *testing.T) {
	store := newBranchIssueStore()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time {
		now = now.Add(time.Minute)
		return now
	}
	for i := 0; i < 7; i++ {
		store.upsert(branchIssue{
			key:       branchIssueKey{org: "org", repo: "repo", source: "main", target: "release-5." + string(rune('0'+i))},
			reason:    "non_fast_forward",
			sourceSHA: "source-sha",
			targetSHA: "target-sha",
			lastError: "GitHub rejected non-forced ref update",
		})
	}

	client := &recordingSlackClient{}
	publisher := newSlackDigestPublisher(client, "CHAN", store)
	if err := publisher.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.channel != "CHAN" {
		t.Fatalf("unexpected channel: %s", client.channel)
	}
	rendered := renderSlackBlocks(publisher.blocks(store.snapshot()))
	if !strings.Contains(rendered, "7 active failure") {
		t.Fatalf("digest did not include total count: %s", rendered)
	}
	if strings.Count(rendered, "reason:") != maxSlackIssues {
		t.Fatalf("expected %d rendered issues, got digest: %s", maxSlackIssues, rendered)
	}
	if !strings.Contains(rendered, "Showing 5 of 7 active failures") {
		t.Fatalf("digest did not include truncation notice: %s", rendered)
	}
}

func TestSlackDigestPublishError(t *testing.T) {
	store := newBranchIssueStore()
	store.upsert(branchIssue{key: branchIssueKey{org: "org", repo: "repo", source: "main", target: "release-5.0"}, reason: "non_fast_forward", lastError: "boom"})
	publisher := newSlackDigestPublisher(&recordingSlackClient{err: errors.New("slack down")}, "CHAN", store)
	if err := publisher.publish(context.Background()); err == nil {
		t.Fatal("expected publish error")
	}
}

func TestIssueStorePrunesRemovedTargets(t *testing.T) {
	store := newBranchIssueStore()
	store.upsert(branchIssue{key: branchIssueKey{org: "org", repo: "repo", source: "main", target: "release-5.0"}, reason: "non_fast_forward"})
	store.upsert(branchIssue{key: branchIssueKey{org: "org", repo: "repo", source: "main", target: "release-5.1"}, reason: "non_fast_forward"})

	store.prune(map[repoKey]sets.Set[string]{
		{org: "org", repo: "repo", source: "main"}: sets.New("release-5.1"),
	})

	issues := store.snapshot()
	if len(issues) != 1 || issues[0].key.target != "release-5.1" {
		t.Fatalf("unexpected remaining issues: %#v", issues)
	}
}

func renderSlackBlocks(blocks []slack.Block) string {
	var parts []string
	for _, block := range blocks {
		switch b := block.(type) {
		case *slack.HeaderBlock:
			if b.Text != nil {
				parts = append(parts, b.Text.Text)
			}
		case *slack.SectionBlock:
			if b.Text != nil {
				parts = append(parts, b.Text.Text)
			}
		case *slack.ContextBlock:
			for _, element := range b.ContextElements.Elements {
				if text, ok := element.(*slack.TextBlockObject); ok {
					parts = append(parts, text.Text)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}
