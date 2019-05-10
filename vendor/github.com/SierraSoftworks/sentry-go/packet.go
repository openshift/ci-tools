package sentry

import (
	"bytes"
	"encoding/json"
)

// A Packet is a JSON serializable object that will be sent to
// the Sentry server to describe an event. It provides convinience
// methods for setting options and handling the various types of
// option that can be added.
type Packet interface {
	// SetOptions will set all non-nil options provided, intelligently
	// merging values when supported by an option, or replacing existing
	// values if not.
	SetOptions(options ...Option) Packet

	// Clone will create a copy of this packet which can then be modified
	// independently. In most cases it is a better idea to create a new
	// client with the options you wish to override, however there are
	// situations where this is a cleaner solution.
	Clone() Packet
}

type packet map[string]Option

// NewPacket creates a new packet which will be sent to the Sentry
// server after its various options have been set.
// You will not usually need to create a Packet yourself, instead
// you should use your `Client`'s `Capture()` method.
func NewPacket() Packet {
	return &packet{}
}

func (p packet) Clone() Packet {
	np := packet{}
	for k, v := range p {
		np[k] = v
	}

	return &np
}

func (p packet) SetOptions(options ...Option) Packet {
	for _, opt := range options {
		p.setOption(opt)
	}

	return &p
}

func (p packet) setOption(option Option) {
	if option == nil {
		return
	}

	// If the option implements Omit(), check to see whether
	// it has elected to be omitted.
	if omittable, ok := option.(OmitableOption); ok {
		if omittable.Omit() {
			return
		}
	}

	// If the option implements Finalize(), call it to give the
	// option the chance to prepare itself properly
	if finalizable, ok := option.(FinalizableOption); ok {
		finalizable.Finalize()
	}

	if existing, ok := p[option.Class()]; ok {
		if mergable, ok := option.(MergeableOption); ok {
			p[option.Class()] = mergable.Merge(existing)
		} else {
			p[option.Class()] = option
		}
	} else {
		p[option.Class()] = option
	}
}

func testSerializePacket(p Packet) (interface{}, error) {
	buf := bytes.NewBuffer([]byte{})
	if err := json.NewEncoder(buf).Encode(p); err != nil {
		return nil, err
	}

	var data interface{}
	if err := json.NewDecoder(buf).Decode(&data); err != nil {
		return nil, err
	}

	return data, nil
}
