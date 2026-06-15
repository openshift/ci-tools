// Package chaibot provides test failure analysis using Chai Bot (ship-help MCP)
// This is the implementation code that goes in openshift/ci-tools/pkg/chaibot/

package chaibot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Analyzer provides test failure analysis using ship-help MCP
type Analyzer struct {
	mcpURL   string
	token    string
	client   *http.Client
	template string
}

// NewAnalyzer creates a new Analyzer instance
func NewAnalyzer(mcpURL, token, promptTemplate string) *Analyzer {
	return &Analyzer{
		mcpURL:   mcpURL,
		token:    token,
		template: promptTemplate,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// MCPRequest represents an MCP JSON-RPC request
type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// ToolCallParams represents parameters for calling an MCP tool
type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// MCPResponse represents an MCP JSON-RPC response
type MCPResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// AnalysisResult contains the result of a failure analysis
type AnalysisResult struct {
	JobURL   string
	Analysis string
	Duration time.Duration
	Error    error
}

// AnalyzeFailure analyzes a Prow CI job failure using Chai Bot
func (a *Analyzer) AnalyzeFailure(ctx context.Context, jobURL string) (*AnalysisResult, error) {
	startTime := time.Now()

	// Build prompt using the configured template
	prompt := strings.ReplaceAll(a.template, "{job_url}", jobURL)

	// Call ship-help MCP ask_persona tool
	params := ToolCallParams{
		Name: "ask_persona",
		Arguments: map[string]interface{}{
			"question": prompt,
		},
	}

	reqBody := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  params,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.mcpURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Check HTTP status code before parsing JSON
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Parse MCP response
	var mcpResp MCPResponse
	if err := json.Unmarshal(body, &mcpResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// Check for errors
	if mcpResp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	// Extract analysis text
	if len(mcpResp.Result.Content) == 0 {
		return nil, fmt.Errorf("no content in response")
	}

	analysis := mcpResp.Result.Content[0].Text

	return &AnalysisResult{
		JobURL:   jobURL,
		Analysis: analysis,
		Duration: time.Since(startTime),
	}, nil
}

// ExtractProwURL extracts a Prow job URL from a message
func ExtractProwURL(text string) string {
	// Look for Prow URLs
	patterns := []string{
		"https://prow.ci.openshift.org/view/gs/",
		"https://prow.ci.openshift.org/?pr=",
		"https://deck-internal-ci.apps.ci.l2s4.p1.openshiftapps.com/",
	}

	for _, pattern := range patterns {
		if idx := strings.Index(text, pattern); idx != -1 {
			// Extract URL until whitespace
			urlStart := idx
			urlEnd := urlStart
			for urlEnd < len(text) && !isWhitespace(text[urlEnd]) {
				urlEnd++
			}

			// Trim trailing punctuation (handles cases like "https://...)" or "<https://...>")
			url := text[urlStart:urlEnd]
			url = strings.TrimRight(url, ")>]}.,;:")

			return url
		}
	}

	return ""
}

func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// ContainsProwURL checks if a message contains a Prow URL
func ContainsProwURL(text string) bool {
	return ExtractProwURL(text) != ""
}

// FormatSlackResponse formats the analysis for Slack using Block Kit
// Returns a simple text message since we're posting in a thread
func FormatSlackResponse(result *AnalysisResult) string {
	// Guard against nil result to prevent panic
	if result == nil {
		return "❌ Error: Unable to format analysis (nil result)"
	}

	// Format as markdown text for thread reply
	return fmt.Sprintf("🔍 *Chaibot Analysis*\n\n%s\n\n_Analysis completed in %.1fs • Powered by Chai Bot_",
		result.Analysis,
		result.Duration.Seconds(),
	)
}
