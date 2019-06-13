package sentry

// A Config allows you to control how events are sent to Sentry.
// It is usually populated through the standard build pipeline
// through the DSN() and UseTransport() options.
type Config interface {
	DSN() string
	Transport() Transport
	SendQueue() SendQueue
}
