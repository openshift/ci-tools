#!/usr/bin/env python3
"""
Tide Query Manager - A query-based tool to modify Prow Tide configurations.

Usage examples:
  # Add staff-eng-approved to release-4.21 queries that have backport-risk-assessed
  ./tide-query-manager.py --prow-config-dir /path/to/prow/config \\
    --query "is:release-4.21 has:backport-risk-assessed add:staff-eng-approved remove:backport-risk-assessed"

  # Add verified label to all main/master branches for specific repos
  ./tide-query-manager.py --prow-config-dir /path/to/prow/config \\
    --query "is:main repo:openshift/ci-tools add:verified"

  # Multiple conditions with wildcards
  ./tide-query-manager.py --prow-config-dir /path/to/prow/config \\
    --query "is:release-4.* has:staff-eng-approved add-branch:release-4.22"
"""

import argparse
import os
import re
import sys
from pathlib import Path
from typing import Dict, List, Optional, Set
import yaml


class QueryCondition:
    """Represents a set of conditions and actions for matching and modifying Tide queries."""

    def __init__(self):
        self.branches: List[str] = []  # branch patterns to match (supports wildcards)
        self.repos: List[str] = []  # repos to match (org/repo format)
        self.has_labels: List[str] = []  # queries must have these labels
        self.missing_labels: List[str] = []  # queries must NOT have these labels
        self.add_labels: List[str] = []  # labels to add
        self.remove_labels: List[str] = []  # labels to remove
        self.add_branches: List[str] = []  # branches to add
        self.remove_branches: List[str] = []  # branches to remove

    @classmethod
    def from_query_string(cls, query: str) -> 'QueryCondition':
        """Parse a query string into a QueryCondition.

        Query format: "key:value key:value ..."

        Supported keys:
        - is, branch: branch pattern to match (supports *)
        - repo: repository to match (org/repo)
        - has, has-label: label that must be present
        - missing, missing-label: label that must be absent
        - add, add-label: label to add
        - remove, remove-label: label to remove
        - add-branch: branch to add
        - remove-branch: branch to remove
        """
        cond = cls()

        # Split by spaces while respecting quotes
        parts = query.split()

        for part in parts:
            if ':' not in part:
                raise ValueError(f"Invalid query part '{part}', expected format 'key:value'")

            key, value = part.split(':', 1)

            if key in ('is', 'branch'):
                cond.branches.append(value)
            elif key == 'repo':
                cond.repos.append(value)
            elif key in ('has', 'has-label'):
                cond.has_labels.append(value)
            elif key in ('missing', 'missing-label'):
                cond.missing_labels.append(value)
            elif key in ('add', 'add-label'):
                cond.add_labels.append(value)
            elif key in ('remove', 'remove-label'):
                cond.remove_labels.append(value)
            elif key == 'add-branch':
                cond.add_branches.append(value)
            elif key == 'remove-branch':
                cond.remove_branches.append(value)
            else:
                raise ValueError(f"Unknown query key '{key}'")

        return cond


def branch_matches(branch: str, pattern: str) -> bool:
    """Check if a branch matches a pattern (supports * wildcard)."""
    regex_pattern = '^' + re.escape(pattern).replace(r'\*', '.*') + '$'
    return bool(re.match(regex_pattern, branch))


def query_matches_condition(query: dict, repo: str, condition: QueryCondition, debug: bool = False) -> bool:
    """Check if a Tide query matches the filter conditions."""
    # Check repo filter
    if condition.repos:
        if repo not in condition.repos:
            if debug:
                print(f"  DEBUG: Repo {repo} not in {condition.repos}")
            return False

    # Check branch filter
    if condition.branches:
        included_branches = query.get('includedBranches', [])
        has_matching_branch = any(
            branch_matches(branch, pattern)
            for branch in included_branches
            for pattern in condition.branches
        )
        if not has_matching_branch:
            if debug:
                print(f"  DEBUG: No matching branch in {included_branches} for patterns {condition.branches}")
            return False

    # Check has-label filter
    query_labels = set(query.get('labels', []))
    for label in condition.has_labels:
        if label not in query_labels:
            if debug:
                print(f"  DEBUG: Missing required label '{label}' in {query_labels}")
            return False

    # Check missing-label filter
    for label in condition.missing_labels:
        if label in query_labels:
            if debug:
                print(f"  DEBUG: Found excluded label '{label}' in {query_labels}")
            return False

    return True


class TideQueryManager:
    """Manages modifications to Prow Tide queries."""

    def __init__(self, prow_config_dir: str, sharded_config_dir: str, dry_run: bool = False, verbose: bool = False):
        self.prow_config_dir = Path(prow_config_dir)
        self.sharded_config_dir = Path(sharded_config_dir)
        self.dry_run = dry_run
        self.verbose = verbose
        self.changed_count = 0

    def load_config(self, file_path: Path) -> Optional[dict]:
        """Load a YAML configuration file."""
        if not file_path.exists():
            return None

        with open(file_path, 'r') as f:
            return yaml.safe_load(f)

    def save_config(self, file_path: Path, config: dict):
        """Save a YAML configuration file."""
        file_path.parent.mkdir(parents=True, exist_ok=True)
        with open(file_path, 'w') as f:
            yaml.dump(config, f, default_flow_style=False, sort_keys=False)

    def process_query(self, query: dict, repo: str, condition: QueryCondition, debug: bool = False) -> bool:
        """Process a single Tide query. Returns True if modified."""
        if debug:
            print(f"DEBUG: Checking query for repo {repo}")
            print(f"  Branches: {query.get('includedBranches', [])}")
            print(f"  Labels: {query.get('labels', [])}")

        if not query_matches_condition(query, repo, condition, debug=debug):
            return False

        labels = set(query.get('labels', []))
        branches = set(query.get('includedBranches', []))

        original_labels = labels.copy()
        original_branches = branches.copy()

        # Add labels
        labels.update(condition.add_labels)

        # Remove labels
        labels.difference_update(condition.remove_labels)

        # Add branches
        branches.update(condition.add_branches)

        # Remove branches
        branches.difference_update(condition.remove_branches)

        labels_changed = labels != original_labels
        branches_changed = branches != original_branches

        if labels_changed or branches_changed:
            self.changed_count += 1

            action = "Would modify" if self.dry_run else "Modified"
            print(f"{action} query for repo {repo}:")

            if labels_changed:
                print(f"  Labels: {sorted(original_labels)} -> {sorted(labels)}")
            if branches_changed:
                print(f"  Branches: {sorted(original_branches)} -> {sorted(branches)}")

            if not self.dry_run:
                query['labels'] = sorted(labels)
                if branches:
                    query['includedBranches'] = sorted(branches)
                elif 'includedBranches' in query:
                    del query['includedBranches']

            return True

        return False

    def process_sharded_configs(self, condition: QueryCondition):
        """Process all sharded Prow config files."""
        # Find all _prowconfig.yaml files in the sharded config directory
        config_files = list(self.sharded_config_dir.glob('**/*_prowconfig.yaml'))

        print(f"DEBUG: Searching in: {self.sharded_config_dir}")
        print(f"DEBUG: Found {len(config_files)} config files")

        if not config_files:
            print(f"Warning: No sharded config files found in {self.sharded_config_dir}")
            return

        for config_file in config_files:
            # Extract org/repo from path
            # Path structure: org/repo/_prowconfig.yaml or org/_prowconfig.yaml
            parts = config_file.relative_to(self.sharded_config_dir).parts
            if len(parts) == 2:
                # org/repo/_prowconfig.yaml
                org, repo = parts[0], parts[1].replace('_prowconfig.yaml', '')
                if repo == '_prowconfig.yaml':  # org-level config
                    repo = parts[0]
                    full_repo = org
                else:
                    full_repo = f"{org}/{repo}"
            elif len(parts) == 3:
                # org/repo/_prowconfig.yaml
                org, repo = parts[0], parts[1]
                full_repo = f"{org}/{repo}"
            else:
                continue

            config = self.load_config(config_file)
            if not config:
                continue

            # Process Tide queries
            tide_config = config.get('tide', {})
            queries = tide_config.get('queries', [])

            modified = False
            for query in queries:
                # Get the repo from the query (should be a single repo in sharded configs)
                query_repos = query.get('repos', [])
                if query_repos:
                    for qr in query_repos:
                        if self.process_query(query, qr, condition, debug=self.verbose):
                            modified = True
                else:
                    # Org-level query
                    if self.process_query(query, full_repo, condition, debug=self.verbose):
                        modified = True

            # Save modified config
            if modified and not self.dry_run:
                self.save_config(config_file, config)

    def run(self, query_string: str):
        """Run the tide query manager with the given query."""
        try:
            condition = QueryCondition.from_query_string(query_string)
        except ValueError as e:
            print(f"Error parsing query: {e}", file=sys.stderr)
            sys.exit(1)

        print(f"Processing with query: {query_string}")
        print(f"Dry run: {self.dry_run}\n")

        self.process_sharded_configs(condition)

        if self.changed_count == 0:
            print("\nNo queries matched the specified conditions")
        else:
            if self.dry_run:
                print(f"\nDry run: would have modified {self.changed_count} queries")
            else:
                print(f"\nSuccessfully modified {self.changed_count} queries")


def main():
    parser = argparse.ArgumentParser(
        description='Modify Prow Tide configurations using a query-based syntax',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Query syntax:
  is:BRANCH          - Match queries with this branch pattern (supports *)
  repo:ORG/REPO      - Match queries for this repository
  has:LABEL          - Match queries that have this label
  missing:LABEL      - Match queries that don't have this label
  add:LABEL          - Add this label to matched queries
  remove:LABEL       - Remove this label from matched queries
  add-branch:BRANCH  - Add this branch to matched queries
  remove-branch:BRANCH - Remove this branch from matched queries

Examples:
  # Replace backport-risk-assessed with staff-eng-approved for release-4.21
  --query "is:release-4.21 has:backport-risk-assessed add:staff-eng-approved remove:backport-risk-assessed"

  # Add verified to all main/master branches
  --query "is:main add:verified"

  # Add acknowledge-critical-fixes-only to specific repos on main
  --query "is:main repo:openshift/ci-tools add:acknowledge-critical-fixes-only"

  # Modify all release-4.x branches
  --query "is:release-4.* has:staff-eng-approved remove:staff-eng-approved"
        """
    )

    parser.add_argument(
        '--prow-config-dir',
        required=True,
        help='Path to the Prow configuration directory'
    )
    parser.add_argument(
        '--sharded-prow-config-base-dir',
        help='Path to the sharded Prow config base directory (defaults to prow-config-dir)'
    )
    parser.add_argument(
        '--query',
        required=True,
        help='Query string specifying conditions and actions'
    )
    parser.add_argument(
        '--dry-run',
        action='store_true',
        help='Print what would be changed without modifying files'
    )
    parser.add_argument(
        '--verbose', '-v',
        action='store_true',
        help='Enable verbose debug output'
    )

    args = parser.parse_args()

    # Default sharded config dir to prow config dir if not specified
    sharded_dir = args.sharded_prow_config_base_dir or args.prow_config_dir

    manager = TideQueryManager(
        prow_config_dir=args.prow_config_dir,
        sharded_config_dir=sharded_dir,
        dry_run=args.dry_run,
        verbose=args.verbose
    )

    manager.run(args.query)


if __name__ == '__main__':
    main()
