package main

import (
	"sort"
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/github"
)

type pushEventHandler struct {
	state      *desiredState
	controller *controller
}

func (h *pushEventHandler) handle(logger *logrus.Entry, event github.PushEvent) {
	if logger == nil {
		logger = logrus.NewEntry(logrus.StandardLogger())
	}
	logger = logger.WithFields(logrus.Fields{
		"org":     event.Repo.Owner.Login,
		"repo":    event.Repo.Name,
		"ref":     event.Ref,
		"deleted": event.Deleted,
	})
	if event.Deleted || !strings.HasPrefix(event.Ref, "refs/heads/") {
		webhooksTotal.WithLabelValues("ignored_ref").Inc()
		logger.Debug("ignored push event")
		return
	}
	keys := h.state.matching(event.Repo.Owner.Login, event.Repo.Name, event.Branch())
	if len(keys) == 0 {
		webhooksTotal.WithLabelValues("unconfigured").Inc()
		logger.WithField("branch", event.Branch()).Debug("push event has no configured forwarding target")
		return
	}
	h.controller.enqueue(keys...)
	webhooksTotal.WithLabelValues("accepted").Inc()
	logger.WithFields(logrus.Fields{
		"branch": event.Branch(),
		"keys":   repoKeyStrings(keys),
	}).Info("accepted push event")
}

func repoKeyStrings(keys []repoKey) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key.String())
	}
	sort.Strings(out)
	return out
}
