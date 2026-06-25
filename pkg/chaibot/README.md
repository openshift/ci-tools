# Chaibot - Ship-Help MCP Integration

This package provides test failure analysis using Chai Bot (ship-help MCP) for automatic Slack triage.

## Overview

Chaibot monitors Slack channels for Prow CI job failure URLs and automatically posts analysis using the Chai Bot service via ship-help MCP.

## Files in This PR

- `pkg/chaibot/analyzer.go` - Ship-help MCP client implementation
- `pkg/chaibot/analyzer_test.go` - Unit tests
- `pkg/slack/events/chaibot/handler.go` - Slack event handler (monitors for Prow URLs)
- `pkg/slack/events/chaibot/handler_test.go` - Event handler tests
- `pkg/slack/events/router/router.go` - Updated to register Chaibot handler
- `cmd/slack-bot/main.go` - Updated with Chaibot initialization

**This PR provides the complete implementation.** The integration is ready to use once deployed.

## How It Works

### 1. Event Handler Pattern (Already Implemented)

Chaibot uses the existing event handler pattern in `openshift/ci-tools`:

**Implementation files:**
- `pkg/slack/events/chaibot/handler.go` - Monitors Slack messages for Prow URLs
- Registered in `pkg/slack/events/router/router.go`
- Initialized in `cmd/slack-bot/main.go`

**What the handler does:**
1. Monitors configured Slack channels (e.g., `#opp-discussion`)
2. Detects Prow CI job URLs in messages
3. Calls `analyzer.AnalyzeFailure()` asynchronously
4. Posts analysis results in a thread

### 2. Initialization in cmd/slack-bot/main.go

**Already implemented in this PR:**

```go
// Command-line flags (added)
--enable-triage          // Enable Chaibot
--triage-config-path     // Path to triage-config.yaml

// Initialization (added to main())
if o.enableTriage && o.triageConfigPath != "" {
    mcpURL := os.Getenv("SHIP_HELP_MCP_URL")
    mcpToken := os.Getenv("SHIP_HELP_MCP_TOKEN")
    
    // Create analyzer
    chaibotAnalyzer = chaibot.NewAnalyzer(mcpURL, mcpToken, promptTemplate)
    
    // Handler is registered in router.ForEvents()
}
```

### 3. Event Router Registration

**Already implemented in pkg/slack/events/router/router.go:**

```go
func ForEvents(client *slack.Client, chaibotAnalyzer *chaibot.Analyzer, chaibotChannels []string, ...) {
    // ... existing handlers ...
    
    if chaibotAnalyzer != nil && len(chaibotChannels) > 0 {
        handlers = append(handlers, chaibothandler.Handler(client, chaibotAnalyzer, chaibotChannels))
    }
}
```

## NOT in This PR (Requires openshift/release Configuration)

The following configuration files are in **openshift/release#80559**, not this PR:

- `core-services/ci-chat-bot/triage-config.yaml` - Chaibot configuration
- `clusters/app.ci/ci-chat-bot/chaibot-configmap.yaml` - Kubernetes ConfigMap
- `clusters/app.ci/ci-chat-bot/ci-chat-bot.yaml` - Deployment with environment variables
- `core-services/ci-secret-bootstrap/chaibot-secret-config.yaml` - Ship-help token secret

## Usage

Once both PRs are merged and deployed:

1. **Post a Prow URL in a monitored channel:**
   ```
   Job failed: https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-stolostron-policy-collection-main-ocp4.22-interop-opp-aws/2066255424226594816
   ```

2. **Chaibot responds in a thread within 30-60 seconds** with:
   - Which step(s) failed
   - Root cause analysis (product bug, test issue, or infrastructure)
   - Related Jira tickets
   - Pass rate history
   - Recommended fixes

## Configuration

**Deployment configuration is in openshift/release#80559:**

- `core-services/ci-chat-bot/triage-config.yaml` - Main config:
  - Monitored channels (e.g., `#opp-discussion`)
  - Ship-help MCP endpoint
  - Analysis prompt template
  - Rate limiting settings

- `clusters/app.ci/ci-chat-bot/ci-chat-bot.yaml` - Deployment:
  - Environment variables: `SHIP_HELP_MCP_URL`, `SHIP_HELP_MCP_TOKEN`
  - ConfigMap mount: `/etc/triage-config/triage-config.yaml`

## How to Enable Chaibot

Chaibot is enabled via **command-line flags** (not environment variables):

**Command-line flags (required):**
- `--enable-triage` - Enable Chaibot functionality
- `--triage-config-path=/etc/triage-config/triage-config.yaml` - Path to config file

**Environment variables (required):**
- `SHIP_HELP_MCP_URL` - Ship-help MCP endpoint
- `SHIP_HELP_MCP_TOKEN` - Authentication token (from Kubernetes secret)

**Example deployment command:**
```yaml
# In clusters/app.ci/ci-chat-bot/ci-chat-bot.yaml
args:
  - --enable-triage
  - --triage-config-path=/etc/triage-config/triage-config.yaml
env:
  - name: SHIP_HELP_MCP_URL
    value: "https://ship-help-mcp-continuous-release-tooling--ship-help-bot.apps.gpc.ocp-hub.prod.psi.redhat.com/personas/ocp_ai_helpdesk/mcp"
  - name: SHIP_HELP_MCP_TOKEN
    valueFrom:
      secretKeyRef:
        name: cluster-secrets-chaibot-ship-help
        key: ship-help-token
```

**Without these flags, Chaibot will NOT activate** - even if environment variables are set.

## Related PRs

- **This PR (openshift/ci-tools#5251)** - Chaibot implementation (analyzer, handler, router, main.go)
- **openshift/release#80559** - Configuration and deployment (config files, secrets, ConfigMaps)
- Based on `/analyze-failure` skill by MPEX Integrity team
- Alternative to PR openshift/release#80476 (OpenAI approach)

## Architecture

```
User posts Prow URL in Slack
         ↓
Slack Event API → ci-chat-bot deployment
         ↓
pkg/slack/events/chaibot/handler.go
    - Detects Prow URL
    - Extracts job URL
         ↓
pkg/chaibot/analyzer.go
    - Calls ship-help MCP (ask_persona tool)
    - Sends prompt with job URL
         ↓
Ship-Help MCP (ocp_ai_helpdesk persona)
    - Searches Jira, Sippy, Prow logs
    - Analyzes failure
    - Returns comprehensive analysis
         ↓
pkg/chaibot/analyzer.go
    - Formats response as Slack Block Kit
         ↓
Slack API
    - Posts analysis in thread
```

## Cost Comparison

| Solution | Cost | Data Sources |
|----------|------|--------------|
| **Chaibot (ship-help MCP)** | $0/month | 9+ sources (Jira, Sippy, Prow, GitHub, etc.) |
| OpenAI GPT-4o (PR #80476) | ~$1,080/year | 3 sources (limited context) |

**Chaibot uses internal Red Hat infrastructure** - no external API costs.
