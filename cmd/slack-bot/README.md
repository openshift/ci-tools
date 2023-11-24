# Slack-bot

This is a Slack bot that helps facilitate common tasks like reporting issues.
Currently, the bot can do the following:
- When the bot is explicitly mentioned in a message (`@DPTP bot`), it lists all available actions it knows how to do, like file a bug, request a consultation, and more. 
- When a specific job link is included in a message, the bot responds with helpful information related to that job.
- In the `CoreOS` slack space, when someone tags `@dptp-helpdesk` in the `forum-ocp-testplatform` channel, the bot sends an automatic reply containing helpful basic information in a new thread. 

# Local testing
There is an alpha instance of Slack Bot running on the app.ci cluster that you can use for testing by running a mitmproxy and reverse tunneling requests to your local machine.

- Make sure to join the `dptp-robot-testing` slack space.
- Add your personal ssh key to `authorized_keys` at https://vault.ci.openshift.org/ui/vault/secrets/kv/show/dptp/sshd-bastion-slack-bot-alpha
- If attempting to test the helpdesk-message handler, update the `helpdesk_alias` var to your slack user-id in the `dptp-robot-testing` space
- Run the `hack/local-slack-bot.sh` script like so: `RELEASE_REPO_DIR=<your openshift/release repo dir> bash local-slack-bot.sh`
- Now you can go into the `dptp-robot-testing` space and execute one of the `/dptp-*` commands, and it should interact with your local slack bot.
