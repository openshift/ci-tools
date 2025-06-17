# AI Plugin

A Prow plugin that integrates with an AI service to provide automated code reviews, pull request descriptions, and commit message suggestions.

## Features

- **AI Code Reviews**: Get automated code reviews by commenting `/ai review` on pull requests
- **PR Descriptions**: Generate pull request descriptions with `/ai pr_description`
- **Commit Messages**: Get commit message suggestions with `/ai commit_message`
- **Access Control**: Only members of the `openshift` organization can use the plugin
- **Dry Run Mode**: Test the plugin without actually posting comments

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
- AI service must be running and accessible

### Deployment

The plugin is deployed as a service in the `ci` namespace. See `ai-plugin.yaml` for the complete deployment configuration.

Key components:
- **ServiceAccount**: `ai-plugin` in the `ci` namespace
- **Service**: Exposes the plugin on port 80 (container port 8888)
- **Deployment**: Runs the plugin with proper secrets and configuration
- **Route**: Exposes the plugin for external access

## AI Service Integration

The plugin communicates with an AI service via HTTP POST requests:

- **Health Check**: `GET /` - Checks if AI service is running
- **Review**: `POST /review` - Requests code review
- **PR Description**: `POST /pr_description` - Requests PR description
- **Commit Message**: `POST /commit_message` - Requests commit message

