# Slack-bot

This is a Slack bot that helps facilitate common tasks like reporting issues.

# Local testing
There is an alpha instance of Slack Bot running on the app.ci cluster that you can use for testing by running a mitmproxy and reverse tunneling requests to your local machine.

- Make sure to join the `dptp-robot-testing` slack space.
- Add your personal ssh key to `authorized_keys` at https://vault.ci.openshift.org/ui/vault/secrets/kv/show/dptp/sshd-bastion-slack-bot-alpha
- Run the `hack/local-slack-bot.sh` script like so: `RELEASE_REPO_DIR=<your openshift/release repo dir> bash local-slack-bot.sh`
- Now you can go into the `dptp-robot-testing` space and execute one of the `/dptp-*` commands, and it should interact with your local slack bot.