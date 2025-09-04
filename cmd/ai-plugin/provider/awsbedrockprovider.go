package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type AWSBedrockProvider struct{}

func (p *AWSBedrockProvider) GetRequest(url string, token string, text string, diff []byte) (*http.Request, error) {
	payload := map[string]any{
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"text": text + " Here is the diff: " + string(diff)},
				},
			},
		},
	}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return req, nil
}

func (p *AWSBedrockProvider) GetResponse(resp *http.Response) (string, error) {
	var result struct {
		Output struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode AI response: %w", err)
	}
	if len(result.Output.Message.Content) == 0 {
		return "", fmt.Errorf("AI response is empty")
	}
	return result.Output.Message.Content[len(result.Output.Message.Content)-1].Text, nil
}

func NewAWSBedrockProvider() *AWSBedrockProvider {
	return &AWSBedrockProvider{}
}
