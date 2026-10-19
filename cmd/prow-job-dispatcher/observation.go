package main

import "bytes"

// dispatcherAPITokens keeps the observation credential from acquiring write access on rotation.
type dispatcherAPITokens struct {
	control     func() []byte
	observation func() []byte
}

// controlToken fails closed if separately mounted credentials contain the same value.
func (tokens dispatcherAPITokens) controlToken() []byte {
	value := tokens.control()
	if tokens.observation != nil && bytes.Equal(value, tokens.observation()) {
		return nil
	}
	return value
}
