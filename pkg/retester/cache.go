package retester

import (
	"context"

	"sigs.k8s.io/prow/pkg/tide"
)

type retestBackoffAction int

const (
	retestBackoffHold = iota
	retestBackoffPause
	retestBackoffRetest
)

type backoffCache interface {
	check(pr tide.PullRequest, baseSha string, policy RetesterPolicy) (retestBackoffAction, string)
	load(ctx context.Context) error
	save(ctx context.Context) error
}
