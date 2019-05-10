package sentry

import "encoding/json"

// Extra allows you to provide additional arbitrary metadata with your
// event. This data is not searchable, but can be invaluable in identifying
// the cause of a problem.
func Extra(extra map[string]interface{}) Option {
	if extra == nil {
		return nil
	}

	return &extraOption{extra}
}

type extraOption struct {
	extra map[string]interface{}
}

func (o *extraOption) Class() string {
	return "extra"
}

func (o *extraOption) Merge(old Option) Option {
	if old, ok := old.(*extraOption); ok {
		extra := make(map[string]interface{}, len(o.extra))
		for k, v := range old.extra {
			extra[k] = v
		}

		for k, v := range o.extra {
			extra[k] = v
		}

		return &extraOption{extra}
	}

	return o
}

func (o *extraOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.extra)
}
