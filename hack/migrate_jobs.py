#!/usr/bin/env python3

### handle the non-prowgen jobs at migration
### example to run the command:
### release_repo=/Users/hongkliu/repo/openshift/release bash -c 'find ${release_repo}  -name "*openshift-origin-master-presubmits.yaml" -exec python3 hack/migrate_jobs.py {} \;'

import collections
import logging
from ruamel.yaml import YAML
from ruamel.yaml.comments import CommentedMap
import sys

filename = sys.argv[1]

def migrate(job):
    if "cluster" in job:
        logging.warning("the cluster of job '%s' has been defined: '%s'", job["name"], job["cluster"])
        return job
    if job["agent"] != "kubernetes":
        logging.warning("the agent '%s' of job '%s' is not 'kubernetes'", job["agent"], job["name"])
        return job
    job['cluster'] = 'ci/api-build01-ci-devcluster-openshift-com:6443'
    containers = []
    found = False
    boskos = False
    for container in job['spec']['containers']:
        if container['image'].endswith("ci-operator:latest"):
            found = True
            if "--lease-server=http://boskos" in container['args']:
                boskos = True
                container['args'].remove("--lease-server=http://boskos")
                container['args'].append("--lease-server-password-file=/etc/boskos/password")
                container['args'].append("--lease-server-username=ci")
                container['args'].append("--lease-server=https://boskos-ci.svc.ci.openshift.org")
                container['volumeMounts'].append({'mountPath': '/etc/boskos', 'name': 'boskos', 'readOnly': True})
            container['args'].append("--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson")
            container['args'].append("--kubeconfig=/etc/apici/kubeconfig")
            container['args'] = sorted(container['args'])
            if not "volumeMounts" in container:
                 container['volumeMounts'] = []
            container['volumeMounts'].append({'mountPath': '/etc/apici', 'name': 'apici-ci-operator-credentials', 'readOnly': True})
            container['volumeMounts'].append({'mountPath': '/etc/pull-secret', 'name': 'pull-secret', 'readOnly': True})
            container['volumeMounts'] = sorted(container['volumeMounts'], key=lambda vm: vm['name'])
        else:
            logging.warning('ignoring appending args and volumes job %s whose image %s is not ci-operator:latest', job["name"], container['image'])
        containers.append(container)
    job['spec']['containers'] = containers
    if found:
        if not "volumes" in job['spec']:
            job['spec']['volumes'] = []
        job['spec']['volumes'].append({'name': 'apici-ci-operator-credentials', 'secret': {'items': [{'key': 'sa.ci-operator.apici.config', 'path': 'kubeconfig'}], 'secretName': 'apici-ci-operator-credentials'}})
        job['spec']['volumes'].append({'name': 'pull-secret', 'secret': {'secretName': 'regcred'}})
        if boskos:
            job['spec']['volumes'].append({'name': 'boskos', 'secret': {'items': [{'key': 'password', 'path': 'password'}], 'secretName': 'boskos-credentials'}})
        job['spec']['volumes'] = sorted(job['spec']['volumes'], key=lambda v: v['name'])
    job = CommentedMap(sorted(job.items(), key=lambda t: t[0]))
    collections.OrderedDict(job.items())
    return job

yaml = YAML()
yaml.compact(seq_seq=False)
yaml.preserve_quotes = True

with open(filename) as f:
    all = yaml.load(f)
    for t in ("presubmits", "postsubmits", "periodics"):
        if t not in filename:
            continue
        for repo in all[t]:
            jobs = []
            for job in all[t][repo]:
                if not "labels" in job or not "ci-operator.openshift.io/prowgen-controlled" in job["labels"] or job["labels"]["ci-operator.openshift.io/prowgen-controlled"] != "true":
                    logging.info('job is not controlled by prowgen: %s', job["name"])
                    jobs.append(migrate(job))
                else:
                    jobs.append(job)
            all[t][repo] = jobs

with open(sys.argv[1], 'w') as f:
    yaml.dump(all, f)
