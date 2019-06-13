package sentry

import "encoding/json"

// Culprit allows you to specify the name of the transaction (or culprit)
// which casued this event.
// For example, in a web app, this might be the route name: `/welcome/`
func Culprit(culprit string) Option {
	return &culpritOption{culprit}
}

type culpritOption struct {
	culprit string
}

func (o *culpritOption) Class() string {
	return "culprit"
}

func (o *culpritOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.culprit)
}
