package retester

import "k8s.io/test-infra/prow/tide"

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
