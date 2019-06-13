package sentry

// An Option represents an object which can be written to the Sentry packet
// as a field with a given class name. Options may implement additional
// interfaces to control how their values are rendered or to offer the
// ability to merge multiple instances together.
type Option interface {
	Class() string
}

// An OmitableOption can opt to have itself left out of the packet by
// making an addition-time determination in its Omit() function.
// This is a useful tool for excluding empty fields automatically.
type OmitableOption interface {
	Omit() bool
}

// The MergeableOption interface gives options the ability to merge themselves
// with other instances posessing the same class name.
// Sometimes it makes sense to offer the ability to merge multiple options
// of the same type together before they are rendered. This interface gives
// options the ability to define how that merging should occur.
type MergeableOption interface {
	Merge(old Option) Option
}

// A FinalizableOption exposes a Finalize() method which is called by the
// Packet builder before its value is used. This gives the option the opportunity
// to perform any last-minute formatting and configuration.
type FinalizableOption interface {
	Finalize()
}

// These defaultOptionProviders are used to populate a packet before it is
// configured by user provided options. Due to the need to generate some
// options dynamically, these are exposed as callbacks.
var defaultOptionProviders = []func() Option{}

// AddDefaultOptions allows you to configure options which will be globally
// set on all top-level clients by default. You can override these options
// later by specifying replacements in each client or event's options list.
func AddDefaultOptions(options ...Option) {
	for _, opt := range options {
		if opt == nil {
			continue
		}

		o := opt
		AddDefaultOptionProvider(func() Option {
			return o
		})
	}
}

// AddDefaultOptionProvider allows you to register a new default option which will
// be globally set on all top-level clients. You can override this option
// later by specifying a replacement in each client or event's options list.
func AddDefaultOptionProvider(provider func() Option) {
	defaultOptionProviders = append(defaultOptionProviders, provider)
}
