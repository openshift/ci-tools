package sentry

import (
	"encoding/json"
	"sync"
)

var globalBreadcrumbs = NewBreadcrumbsList(10)

func init() {
	AddDefaultOptions(Breadcrumbs(DefaultBreadcrumbs()))
}

// DefaultBreadcrumbs are registered for inclusion in situations where
// you have not specified your own Breadcrumbs collection. You can use
// them to keep track of global events throughout your application.
func DefaultBreadcrumbs() BreadcrumbsList {
	return globalBreadcrumbs
}

// Breadcrumbs can be included in your events to help track down
// the sequence of steps that resulted in a failure.
func Breadcrumbs(list BreadcrumbsList) Option {
	if opt, ok := list.(Option); ok {
		return opt
	}

	return nil
}

// A BreadcrumbsList is responsible for keeping track of all the
// breadcrumbs that make up a sequence. It will automatically remove
// old breadcrumbs as new ones are added and is both type-safe and
// O(1) execution time for inserts and removals.
type BreadcrumbsList interface {
	// Adjusts the maximum number of breadcrumbs which will be maintained
	// in this list.
	WithSize(length int) BreadcrumbsList

	// NewDefault creates a new breadcrumb using the `default` type.
	// You can provide any data you wish to include in the breadcrumb,
	// or nil if you do not wish to include any extra data.
	NewDefault(data map[string]interface{}) Breadcrumb

	// NewNavigation creates a new navigation breadcrumb which represents
	// a transition from one page to another.
	NewNavigation(from, to string) Breadcrumb

	// NewHTTPRequest creates a new HTTP request breadcrumb which
	// describes the results of an HTTP request.
	NewHTTPRequest(method, url string, statusCode int, reason string) Breadcrumb
}

// NewBreadcrumbsList will create a new BreadcrumbsList which can be
// used to track breadcrumbs within a specific context.
func NewBreadcrumbsList(size int) BreadcrumbsList {
	return &breadcrumbsList{
		MaxLength: size,
		Length:    0,
	}
}

type breadcrumbsList struct {
	MaxLength int

	Head   *breadcrumbListNode
	Tail   *breadcrumbListNode
	Length int
	mutex  sync.Mutex
}

func (l *breadcrumbsList) Class() string {
	return "breadcrumbs"
}

func (l *breadcrumbsList) WithSize(length int) BreadcrumbsList {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.MaxLength = length

	for l.Length > l.MaxLength {
		if l.Head == nil {
			break
		}
		l.Head = l.Head.Next
		l.Length--
	}

	return l
}

func (l *breadcrumbsList) NewDefault(data map[string]interface{}) Breadcrumb {
	if data == nil {
		data = map[string]interface{}{}
	}

	b := newBreadcrumb("default", data)
	l.append(b)
	return b
}

func (l *breadcrumbsList) NewNavigation(from, to string) Breadcrumb {
	b := newBreadcrumb("navigation", map[string]interface{}{
		"from": from,
		"to":   to,
	})
	l.append(b)
	return b
}

func (l *breadcrumbsList) NewHTTPRequest(method, url string, statusCode int, reason string) Breadcrumb {
	b := newBreadcrumb("http", map[string]interface{}{
		"method":      method,
		"url":         url,
		"status_code": statusCode,
		"reason":      reason,
	})
	l.append(b)
	return b
}

func (l *breadcrumbsList) MarshalJSON() ([]byte, error) {
	return json.Marshal(l.list())
}

func (l *breadcrumbsList) append(b Breadcrumb) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	// If we've disabled the breadcrumbs collector, skip
	// any extra work.
	if l.MaxLength == 0 {
		return
	}

	n := &breadcrumbListNode{
		Value: b,
		Next:  nil,
	}

	if l.Tail != nil {
		l.Tail.Next = n
	} else {
		l.Head = n
	}
	l.Tail = n
	l.Length++

	for l.Length > l.MaxLength {
		if l.Head == nil {
			break
		}
		l.Head = l.Head.Next
		l.Length--
	}
}

func (l *breadcrumbsList) list() []Breadcrumb {
	current := l.Head
	out := []Breadcrumb{}
	for current != nil {
		out = append(out, current.Value)
		current = current.Next
	}

	return out
}

type breadcrumbListNode struct {
	Next  *breadcrumbListNode
	Value Breadcrumb
}
