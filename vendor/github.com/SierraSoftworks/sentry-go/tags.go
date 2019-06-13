package sentry

import "encoding/json"

// Tags allow you to add additional tagging information to events which
// makes it possible to easily group and query similar events.
func Tags(tags map[string]string) Option {
	if tags == nil {
		return nil
	}

	return &tagsOption{tags}
}

type tagsOption struct {
	tags map[string]string
}

func (o *tagsOption) Class() string {
	return "tags"
}

func (o *tagsOption) Merge(old Option) Option {
	if old, ok := old.(*tagsOption); ok {
		tags := make(map[string]string, len(old.tags))
		for k, v := range old.tags {
			tags[k] = v
		}

		for k, v := range o.tags {
			tags[k] = v
		}

		return &tagsOption{tags}
	}

	return o
}

func (o *tagsOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.tags)
}
