#!/usr/bin/env bash
set -euo pipefail
set +x
umask 077
repo=
src=
dst=
pat=${GH_TOKEN:-}
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
restore_only=0
snapshot=
usage() {
  cat <<EOF
Usage: $0 --repo OWNER/REPO --from SOURCE --to TARGET [--pat PAT]
       $0 [--repo OWNER/REPO --to TARGET] --restore-protection SNAPSHOT.json

Reset TARGET to SOURCE, saving its old commit in a backup branch.
Missing repository and branch values are prompted for. The PAT is read
from --pat or GH_TOKEN, or requested with a hidden prompt.

Options:
  --repo OWNER/REPO           GitHub repository
  --from SOURCE              Source branch whose commit will be copied
  --to TARGET                Target branch whose history will be overwritten
  --pat PAT                  Administrator personal access token
  --restore-protection FILE  Restore protection only; infer repo/target from FILE
  -h, --help                 Show this help
EOF
}
while (($#)); do
  case $1 in
    --repo|--from|--to|--pat|--restore-protection)
      [[ $# -ge 2 && -n $2 ]] || { echo "Missing value for $1." >&2; exit 2; }
      case $1 in
        --repo) repo=$2 ;;
        --from) src=$2 ;;
        --to) dst=$2 ;;
        --pat) pat=$2 ;;
        --restore-protection) restore_only=1; snapshot=$2 ;;
      esac
      shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo 'Unknown argument. Use --help for usage.' >&2; exit 2 ;;
  esac
done
for command in git gh jq cmp; do
  command -v "$command" >/dev/null || { echo "Missing command: $command" >&2; exit 1; }
done
# Keep the recovery command usable even when called with only the snapshot.
if ((restore_only)); then
  snapshot_repo=$(jq -er '.url | capture("^https://api.github.com/repos/(?<repo>[^/]+/[^/]+)/branches/(?<branch>.+)/protection$") | .repo' "$snapshot")
  snapshot_branch=$(jq -er '.url | capture("^https://api.github.com/repos/(?<repo>[^/]+/[^/]+)/branches/(?<branch>.+)/protection$") | .branch' "$snapshot")
  [[ -n $repo ]] || repo=$snapshot_repo
  if [[ -z $dst ]]; then
    dst=$(printf '%b' "${snapshot_branch//%/\\x}")
  fi
fi
[[ -n $repo ]] || read -rp 'Repository (OWNER/REPO): ' repo
if ((!restore_only)); then
  [[ -n $src ]] || read -rp 'From branch (source): ' src
fi
[[ -n $dst ]] || read -rp 'To branch (target to overwrite): ' dst
[[ $repo =~ ^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$ ]] || {
  echo 'Repository must have the form OWNER/REPO.' >&2; exit 2
}
git check-ref-format "refs/heads/$dst" >/dev/null || { echo 'Invalid target branch.' >&2; exit 2; }
if ((!restore_only)); then
  git check-ref-format "refs/heads/$src" >/dev/null || { echo 'Invalid source branch.' >&2; exit 2; }
  [[ $src != "$dst" ]] || { echo 'Source and target branches must differ.' >&2; exit 2; }
fi
dst_api=$(jq -nr --arg name "$dst" '$name | @uri')
url="https://github.com/$repo.git"
protection="repos/$repo/branches/$dst_api/protection"
snapshot_prefix="${repo//\//--}-$dst_api-protection"
if [[ -z $pat ]]; then
  read -rsp 'Administrator PAT: ' pat
  echo
fi
[[ -n $pat ]] || { echo 'An administrator PAT is required.' >&2; exit 1; }
GH_TOKEN=$pat
unset pat
export GH_TOKEN GIT_TERMINAL_PROMPT=0
api() { gh api --hostname github.com "$@"; }
[[ $(api "repos/$repo" --jq '.permissions.admin') == true ]] || {
  echo "The supplied PAT does not have administrator access to $repo." >&2
  exit 1
}

tmp=$(mktemp -d)
removed=0
payload="$tmp/protection-request.json"
signatures=false

# GET responses contain URLs and account objects; PUT needs booleans and names.
protection_payload() {
  jq -eS --arg endpoint "https://api.github.com/$protection" \
    --arg raw_endpoint "https://api.github.com/repos/$repo/branches/$dst/protection" '
    if (.url != $endpoint and .url != $raw_endpoint) or (.enforce_admins.enabled | type) != "boolean"
    then error("Invalid protection snapshot for this branch") else . end
    | def actors:
        {users: ([(.users // [])[] | .login] | sort),
         teams: ([(.teams // [])[] | .slug] | sort),
         apps: ([(.apps // [])[] | .slug] | sort)};
      {
        required_status_checks: (
          if .required_status_checks == null then null else
            .required_status_checks
            | {strict, checks: (
                (.checks // ((.contexts // []) | map({context: ., app_id: -1})))
                | map({context, app_id: (.app_id // -1)})
                | sort_by(.context, .app_id))}
          end),
        enforce_admins: .enforce_admins.enabled,
        required_pull_request_reviews: (
          if .required_pull_request_reviews == null then null else
            .required_pull_request_reviews
            | {dismiss_stale_reviews, require_code_owner_reviews,
               required_approving_review_count,
               require_last_push_approval: (.require_last_push_approval // false)}
              + (if .dismissal_restrictions == null then {} else
                  {dismissal_restrictions: (.dismissal_restrictions | actors)} end)
              + (if .bypass_pull_request_allowances == null then {} else
                  {bypass_pull_request_allowances: (.bypass_pull_request_allowances | actors)} end)
          end),
        restrictions: (if .restrictions == null then null else .restrictions | actors end),
        required_linear_history: (.required_linear_history.enabled // false),
        allow_force_pushes: (.allow_force_pushes.enabled // false),
        allow_deletions: (.allow_deletions.enabled // false),
        block_creations: (.block_creations.enabled // false),
        required_conversation_resolution: (.required_conversation_resolution.enabled // false),
        lock_branch: (.lock_branch.enabled // false),
        allow_fork_syncing: (.allow_fork_syncing.enabled // false)
      }
  ' "$1"
}

prepare_restore() {
  protection_payload "$snapshot" > "$payload" || return
  signatures=$(jq -r '.required_signatures.enabled // false' "$snapshot") || return
}

restore_protection() {
  api -X PUT "$protection" --input "$payload" --silent || return
  if [[ $signatures == true ]]; then
    api -X POST "$protection/required_signatures" --silent || return
  fi
  api "$protection" > "$tmp/restored.json" || return
  protection_payload "$tmp/restored.json" > "$tmp/restored-request.json" || return
  cmp -s "$payload" "$tmp/restored-request.json" || {
    echo 'Restored protection differs from the snapshot.' >&2
    return 1
  }
  [[ $(jq -r '.required_signatures.enabled // false' "$tmp/restored.json") == "$signatures" ]] || return 1
  removed=0
  printf 'Protection restored and verified: %s\n' "$dst"
}

cleanup() {
  local status=$?
  trap - EXIT
  if ((removed)); then
    if ! restore_protection; then
      printf 'ERROR: protection restoration failed. Retry: %q --repo %q --to %q --restore-protection %q\n' "$0" "$repo" "$dst" "$snapshot" >&2
      status=1
    fi
  fi
  rm -rf -- "$tmp"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

g() {
  # Keep HTTPS/PAT authentication even when ~/.gitconfig rewrites GitHub to SSH.
  GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_COUNT=0 \
    git -C "$tmp" -c protocol.allow=never -c protocol.https.allow=always \
    -c credential.helper= \
    -c 'credential.helper=!f() { if [ "$1" = get ]; then printf "%s\n" "username=x-access-token" "password=$GH_TOKEN"; fi; }; f' "$@"
}

if ((restore_only)); then
  prepare_restore
  removed=1
  restore_protection
  exit 0
fi

# Recover an earlier failed run before starting another reset.
has_protection=0
protected=$(api "repos/$repo/branches/$dst_api" --jq '.protected')
if [[ $protected == true ]]; then
  has_protection=1
elif [[ $protected == false ]]; then
  shopt -s nullglob
  legacy_prefix="${repo##*/}-$dst_api-protection"
  candidates=("$script_dir"/"$snapshot_prefix"-*.json "$PWD"/"$snapshot_prefix"-*.json
              "$script_dir"/"$legacy_prefix"-*.json "$PWD"/"$legacy_prefix"-*.json)
  for candidate in "${candidates[@]}"; do
    # Older filenames omitted the owner, so validate their repository too.
    if protection_payload "$candidate" >/dev/null 2>&1 && [[ -z $snapshot || $candidate -nt $snapshot ]]; then
      snapshot=$candidate
    fi
  done
  if [[ -n $snapshot ]]; then
    printf 'Recovering protection from: %s\n' "$snapshot"
    prepare_restore
    removed=1
    restore_protection
    has_protection=1
  else
    printf 'No existing branch protection on %s:%s.\n' "$repo" "$dst"
  fi
else
  echo 'Could not determine target branch protection.' >&2
  exit 1
fi

g init -q
g fetch -q --depth=1 "$url" "refs/heads/$src"
new=$(g rev-parse FETCH_HEAD)
old=$(api "repos/$repo/git/ref/heads/$dst_api" --jq '.object.sha')
[[ $new =~ ^[0-9a-f]{40}$ && $old =~ ^[0-9a-f]{40}$ ]] || {
  echo 'Could not read valid branch commit IDs.' >&2
  exit 1
}
if [[ $new == "$old" ]]; then
  printf '%s already points to %s.\n' "$dst" "$new"
  exit 0
fi
stamp=$(date -u +%Y%m%dT%H%M%SZ)
backup="backup/$dst-$stamp"
if ((has_protection)); then
  snapshot="$PWD/$snapshot_prefix-$stamp.json"
  api "$protection" > "$snapshot"
  prepare_restore
fi
printf 'Reset %s:%s from %s: %s -> %s\n' "$repo" "$dst" "$src" "$old" "$new"
read -rp "Type $dst to confirm overwriting its history: " answer
[[ $answer == "$dst" ]] || exit 1

# Check push authentication while protection is still in place.
g push --dry-run --force-with-lease="refs/heads/$dst:$old" \
  "$url" "$new:refs/heads/$dst"
# Preserve the old target before removing protection.
api -X POST "repos/$repo/git/refs" \
  -f ref="refs/heads/$backup" -f sha="$old" >/dev/null
# Arm cleanup first: DELETE can succeed even if its response is lost.
if ((has_protection)); then
  removed=1
  api -X DELETE "$protection" --silent
fi
# Refuse to overwrite any target update since we read its SHA.
g push --force-with-lease="refs/heads/$dst:$old" \
  "$url" "$new:refs/heads/$dst"
if ((has_protection)); then
  restore_protection
fi
[[ $(api "repos/$repo/git/ref/heads/$dst_api" --jq '.object.sha') == "$new" ]]
printf 'Done. Backup: %s\n' "$backup"
if ((has_protection)); then
  printf 'Protection snapshot: %s\n' "$snapshot"
fi
