package sentry

// HTTPRequestInfo is a low-level interface which describes
// the details of an HTTP request made to a web server.
// If you are using the net/http library, the HTTPRequest()
// option will populate this information for you automatically.
type HTTPRequestInfo struct {
	URL    string `json:"url"`
	Method string `json:"method"`
	Query  string `json:"query_string,omitempty"`

	// These fields are optional
	Cookies string            `json:"cookies,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Data    interface{}       `json:"data,omitempty"`
}

// Class is used to meet the Option interface constraints by
// providing the name of the API field that this data will be
// submitted in.
func (o *HTTPRequestInfo) Class() string {
	return "request"
}

// HTTP creates a new HTTP interface with the raw data provided
// by a user. It is useful in situations where you are not leveraging
// Go's underlying net/http library or wish to have direct control over
// the values sent to Sentry.
// For all other purposes, the HTTPRequest() option is a more useful
// replacement.
func HTTP(h *HTTPRequestInfo) Option {
	return h
}
