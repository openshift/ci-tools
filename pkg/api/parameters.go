package api

import (
	"fmt"
	"os"
	"sync"
)

type DeferredParameters struct {
	lock   sync.Mutex
	fns    ParameterMap
	values map[string]string
	links  map[string][]StepLink
}

func NewDeferredParameters() *DeferredParameters {
	return &DeferredParameters{
		fns:    make(ParameterMap),
		values: make(map[string]string),
		links:  make(map[string][]StepLink),
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
			return nil, fmt.Errorf("could not lazily evaluate deferred parameter: %v", err)
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
		return
	}
	if _, ok := p.values[name]; ok {
		return
	}
	p.values[name] = value
}

func (p *DeferredParameters) Add(name string, link StepLink, fn func() (string, error)) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.fns[name] = fn
	if link != nil {
		p.links[name] = []StepLink{link}
	}
}

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

func (p *DeferredParameters) Links(name string) []StepLink {
	p.lock.Lock()
	defer p.lock.Unlock()
	if _, ok := os.LookupEnv(name); ok {
		return nil
	}
	return p.links[name]
}

func (p *DeferredParameters) AllLinks() []StepLink {
	p.lock.Lock()
	defer p.lock.Unlock()
	var links []StepLink
	for name, v := range p.links {
		if _, ok := os.LookupEnv(name); ok {
			continue
		}
		links = append(links, v...)
	}
	return links
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
			return "", fmt.Errorf("could not lazily evaluate deferred parameter: %v", err)
		}
		p.values[name] = value
		return value, nil
	}
	return "", nil
}
