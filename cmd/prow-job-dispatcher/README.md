# prow-job-dispatcher

As designed in [[DPTP-1152] Choose a cluster for prow jobs](https://docs.google.com/document/d/1aiuZ70jtvZiQBo2P8NgacRj0GmqUH6DRxE4KFFph1RM/edit) this tool chooses a cluster in the CI build farm for Prow jobs.

* It starts off by figuring out how many runs of each Prow jobs we had in the last seven days by querying the Prometheus instance in Prow-monitoring stack.
* It groups all jobs from a Prow job file together and will always try to put all of them on the same cluster.
* If a job has config stating it must be on a specific cluster, that will always be respected. This could lead to a job with tests on different clusters. We should not have many of those cases.
* If all e2e jobs in a group run on the same cloud provider, it will only consider clusters on that cloud provider, if any. Otherwise, all build clusters are considered.
* It will then choose the cluster with the least number of jobs, based on the Prometheus metrics and the already dispatched jobs.

The choices of cluster are stored in the following stanza of [the config file](https://github.com/openshift/release/blob/main/core-services/sanitize-prow-jobs/_config.yaml) of [`sanitize-prow-jobs`](../sanitize-prow-jobs).

```
buildFarm:
  aws: 
    build01:
      jobs:
      - job-name-1
  gcp: 
    build02:
      jobs:
      - job-name-1

```

The tool `sanitize-prow-jobs` will then use the stored information to generate the `cluster` field of the Prow jobs.

We can use [run-prow-job-dispatcher.sh](../../hack/run-prow-job-dispatcher.sh) to build and run the tool locally.
