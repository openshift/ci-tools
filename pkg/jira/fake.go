package jira

import (
	"errors"
	"testing"

	"github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
)

// IssueRequest describes a client call to file an issue
type IssueRequest struct {
	IssueType, Title, Description, Reporter string
}

// IssueResponse describes a client response for filing an issue
type IssueResponse struct {
	Issue *jira.Issue
	Error error
}

// Fake is an injectable IssueFiler
type Fake struct {
	behavior map[IssueRequest]IssueResponse
	unwanted []IssueRequest
}

// FileIssue files the issue using injected behavior
func (f *Fake) FileIssue(issueType, title, description, reporter string, logger *logrus.Entry) (*jira.Issue, error) {
	request := IssueRequest{
		IssueType:   issueType,
		Title:       title,
		Description: description,
		Reporter:    reporter,
	}
	response, registered := f.behavior[request]
	if !registered {
		f.unwanted = append(f.unwanted, request)
		return nil, errors.New("no such issue request behavior in fake")
	}
	delete(f.behavior, request)
	return response.Issue, response.Error
}

// Validate ensures that all expected client calls happened
func (f *Fake) Validate(t *testing.T) {
	for request := range f.behavior {
		t.Errorf("fake issue filer did not get request: %v", request)
	}
	for _, request := range f.unwanted {
		t.Errorf("fake issue filer got unwanted request: %v", request)
	}
}

var _ IssueFiler = &Fake{}

// NewFake creates a new fake filer with the injected behavior
func NewFake(calls map[IssueRequest]IssueResponse) *Fake {
	return &Fake{behavior: calls}
}
