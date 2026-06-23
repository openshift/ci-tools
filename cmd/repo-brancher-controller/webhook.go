package main

import (
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/github"
)

type pushEventHandler struct {
	state      *desiredState
	controller *controller
}

func (h *pushEventHandler) handle(_ *logrus.Entry, event github.PushEvent) {
	if event.Deleted || !strings.HasPrefix(event.Ref, "refs/heads/") {
		webhooksTotal.WithLabelValues("ignored_ref").Inc()
		return
	}
	keys := h.state.matching(event.Repo.Owner.Login, event.Repo.Name, event.Branch())
	h.controller.enqueue(keys...)
	webhooksTotal.WithLabelValues("accepted").Inc()
}
