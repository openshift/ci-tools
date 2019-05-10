package sentry

import "encoding/json"

// Fingerprint is used to configure the array of strings used to deduplicate
// events when they are processed by Sentry.
// You may use the special value "{{ default }}" to extend the default behaviour
// if you wish.
// https://docs.sentry.io/learn/rollups/#custom-grouping
func Fingerprint(keys ...string) Option {
	return &fingerprintOption{keys}
}

type fingerprintOption struct {
	keys []string
}

func (o *fingerprintOption) Class() string {
	return "fingerprint"
}

func (o *fingerprintOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.keys)
}
