#!/bin/bash
set -euo pipefail

OLD_VERSION="${1:-}"
NEW_VERSION="${2:-}"
RELEASE_REPO="${3:-}"

if [ -z "$OLD_VERSION" ] || [ -z "$NEW_VERSION" ] || [ -z "$RELEASE_REPO" ]; then
    echo "Usage: $0 <old_version> <new_version> <release_repo>"
    echo "Example: $0 4.21 4.22 /path/to/release"
    exit 1
fi

echo "=========================================="
echo "Bumping Analysis Jobs"
echo "=========================================="
echo ""
echo "Old version: $OLD_VERSION"
echo "New version: $NEW_VERSION"
echo "Release repo: $RELEASE_REPO"
echo ""

cd "$RELEASE_REPO"

# Export variables for Python script
export OLD_VERSION
export NEW_VERSION

# Python script to extract and bump jobs
python3 << 'PYTHON_EOF'
import re
import sys
import os

old_version = os.environ['OLD_VERSION']
new_version = os.environ['NEW_VERSION']

def bump_analysis_job(job_text, old_ver, new_ver):
    """Replace version numbers in a job definition"""
    return job_text.replace(old_ver, new_ver)

def extract_jobs(file_path, job_names):
    """Extract complete job definitions for given job names from a YAML file"""
    with open(file_path, 'r') as f:
        content = f.read()

    jobs = []
    for job_name in job_names:
        # Find the job by name - match from "- " to the next "- " or end of file
        pattern = rf'(\n- .*?\n  name: {re.escape(job_name)}\n.*?)(?=\n- |\Z)'
        match = re.search(pattern, content, re.DOTALL)
        if match:
            jobs.append(match.group(1))
            print(f"  ✓ Found job: {job_name}", file=sys.stderr)
        else:
            print(f"  ✗ Could not find job: {job_name}", file=sys.stderr)

    return jobs

def append_bumped_jobs(file_path, job_names, old_ver, new_ver):
    """Extract jobs, bump versions, and append to the file"""
    print(f"\nProcessing {file_path}...", file=sys.stderr)
    jobs = extract_jobs(file_path, job_names)

    if not jobs:
        print(f"  No jobs found in {file_path}", file=sys.stderr)
        return False

    # Read the current file
    with open(file_path, 'r') as f:
        content = f.read()

    # Bump versions in the extracted jobs
    bumped_jobs = [bump_analysis_job(job, old_ver, new_ver) for job in jobs]

    # Append the bumped jobs to the file
    new_content = content.rstrip() + '\n' + '\n'.join(bumped_jobs) + '\n'

    with open(file_path, 'w') as f:
        f.write(new_content)

    print(f"  ✓ Added {len(bumped_jobs)} bumped jobs", file=sys.stderr)
    return True

# Bump multiarch jobs
multiarch_file = 'ci-operator/jobs/openshift/multiarch/openshift-multiarch-master-periodics.yaml'
multiarch_jobs = [
    f'periodic-ci-openshift-multiarch-master-nightly-{old_version}-install-analysis-all-multi-p-p',
    f'periodic-ci-openshift-multiarch-master-nightly-{old_version}-install-analysis-all-ppc64le',
    f'periodic-ci-openshift-multiarch-master-nightly-{old_version}-install-analysis-all-s390x',
]

print("\n=== Multiarch Analysis Jobs ===", file=sys.stderr)
append_bumped_jobs(multiarch_file, multiarch_jobs, old_version, new_version)

# Bump release jobs
release_file = 'ci-operator/jobs/openshift/release/openshift-release-master-periodics.yaml'
release_jobs = [
    f'periodic-ci-openshift-release-master-nightly-{old_version}-install-analysis-all',
    f'periodic-ci-openshift-release-master-nightly-{old_version}-upgrade-analysis-all',
    f'periodic-ci-openshift-release-master-nightly-{old_version}-overall-analysis-all',
]

print("\n=== Release Analysis Jobs ===", file=sys.stderr)
append_bumped_jobs(release_file, release_jobs, old_version, new_version)

print("\n✓ All analysis jobs bumped successfully", file=sys.stderr)

PYTHON_EOF

echo ""
echo "=========================================="
echo "✓ Analysis jobs bumped!"
echo "=========================================="
