# Chaibot - Ship-Help MCP Integration

This package provides test failure analysis using Chai Bot (ship-help MCP) for automatic Slack triage.

## Overview

Chaibot monitors Slack channels for Prow CI job failure URLs and automatically posts analysis using the Chai Bot service via ship-help MCP.

## Files

- `analyzer.go` - Ship-help MCP client implementation

## Integration with slack-bot

To enable Chaibot in the slack-bot command, add the following to `cmd/slack-bot/main.go`:

### 1. Add imports

```go
import (
    // ... existing imports ...
    "github.com/openshift/ci-tools/pkg/chaibot"
    "gopkg.in/yaml.v2"
)
```

### 2. Add command-line flags

```go
type options struct {
    // ... existing fields ...
    
    enableTriage      bool
    triageConfigPath  string
}

func (o *options) Bind(fs *flag.FlagSet) {
    // ... existing bindings ...
    
    fs.BoolVar(&o.enableTriage, "enable-triage", false, "Enable automatic test failure triage")
    fs.StringVar(&o.triageConfigPath, "triage-config-path", "", "Path to triage configuration file")
}
```

### 3. Add Chaibot initialization in main()

```go
func main() {
    // ... existing setup ...
    
    if o.enableTriage {
        mcpURL := os.Getenv("SHIP_HELP_MCP_URL")
        mcpToken := os.Getenv("SHIP_HELP_MCP_TOKEN")
        
        if mcpURL != "" && mcpToken != "" {
            // Load triage config
            configData, err := os.ReadFile(o.triageConfigPath)
            if err != nil {
                logrus.WithError(err).Fatal("Failed to load triage config")
            }
            
            var triageConfig TriageConfig
            if err := yaml.Unmarshal(configData, &triageConfig); err != nil {
                logrus.WithError(err).Fatal("Failed to parse triage config")
            }
            
            // Create analyzer
            analyzer := chaibot.NewAnalyzer(mcpURL, mcpToken, triageConfig.Analysis.PromptTemplate)
            
            logrus.WithFields(logrus.Fields{
                "channels": len(triageConfig.MonitoredChannels),
                "provider": triageConfig.Analysis.AIProvider,
            }).Info("Chaibot triage enabled")
            
            // Start monitoring (add as event handler)
            go monitorForFailures(slackClient, analyzer, &triageConfig)
        } else {
            logrus.Warn("Chaibot enabled but SHIP_HELP_MCP_URL or SHIP_HELP_MCP_TOKEN not set")
        }
    }
    
    // ... rest of main ...
}
```

### 4. Add monitoring function

```go
type TriageConfig struct {
    Enabled           bool                 `yaml:"enabled"`
    MonitoredChannels []MonitoredChannel   `yaml:"monitored_channels"`
    Analysis          AnalysisConfig       `yaml:"analysis"`
}

type MonitoredChannel struct {
    Name      string `yaml:"name"`
    ChannelID string `yaml:"channel_id"`
}

type AnalysisConfig struct {
    Timeout        int    `yaml:"timeout"`
    AIProvider     string `yaml:"ai_provider"`
    PromptTemplate string `yaml:"prompt_template"`
}

func monitorForFailures(client *slack.Client, analyzer *chaibot.Analyzer, config *TriageConfig) {
    // Create channel ID map
    monitoredChannels := make(map[string]bool)
    for _, ch := range config.MonitoredChannels {
        monitoredChannels[ch.ChannelID] = true
    }
    
    // This would integrate with existing event handling
    // For now, this is a placeholder showing the pattern
    logrus.Info("Chaibot monitoring started")
}
```

## Alternative: Event Handler Pattern

A cleaner integration would be to add Chaibot as an event handler in the existing event routing system:

1. Create `pkg/slack/events/chaibot/handler.go` following the pattern of `pkg/slack/events/helpdesk/`
2. Register it in the event router in `cmd/slack-bot/main.go`
3. The handler would check for Prow URLs and call the analyzer

## Configuration

The triage configuration is mounted at `/etc/triage-config/triage-config.yaml` in the deployment and includes:
- Monitored channel IDs
- Ship-help MCP endpoint
- Analysis prompt template
- Rate limiting settings

See `openshift/release#80559` for the full configuration.

## Environment Variables

- `CHAIBOT_ENABLED` - Set to "true" to enable
- `SHIP_HELP_MCP_URL` - Ship-help MCP endpoint
- `SHIP_HELP_MCP_TOKEN` - Authentication token

## Related PRs

- openshift/release#80559 - Configuration and deployment
- Based on /analyze-failure skill by MPEX Integrity team
- Alternative to PR #80476 (OpenAI approach)
