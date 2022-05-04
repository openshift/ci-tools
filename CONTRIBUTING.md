# Contributing to ci-tools

## The Code Review Process
### Submit a PR

The author submits a PR. The PR should contain the following to help the reviewers and approvers understand the PR and make the code review process more efficient:
- a short summary of what is being done.
- an informative body of what problem the PR solves and how it solves the problem. If the PR also brings any shortcomings or limitations, they should be mentioned too.
- the identifier of the Jira card (or an upstream issue) if applicable. Then the Jira plugin will link the PR to the Jira card.
- log or other types of output to show the problem that it attempts to solve, or the result of the solved problem
- a preview or a screenshot based on the PR if the PR is about a UI change.

The author should check and ensure the presubmits of the PR run successfully.
In addition, the author should keep the change in a PR [small, simple](https://google.github.io/eng-practices/review/developer/small-cls.html) and independent, and [respond to the comments](https://google.github.io/eng-practices/review/developer/handling-comments.html) from the reviewers.

### Assign Reviewers and Approvers
The [blunderbuss](https://github.com/kubernetes/test-infra/tree/master/prow/plugins/blunderbuss) plugin chooses the reviewers and the approvers defined in the [OWNERS](https://www.kubernetes.dev/docs/guide/owners/) files. All TP members are both reviewers and approvers of the test platform’s github repositories.

#### Reviewers
Reviewers look for: general code quality, correctness, sane software engineering, style, etc.
Anyone in the organization, besides the chosen reviewers except the author of the PR, can act as a reviewer.
If the changes made by the PR look good to them, a reviewer types `/lgtm` in a PR comment; if they change their mind, `/lgtm cancel`.

#### Approvers
The PR author `/assign`s the suggested approvers to approve the PR.
Only the approvers listed in the OWNERS file can approve the PR.
Approvers look for holistic acceptance criteria, including dependencies with other features, forwards/backwards compatibility, API and flag definitions, etc
If the changes made by the PR look good to them, a reviewer types `/approve` in a PR comment; if they change their mind, `/approve cancel`.

### PR Merge Automation
Once all the conditions are satisfied, [Tide](https://github.com/kubernetes/test-infra/blob/master/prow/cmd/tide/README.md) merges the PR.

### Sanity Check After Merging
The PR author is responsible for ensuring the change lands in the production system and works as expected. If the users get impacted by an error caused by the change, revert the PR and roll back the system to give space to think of the fix. If no one is around to approve the reverted PR, impersonate the merge robot whose credentials are stored in BitWarden to do the green button merge with a comment on the PR indicating who is behind the scenes. This is only for the cases where our production system does not work properly and the revert is going to fix it.

## The Code Review Guidelines
In general, reviewers should favor approving a PR once it is in a state where it definitely improves the overall code health of the system being worked on, even if it isn’t perfect.

### Design and Functionality
Reviewers should take the following into account:
- Does the PR bring a useful feature to the system? Does the PR implement the feature requested in a Jira card from the DP team’s current sprint?
- Should the functionality be refactored into an existing tool? For example, if someone added a new tool rather than enhancing an existing one.
- Is there any existing code that can be refactored and/or reused in the PR?
- How to verify if the new feature or the fix from the PR works after it lands in production?

### PR size

If the size of a PR is too big, reviewers can think about breaking it into smaller ones.

### Tests
[The Pull Request Workflow section](https://docs.google.com/document/d/1Qd4qcRHUxk5-eiFIjQm2TTH1TaGQ-zhbphLNXxyvr00/edit?usp=sharing) as described in “Definition of Done”

### Naming
A good name is long enough to fully communicate what the item is or does, without being so long that it becomes hard to read.

### Comments
- Is the comment useful? E.g., explaining why some code exists.
- Is there a TODO comment that can be removed since  the PR does the TODO?
- GoLang documentation on Classes/Functions/Fields are also comments. Are they written properly?

### Documentation
Should the change from the PR be documented in the README file? Should the README file be created for the PR if it is for a new tool? Should the ci-docs site be updated accordingly in case the change will impact our CI users?

### Every line
Check every line of human written code. There should be automation checking the generated code. The reviewers should ask questions until they understand what the code is doing.

For the critical or complex changes, it is acceptable to review the code partially, comment LGTM on the part and ask other reviewers to cover the rest.

### Errors Handling
- Are the errors handled correctly in the PR? Should it be ignored, logged, wrapped and raised up?
- Is the error message informative enough for the developer to understand the error?

### Logging
- Is the correct logger used? File, Standard error?
- Is the level correct?

### Parallel Programming
- Is there a potential deadlock?
- Is there a racing condition?

### Security
- Does it expose any sensitive information to the Internet?
- Could an API be abused?

### Impact After Merging
- Do we need to announce the change to avoid users’ surprises?
- Could the PR cause orphaned objects in the production? Should we do the cleanup manually?

### Good Things
_If you see something nice in the CL, tell the developer, especially when they addressed one of your comments in a great way. Code reviews often just focus on mistakes, but they should offer encouragement and appreciation for good practices, as well. It’s sometimes even more valuable, in terms of mentoring, to tell a developer what they did right than to tell them what they did wrong._ [1]

## References
1. [Google Engineering Practices Documentation](https://google.github.io/eng-practices/): [How to do a code review](https://google.github.io/eng-practices/review/reviewer/) and [The CL author’s guide to getting through code review](https://google.github.io/eng-practices/review/developer/)
1. [The Code Review Process in Kubernetes community](https://github.com/kubernetes/community/blob/master/contributors/guide/owners.md#the-code-review-process)
1. [Submitting patches: the essential guide to getting your code into the kernel](https://www.kernel.org/doc/html/latest/process/submitting-patches.html)
