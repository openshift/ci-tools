package api

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// Parameters allows a step to read values set by other steps.
type Parameters interface {
	Has(name string) bool
	HasInput(name string) bool
	Get(name string) (string, error)
}

type overrideParameters struct {
	params    Parameters
	overrides map[string]string
}

func (p *overrideParameters) Has(name string) bool {
	if _, ok := p.overrides[name]; ok {
		return true
	}
	return p.params.Has(name)
}

func (p *overrideParameters) HasInput(name string) bool {
	return p.params.HasInput(name)
}

func (p *overrideParameters) Get(name string) (string, error) {
	if value, ok := p.overrides[name]; ok {
		return value, nil
	}
	return p.params.Get(name)
}

func NewOverrideParameters(params Parameters, overrides map[string]string) Parameters {
	return &overrideParameters{
		params:    params,
		overrides: overrides,
	}
}

type DeferredParameters struct {
	lock   sync.Mutex
	fns    ParameterMap
	values map[string]string
}

func NewDeferredParameters() *DeferredParameters {
	return &DeferredParameters{
		fns:    make(ParameterMap),
		values: make(map[string]string),
	}
}

func (p *DeferredParameters) Map() (map[string]string, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	m := make(map[string]string)
	for k, fn := range p.fns {
		if v, ok := p.values[k]; ok {
			m[k] = v
			continue
		}
		v, err := fn()
		if err != nil {
			return nil, fmt.Errorf("could not lazily evaluate deferred parameter %q: %w", k, err)
		}
		p.values[k] = v
		m[k] = v
	}
	return m, nil
}

func (p *DeferredParameters) Set(name, value string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if _, ok := p.fns[name]; ok {
		log.Printf("warning: ignoring parameter %q, already set", name)
		return
	}
	if _, ok := p.values[name]; ok {
		log.Printf("warning: ignoring parameter %q, already set", name)
		return
	}
	p.values[name] = value
}

func (p *DeferredParameters) Add(name string, fn func() (string, error)) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if _, ok := p.fns[name]; ok {
		log.Printf("warning: overriding parameter %q", name)
	}
	p.fns[name] = fn
}

// HasInput returns true if the named parameter is an input from outside the graph, rather
// than provided either by the graph caller or another node.
func (p *DeferredParameters) HasInput(name string) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	_, ok := os.LookupEnv(name)
	return ok
}

// Has returns true if the named parameter exists. Use HasInput if you need to know whether
// the value comes from outside the graph.
func (p *DeferredParameters) Has(name string) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	_, ok := p.fns[name]
	if ok {
		return true
	}
	_, ok = os.LookupEnv(name)
	return ok
}

func (p *DeferredParameters) Get(name string) (string, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if value, ok := p.values[name]; ok {
		return value, nil
	}
	if value, ok := os.LookupEnv(name); ok {
		p.values[name] = value
		return value, nil
	}
	if fn, ok := p.fns[name]; ok {
		value, err := fn()
		if err != nil {
			return "", fmt.Errorf("could not lazily evaluate deferred parameter %q: %w", name, err)
		}
		p.values[name] = value
		return value, nil
	}
	return "", nil
}
