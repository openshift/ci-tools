package sentry

import (
	"encoding/json"
	"time"
)

func init() {
	AddDefaultOptionProvider(func() Option {
		return Timestamp(time.Now().UTC())
	})
}

// Timestamp allows you to provide a custom timestamp for an event
// that is sent to Sentry.
func Timestamp(timestamp time.Time) Option {
	return &timestampOption{timestamp}
}

type timestampOption struct {
	timestamp time.Time
}

func (o *timestampOption) Class() string {
	return "timestamp"
}

func (o *timestampOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.timestamp.Format("2006-01-02T15:04:05"))
}
