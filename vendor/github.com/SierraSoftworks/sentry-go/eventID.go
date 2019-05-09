package sentry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
)

func init() {
	AddDefaultOptionProvider(func() Option {
		id, err := NewEventID()
		if err != nil {
			return nil
		}

		return EventID(id)
	})
}

// NewEventID attempts to generate a new random UUIDv4 event
// ID which can be passed to the EventID() option.
func NewEventID() (string, error) {
	id := make([]byte, 16)
	_, err := io.ReadFull(rand.Reader, id)
	if err != nil {
		return "", err
	}
	id[6] &= 0x0F // clear version
	id[6] |= 0x40 // set version to 4 (random uuid)
	id[8] &= 0x3F // clear variant
	id[8] |= 0x80 // set to IETF variant
	return hex.EncodeToString(id), nil
}

// EventID is an option which controls the UUID used to represent
// an event. The ID should be exactly 32 hexadecimal characters long
// and include no dashes.
// If an invalid ID is passed to this option, it will return nil and
// be ignored by the packet builder.
func EventID(id string) Option {
	if len(id) != 32 {
		return nil
	}

	for _, r := range id {
		if r <= 'f' && r >= 'a' {
			continue
		}

		if r <= '9' && r >= '0' {
			continue
		}

		return nil
	}

	return &eventIDOption{id}
}

func (p packet) getEventID() string {
	if idOpt, ok := p["event_id"]; ok {
		if id, ok := idOpt.(*eventIDOption); ok {
			return id.ID
		}
	}

	return ""
}

type eventIDOption struct {
	ID string
}

func (o *eventIDOption) Class() string {
	return "event_id"
}

func (o *eventIDOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.ID)
}
