# slack-bot

## What
Interactive Slack bot for OpenShift CI operations. It listens for Slack events (messages, app mentions) and interaction callbacks (shortcuts, modal submissions) and provides helpdesk workflows, Jira issue filing, keyword auto-responses, job link enrichment, and FAQ management.

## How it works -- full flow

### HTTP server
The bot runs an HTTP server with two Slack-facing endpoints:
- `/slack/events-endpoint` -- receives Slack Events API callbacks (messages, app mentions)
- `/slack/interactive-endpoint` -- receives Slack interaction payloads (shortcut triggers, modal view submissions, block actions)

Both endpoints verify request authenticity using the Slack signing secret before processing.

### Event handling
Events are routed through a `MultiHandler` that dispatches to these handlers in order:

1. **helpdesk.MessageHandler** -- monitors the forum channel (`#forum-ocp-testplatform`) for messages. When someone tags `@dptp-helpdesk` in the channel, the bot sends an automatic reply with helpful basic information in a new thread. When `--require-workflows-in-forum` is enabled, it directs users to use the Slack workflow for new posts. Also responds to keyword matches from the keywords config.
2. **helpdesk.FAQHandler** -- manages FAQ items stored as Kubernetes ConfigMaps in the `ci` namespace. Authorized users (from the `test-platform-ci-admins` group) can create, update, and delete FAQ entries via thread reactions.
3. **supportrequest.HandlerWithLock** -- monitors the support channel for threads that exceed `--support-request-threshold` messages (default `12`). When exceeded, creates a Jira issue in `DPTP`, posts the link in the thread, and closes that Jira with `Done` when `:closed:` is added to the root thread message.
4. **mention.Handler** -- when the bot is @-mentioned, it responds with contextual suggestions for interactive workflows (bug report, consultation, enhancement, incident, helpdesk, triage) based on the phrasing used.
5. **joblink.Handler** -- detects Prow job URLs in messages and posts enriched information: job status, GCS artifact links, ci-operator config metadata.

### Interaction handling
Interactions are routed through a modal router that manages Slack modal flows for:
- **Bug** -- file a Jira bug report
- **Consultation** -- request a consultation
- **Enhancement** -- file an enhancement request
- **Helpdesk** -- file a helpdesk ticket
- **Incident** -- report an incident
- **Triage** -- triage an issue

Each modal flow is triggered via Slack shortcuts or message button presses and progresses through multi-step modal views, ultimately creating Jira issues in the DPTP project.

### Response pattern
Events are always acknowledged with HTTP 200 immediately. Actual handling runs in a background goroutine so Slack's 3-second timeout is never exceeded.

## Flags

| Flag | Default | What it controls |
|---|---|---|
| `--port` | `8888` | HTTP server listen port |
| `--log-level` | `info` | Log output level |
| `--grace-period` | `180s` | Graceful shutdown duration |
| `--slack-token-path` | (required) | Path to file containing the Slack bot token |
| `--slack-signing-secret-path` | (required) | Path to file containing the Slack signing secret |
| `--keywords-config-path` | (empty) | Path to the keywords auto-response config file |
| `--helpdesk-alias` | `@dptp-helpdesk` | Alias for the helpdesk user(s) |
| `--forum-channel-id` | `CBN38N3MW` | Slack channel ID for `#forum-ocp-testplatform` |
| `--review-request-workflow-id` | `B06T46F374N` | Slack workflow ID for review requests |
| `--namespace` | `ci` | Kubernetes namespace for storing helpdesk FAQ ConfigMaps |
| `--require-workflows-in-forum` | `true` | Require use of Slack workflows in the forum channel |
| `--prow-config-path` | (Prow) | Path to Prow config (for job link enrichment) |
| `--prow-job-config-path` | (Prow) | Path to Prow job configs |
| `--support-request-channel-id` | `CBN38N3MW` | Slack channel ID for support request monitoring |
| `--support-request-threshold` | `12` | Thread reply count threshold to auto-create Jira support-request ticket |
| `--pager-duty-token-file` | — | Path to PagerDuty API token file |
| `--jira-*` | (various) | Jira connection options for issue filing |

## Key files
- `cmd/slack-bot/main.go` -- entry point, HTTP server setup, request verification
- `pkg/slack/events/router/router.go` -- event handler multiplexer
- `pkg/slack/events/helpdesk/helpdesk-message.go` -- forum channel message handling, keyword responses
- `pkg/slack/events/helpdesk/helpdesk-faq.go` -- FAQ management via ConfigMaps
- `pkg/slack/events/mention/mention.go` -- @-mention response with workflow suggestions
- `pkg/slack/events/joblink/link.go` -- Prow job URL detection and enrichment
- `pkg/slack/interactions/router/router.go` -- interaction callback router for modal flows
- `pkg/slack/modals/bug/`, `consultation/`, `enhancement/`, `helpdesk/`, `incident/`, `triage/` -- individual modal flow implementations
- `pkg/jira/issues.go` -- Jira issue creation backend

## Deployment
Long-lived Deployment on app.ci in the `ci` namespace. Requires in-cluster access for Kubernetes API (FAQ ConfigMap storage) and `userv1` scheme (authorized user lookup). Uses GCS client (unauthenticated, read-only) for job artifact lookups.

Slack app must be configured to send Events API and Interactivity payloads to this service's endpoints.
## Local testing
There is an alpha instance of Slack Bot running on the app.ci cluster that you can use for testing by running a mitmproxy and reverse tunneling requests to your local machine.

- Make sure to join the `dptp-robot-testing` slack space.
- Add your personal ssh key to `authorized_keys` at https://vault.ci.openshift.org/ui/vault/secrets/kv/show/dptp/sshd-bastion-slack-bot-alpha
- If attempting to test the helpdesk-message handler, update the `helpdesk_alias` var to your slack user-id in the `dptp-robot-testing` space
- Run the `hack/local-slack-bot.sh` script like so: `RELEASE_REPO_DIR=<your openshift/release repo dir> bash hack/local-slack-bot.sh`
- Now you can go into the `dptp-robot-testing` space and execute one of the `/dptp-*` commands, and it should interact with your local slack bot.
