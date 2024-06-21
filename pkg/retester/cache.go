package retester

import "sigs.k8s.io/prow/pkg/tide"

type retestBackoffAction int

const (
	retestBackoffHold = iota
	retestBackoffPause
	retestBackoffRetest
)

type backoffCache interface {
	check(pr tide.PullRequest, baseSha string, policy RetesterPolicy) (retestBackoffAction, string)
	load() error
	save() error
}
