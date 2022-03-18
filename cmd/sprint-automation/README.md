# Sprint-automation

Test Platform's daily helper. Utilizes slack and pager duty to do things such as:
- Remind `team-dp-testplatform` of our rotating positions, and cards awaiting acceptance
- Send the daily intake digest to the intake role
- Send reminders about next week's roles
- Ensure that our aliases are staffed
- Remind triage of necessary upgrades

# Local testing
You can test out `sprint-automation` utilizing the `dptp-robot-testing` and the `hack/local-sprint-automation.sh` script:
- Make sure to join the `dptp-robot-testing` slack space.
- Run the `hack/local-sprint-automation.sh` script like so: `bash local-sprint-automation.sh`
- Now you can go into `dptp-robot-testing` and watch the output of your local `sprint-automation` run.
