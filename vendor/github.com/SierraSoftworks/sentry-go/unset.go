package sentry

// Unset will unset a field on the packet prior to it being sent.
func Unset(field string) Option {
	return &unsetOption{
		className: field,
	}
}

type unsetOption struct {
	className string
}

func (o *unsetOption) Class() string {
	return o.className
}

func (o *unsetOption) MarshalJSON() ([]byte, error) {
	return []byte("null"), nil
}

func (o *unsetOption) Apply(packet map[string]Option) {
	delete(packet, o.className)
}
