jobs:
	ci-operator-prowgen --from-dir ./ci-operator/config --to-dir ./ci-operator/jobs
	determinize-prow-jobs --prow-jobs-dir ./ci-operator/jobs
.PHONY: jobs