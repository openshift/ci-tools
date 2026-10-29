# Slack-bot

This is a Slack bot that helps facilitate common tasks like reporting issues.
Currently, the bot can do the following:
- When the bot is explicitly mentioned in a message (`@DPTP bot`), it lists all available actions it knows how to do, like file a bug, request a consultation, and more. 
- When a specific job link is included in a message, the bot responds with helpful information related to that job.
- In the `CoreOS` slack space, when someone tags `@dptp-helpdesk` in the `forum-ocp-testplatform` channel, the bot sends an automatic reply containing helpful basic information in a new thread. 
- Support-request mode (enabled by default in `#forum-ocp-testplatform`): if a thread exceeds `--support-request-threshold` messages (default `12`), the bot creates a Jira issue in `DPTP`, posts the link in the thread, and closes that Jira with `Done` when `:closed:` is added to the root thread message.
- When configured, a Jira Automation web request asks chai-bot to implement a DPTP issue and open a pull request in `#team-dp-testplatform` after the `dptp-ai-ready` label is added.

# Jira label notifications

The bot accepts `POST https://slack.ci.openshift.org/jira/label-endpoint` when
`--jira-automation-token-path`, `--jira-label-channel-id`, and
`--jira-label-chai-bot-user-id` are set. The token must be a dedicated secret
with no trailing newline. Use the immutable Slack
channel ID for `#team-dp-testplatform`; the request cannot choose a channel.
Use chai-bot's Slack **member ID** (not its display name or app ID) so the
message contains a real `<@member-id>` mention. The deployment currently uses
`U0AKNPBBVT7`. Chai-bot must be present in
the channel and configured to process mentions from other bots.
The bot's deployment must mount the secret and pass all three flags before enabling
the Jira rule.

Create a Jira Automation rule scoped to the **DPTP** project with these steps:

1. **Field value changed**: monitor **Labels**, choose **Value added**, and
   include both issue creation and editing if the label may be set at creation.
2. Add a smart-value condition: `{{addedfieldChange.values}}` **equals**
   `dptp-ai-ready`.
3. **Send web request** to the URL above, using `POST`, content type
   `application/json`, and a hidden `X-Jira-Automation-Token` header whose value
   exactly matches the mounted secret. Choose **Custom data** with this body:

   ```json
   {
     "issueKey": "{{issue.key}}",
     "issueURL": "{{issue.url}}",
     "summary": "{{issue.summary.jsonEncode}}",
     "label": "{{addedfieldChange.values}}"
   }
   ```

Test by adding the label to a
test issue and checking both the rule audit log and the target Slack channel.
Removing or editing other labels should not post another message. The bot
accepts only `DPTP` links on `redhat.atlassian.net` and escapes
the issue summary before sending it to Slack.

# Alert-proxy callbacks

`--alert-proxy-interaction-url` and `--alert-proxy-mention-url` enable forwarding
alert-proxy Slack interactions and `@dptp-bot` mentions in the configured alerts
channel. Set `--alert-proxy-forwarder-secret-path` to the shared signing secret
file when either endpoint is enabled.

The endpoint URLs must use the exact `/interactions` or `/mentions` path,
respectively. Approved destinations are:

- The in-cluster service `alert-proxy.ci.svc` or
  `alert-proxy.ci.svc.cluster.local`, using HTTP or HTTPS on port `8080`.
- The Core CI route `alert-proxy-ci.apps.master.ci.devcluster.openshift.com`,
  using HTTPS with no explicit port or port `443`, for example
  `https://alert-proxy-ci.apps.master.ci.devcluster.openshift.com/interactions`.

User information, queries, fragments, and escaped paths are rejected. The
forwarder signs requests with the shared secret and does not follow redirects.

# Local testing
There is an alpha instance of Slack Bot running on the app.ci cluster that you can use for testing by running a mitmproxy and reverse tunneling requests to your local machine.

- Make sure to join the `dptp-robot-testing` slack space.
- Add your personal ssh key to `authorized_keys` at https://vault.ci.openshift.org/ui/vault/secrets/kv/show/dptp/sshd-bastion-slack-bot-alpha
- If attempting to test the helpdesk-message handler, update the `helpdesk_alias` var to your slack user-id in the `dptp-robot-testing` space
- Run the `hack/local-slack-bot.sh` script like so: `RELEASE_REPO_DIR=<your openshift/release repo dir> bash local-slack-bot.sh`
- Now you can go into the `dptp-robot-testing` space and execute one of the `/dptp-*` commands, and it should interact with your local slack bot.
