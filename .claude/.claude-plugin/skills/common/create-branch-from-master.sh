#!/bin/bash
set -euo pipefail

[ "$#" -lt 2 ] || [ "$#" -gt 3 ] && echo "Usage: $0 <branch-name> <release-repo> [--reset]" && exit 1

BRANCH="$1"
USE_RESET=false
[ "$#" -eq 3 ] && [ "$3" = "--reset" ] && USE_RESET=true

cd "$2"
git rev-parse --git-dir >/dev/null 2>&1 || { echo "Error: Not a git repo"; exit 1; }

REMOTE=$(git remote | grep -q "^upstream$" && echo "upstream" || echo "origin")
git fetch $REMOTE

[ "$(git branch --show-current)" != "master" ] && git checkout master

if [ "$USE_RESET" = true ]; then
    git reset --hard $REMOTE/master
else
    git rebase $REMOTE/master || { echo "Error: Rebase failed"; exit 1; }
fi

git show-ref --verify --quiet "refs/heads/$BRANCH" && git branch -D "$BRANCH"

git checkout -b "$BRANCH"
echo "✓ Branch: $BRANCH"
