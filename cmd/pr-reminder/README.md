# PR Reminder
A tool to remind Test Platform team members of active PR review requests in the repos the team cares about.
The tool utilizes the following configuration:
- `config-path`: The location of the tool's config file; containing: `teamMembers`, `teamName`, and `repos`
- `github-mapping-config-path` The location of the github mapping config file. This file contains a map of github ids to kerberos ids.
- `slack-token-path`: The location of a file containing the slack token.

## Overview
Each of the `teamMembers` in the config will have their `slack id` and `github id` resolved utilizing their inferred email (`{kerberosId}@redhat.com`), and the mapping config respectively.
PRs will then be gathered via the github API for each of the `repos` in the config, and added to users based on the `requested_reviewers` and `requested_teams` attributes.
Finally, a slack message will be sent to each of the `teamMember's` containing information about each PR review request.

## Local Development
A script, `hack/local-pr-reminder.sh`, exists for running the tool locally. This script takes no arguments, but the user must be logged into the `app.ci` cluster.
The cluster is utilized to obtain the production `config` and `github-mapping` files, and the `slack-token` for the alpha slack instance.
The script will run the tool, and message corresponding slack users in the `dptp-robot-testing` space.
