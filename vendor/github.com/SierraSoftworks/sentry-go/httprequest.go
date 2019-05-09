package sentry

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// An HTTPRequestOption describes an HTTP request's data
type HTTPRequestOption interface {
	Option
	WithCookies() HTTPRequestOption
	WithHeaders() HTTPRequestOption
	WithEnv() HTTPRequestOption
	WithData(data interface{}) HTTPRequestOption
	Sanitize(fields ...string) HTTPRequestOption
}

// HTTPRequest passes the request context from a net/http
// request object to Sentry. It exposes a number of options
// to control what information is exposed and how it is
// sanitized.
func HTTPRequest(req *http.Request) HTTPRequestOption {
	return &httpRequestOption{
		request: req,
		sanitize: []string{
			"password",
			"passwd",
			"passphrase",
			"secret",
		},
	}
}

type httpRequestOption struct {
	request     *http.Request
	withCookies bool
	withHeaders bool
	withEnv     bool
	data        interface{}
	sanitize    []string
}

func (h *httpRequestOption) Class() string {
	return "request"
}

func (h *httpRequestOption) WithCookies() HTTPRequestOption {
	h.withCookies = true
	return h
}

func (h *httpRequestOption) WithHeaders() HTTPRequestOption {
	h.withHeaders = true
	return h
}

func (h *httpRequestOption) WithEnv() HTTPRequestOption {
	h.withEnv = true
	return h
}

func (h *httpRequestOption) WithData(data interface{}) HTTPRequestOption {
	h.data = data
	return h
}

func (h *httpRequestOption) Sanitize(fields ...string) HTTPRequestOption {
	h.sanitize = append(h.sanitize, fields...)
	return h
}

func (h *httpRequestOption) Omit() bool {
	return h.request == nil
}

func (h *httpRequestOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(h.buildData())
}

func (h *httpRequestOption) buildData() *HTTPRequestInfo {
	proto := "http"
	if h.request.TLS != nil || h.request.Header.Get("X-Forwarded-Proto") == "https" {
		proto = "https"
	}
	p := &HTTPRequestInfo{
		Method:  h.request.Method,
		Query:   sanitizeQuery(h.request.URL.Query(), h.sanitize).Encode(),
		URL:     proto + "://" + h.request.Host + h.request.URL.Path,
		Headers: make(map[string]string, 0),
		Env:     make(map[string]string, 0),
		Data:    h.data,
	}

	if h.withCookies {
		p.Cookies = h.request.Header.Get("Cookie")
	}

	if h.withEnv {
		p.Env = make(map[string]string, 0)

		if addr, port, err := net.SplitHostPort(h.request.RemoteAddr); err == nil {
			p.Env["REMOTE_ADDR"] = addr
			p.Env["REMOTE_PORT"] = port
		}

		for _, env := range os.Environ() {
			ps := strings.SplitN(env, "=", 2)
			k := ps[0]
			v := ""
			if len(ps) > 1 {
				v = ps[1]
			}

			for _, keyword := range h.sanitize {
				if strings.Contains(k, keyword) {
					v = "********"
				}
			}

			p.Env[k] = v
		}
	}

	if h.withHeaders {
		p.Headers = make(map[string]string, len(h.request.Header))
		for k, v := range h.request.Header {
			p.Headers[k] = strings.Join(v, ",")

			for _, keyword := range h.sanitize {
				if strings.Contains(k, keyword) {
					p.Headers[k] = "********"
					break
				}
			}
		}
	}
	return p
}

func sanitizeQuery(query url.Values, fields []string) url.Values {
	for field := range query {
		for _, keyword := range fields {
			if strings.Contains(field, keyword) {
				query[field] = []string{"********"}
				break
			}
		}
	}
	return query
}
