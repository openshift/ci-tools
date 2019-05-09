package sentry

import "fmt"

type messageOption struct {
	Message   string        `json:"message"`
	Params    []interface{} `json:"params,omitempty"`
	Formatted string        `json:"formatted,omitempty"`
}

func (m *messageOption) Class() string {
	return "sentry.interfaces.Message"
}

// Message generates a new message entry for Sentry, optionally
// using a format string with standard fmt.Sprintf params.
func Message(format string, params ...interface{}) Option {
	if len(params) == 0 {
		return &messageOption{
			Message: format,
		}
	}

	return &messageOption{
		Message:   format,
		Params:    params,
		Formatted: fmt.Sprintf(format, params...),
	}
}
