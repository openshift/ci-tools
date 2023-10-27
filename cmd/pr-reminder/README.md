# PR Reminder
A tool to remind Test Platform team members of active PR review requests in the repos the team cares about.
The tool utilizes the following configuration:
- `config-path`: The location of the tool's config file; containing: `teamMembers`, `teamName`, and `repos`
- `github-users-file` The location of the users' information file. This file contains the GitHub username and Kerberos ID for each user.
- `slack-token-path`: The location of a file containing the slack token.
- `validate-only`: Run in `validate` mode. This simply validates that the config is correct, and will not send any messages or check for PR review requests.

## Overview
Each of the `teams` in the config will have all of their `teamMember's` `slack id` and `github id` resolved utilizing their inferred email (`{kerberosId}@redhat.com`), and the users' config respectively.
PRs will then be gathered via the github API for each of the `repos` in that `team's` config, and added to users based on the `requested_reviewers` and `requested_teams` attributes.
Finally, a slack message will be sent to each of the `teamMember's` containing information about each PR review request.

## Local Development
A script, `hack/local-pr-reminder.sh`, exists for running the tool locally. This script takes no arguments, but the user must be logged into the `app.ci` cluster.
You will want to run the script as `USR="my-kerberos-id" hack/local-pr-reminder.sh` to include your own kerberos ID to receive the message in the testing space.
The cluster is utilized to obtain the production `github-users-file` file and the `slack-token` for the alpha slack instance.
The script will run the tool, and message corresponding slack users in the `dptp-robot-testing` space.
