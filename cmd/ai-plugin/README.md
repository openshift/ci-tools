# AI Plugin

A Prow plugin that integrates with an AI service to provide automated code reviews, pull request descriptions, and commit message suggestions. This plugin acts as a bridge between GitHub pull requests and an external AI service. By commenting specific commands on a PR, users can trigger the AI to review code, generate PR descriptions, or suggest commit messages. The plugin ensures only authorized users can access these features and communicates securely with the AI backend.

## Features

- **AI Code Reviews**: Get automated code reviews by commenting `/ai review` on pull requests
- **PR Descriptions**: Generate pull request descriptions with `/ai pr_description`
- **Commit Messages**: Get commit message suggestions with `/ai commit_message`
- **Access Control**: Only members of the `openshift` organization can use the plugin

## Usage

### Commands

| Command | Description | Example |
|---------|-------------|---------|
| `/ai review` | Request an AI code review of the pull request | `/ai review` |
| `/ai pr_description` | Generate a description for the pull request | `/ai pr_description` |
| `/ai commit_message` | Get a commit message suggestion | `/ai commit_message` |
| `/ai <anything>` | Defaults to AI review | `/ai help` |

### Prerequisites

- Must be a member of the `openshift` GitHub organization
- Comment must be made on a pull request (not a regular issue)

###  How it works

The `provider` in this plugin is an abstraction layer that defines how the AI plugin communicates with different AI backend services.
The `provider.go` file defines a Provider interface with methods like GetRequest and GetResponse.
Concrete implementations (such as `awsbedrockprovider.go`) implement this interface for specific AI services (e.g., AWS Bedrock). With this implementation it is easy to swap out the backend AI service (OpenAI, AWS Bedrock, etc.) by providing a different implementation of the Provider interface.
