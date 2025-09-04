package provider

import "net/http"

// Provider is an interface for getting the right request and response for the AI service.
type Provider interface {
	GetRequest(url string, token string, text string, diff []byte) (*http.Request, error)
	GetResponse(resp *http.Response) (string, error)
}
