package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	jiraapi "github.com/andygrunwald/go-jira"
)

const (
	jiraSearchMaxPages    = 500
	jiraSearchPageSizeCap = 100
	jiraErrBodyRunes      = 512
	jiraErrBodyBytesCap   = 16 * 1024
)

type jiraSearchJQLV3Response struct {
	IsLast        bool            `json:"isLast"`
	Issues        []jiraapi.Issue `json:"issues"`
	NextPageToken string          `json:"nextPageToken"`
}

func searchIssuesByJQL(ctx context.Context, client *jiraapi.Client, jql string, maxPerPage int, fields []string) ([]jiraapi.Issue, error) {
	return searchIssuesByJQLPageLimit(ctx, client, jql, maxPerPage, fields, jiraSearchMaxPages)
}

func searchIssuesByJQLPageLimit(ctx context.Context, client *jiraapi.Client, jql string, maxPerPage int, fields []string, pageLimit int) ([]jiraapi.Issue, error) {
	if pageLimit < 1 {
		pageLimit = 1
	}
	jql = strings.TrimSpace(jql)
	if jql == "" {
		return nil, fmt.Errorf("jira search/jql: empty jql")
	}
	if client == nil {
		return nil, fmt.Errorf("jira search/jql: nil client")
	}
	switch {
	case maxPerPage <= 0:
		maxPerPage = jiraSearchPageSizeCap
	case maxPerPage > jiraSearchPageSizeCap:
		maxPerPage = jiraSearchPageSizeCap
	}

	fieldsParam := ""
	if len(fields) > 0 {
		fieldsParam = strings.Join(fields, ",")
	}

	out := make([]jiraapi.Issue, 0, pageLimit*maxPerPage)
	var nextPageToken string

	for page := 0; page < pageLimit; page++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("jira search/jql: %w", err)
		}

		sentToken := nextPageToken
		uv := url.Values{}
		uv.Set("jql", jql)
		uv.Set("maxResults", strconv.Itoa(maxPerPage))
		if fieldsParam != "" {
			uv.Set("fields", fieldsParam)
		}
		if nextPageToken != "" {
			uv.Set("nextPageToken", nextPageToken)
		}

		req, err := client.NewRequestWithContext(ctx, http.MethodGet, "rest/api/3/search/jql?"+uv.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("jira search/jql: build request: %w", err)
		}

		var payload jiraSearchJQLV3Response
		resp, doErr := client.Do(req, &payload)
		if doErr != nil {
			return nil, jiraSearchFormatError(resp, doErr)
		}
		if payload.Issues == nil {
			payload.Issues = []jiraapi.Issue{}
		}
		for i := range payload.Issues {
			iss := &payload.Issues[i]
			if strings.TrimSpace(iss.Key) == "" {
				return nil, fmt.Errorf("jira search/jql: page %d issue[%d] has empty key", page+1, i)
			}
			if iss.Fields == nil {
				return nil, fmt.Errorf("jira search/jql: page %d issue %s has nil fields", page+1, iss.Key)
			}
		}

		out = append(out, payload.Issues...)

		if payload.IsLast {
			return out, nil
		}
		if payload.NextPageToken == "" {
			return nil, fmt.Errorf("jira search/jql: page %d not isLast but nextPageToken empty", page+1)
		}
		if sentToken != "" && payload.NextPageToken == sentToken {
			return nil, fmt.Errorf("jira search/jql: page %d nextPageToken equals request token (server loop)", page+1)
		}
		nextPageToken = payload.NextPageToken
	}

	return nil, fmt.Errorf("jira search/jql: exceeded %d pages", pageLimit)
}

func isDecodeError(err error) bool {
	var syn *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syn) || errors.As(err, &typeErr) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "invalid character") && strings.Contains(msg, "literal")
}

func jiraSearchFormatError(resp *jiraapi.Response, err error) error {
	if err == nil {
		return nil
	}
	if resp == nil || resp.Response == nil {
		return fmt.Errorf("jira search/jql: %w", err)
	}
	code := resp.StatusCode
	if code == 0 {
		code = http.StatusInternalServerError
	}

	if isDecodeError(err) {
		return fmt.Errorf("jira search/jql: HTTP %d: decode response: %w", code, err)
	}

	if resp.Body == nil {
		if code >= 200 && code <= 299 {
			return fmt.Errorf("jira search/jql: HTTP %d: decode response: %w", code, err)
		}
		return fmt.Errorf("jira search/jql: HTTP %d: (nil response body)", code)
	}
	if code >= 200 && code <= 299 {
		return fmt.Errorf("jira search/jql: HTTP %d: decode response: %w", code, err)
	}

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, jiraErrBodyBytesCap))
	closeErr := resp.Body.Close()
	if readErr != nil {
		if closeErr != nil {
			return fmt.Errorf("jira search/jql: HTTP %d (read body: %s; close: %s): %w", code, readErr.Error(), closeErr.Error(), err)
		}
		return fmt.Errorf("jira search/jql: HTTP %d (body read failed: %s): %w", code, readErr.Error(), err)
	}
	if closeErr != nil {
		return fmt.Errorf("jira search/jql: HTTP %d (close body: %s): %w", code, closeErr.Error(), err)
	}
	return fmt.Errorf("jira search/jql: HTTP %d: %s", code, truncateJiraErrBody(raw))
}

func truncateJiraErrBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "(empty body)"
	}
	if utf8.RuneCountInString(s) <= jiraErrBodyRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:jiraErrBodyRunes]) + "…"
}
