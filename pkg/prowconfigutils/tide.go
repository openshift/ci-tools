package prowconfigutils

import (
	"strings"
)

const TideRepoMergeTypeWildcard = "*"

func ExtractOrgRepoBranch(orgRepoBranch string) (org, repo, branch string) {
	slashSplit := strings.Split(orgRepoBranch, "/")
	org = slashSplit[0]
	if len(slashSplit) > 1 {
		atSplit := strings.Split(slashSplit[1], "@")
		if len(atSplit) > 1 {
			repo, branch = atSplit[0], atSplit[1]
		} else {
			repo = atSplit[0]
		}
	}
	return
}
