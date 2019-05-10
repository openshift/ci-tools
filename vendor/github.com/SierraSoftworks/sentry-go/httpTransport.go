package sentry

import (
	"bytes"
	"compress/zlib"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	log "github.com/sirupsen/logrus"
	"github.com/certifi/gocertifi"
	"github.com/pkg/errors"
)

const (
	// ErrMissingRootTLSCerts is used when this library cannot load the required
	// RootCA certificates needed for its HTTPS transport.
	ErrMissingRootTLSCerts = ErrType("sentry: Failed to load root TLS certificates")
)

type httpTransport struct {
	client *http.Client
}

func newHTTPTransport() Transport {
	t := &httpTransport{
		client: http.DefaultClient,
	}

	rootCAs, err := gocertifi.CACerts()
	if err != nil {
		log.WithError(errors.Wrap(err, ErrMissingRootTLSCerts.Error())).Error(ErrMissingRootTLSCerts.Error())
		return t
	}

	t.client = &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{RootCAs: rootCAs},
		},
	}

	return t
}

func (t *httpTransport) Send(dsn string, packet Packet) error {
	if dsn == "" {
		return nil
	}

	url, authHeader, err := t.parseDSN(dsn)
	if err != nil {
		return errors.Wrap(err, "failed to parse DSN")
	}

	body, contentType, err := t.serializePacket(packet)
	if err != nil {
		return errors.Wrap(err, "failed to serialize packet")
	}

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return errors.Wrap(err, "failed to create new request")
	}

	req.Header.Set("X-Sentry-Auth", authHeader)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", fmt.Sprintf("sentry-go %s (Sierra Softworks; github.com/SierraSoftworks/sentry-go)", version))

	log.WithFields(log.Fields{
		"method": req.Method,
		"url":    req.URL.String(),
	}).Debug("sentry: Making request to send event")

	res, err := t.client.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to submit request")
	}

	io.Copy(ioutil.Discard, res.Body)
	res.Body.Close()

	if res.StatusCode != 200 {
		return errors.Errorf("got http status %d, expected 200", res.StatusCode)
	}

	return nil
}

func (t *httpTransport) parseDSN(dsn string) (url, authHeader string, err error) {
	d, err := newDSN(dsn)
	if err != nil {
		return "", "", err
	}

	return d.URL, d.AuthHeader(), nil
}

func (t *httpTransport) serializePacket(packet Packet) (io.Reader, string, error) {
	buf := bytes.NewBuffer([]byte{})
	if err := json.NewEncoder(buf).Encode(packet); err != nil {
		return nil, "", errors.Wrap(err, "failed to encode JSON payload data")
	}

	if buf.Len() < 1000 {
		return buf, "application/json; charset=utf8", nil
	}

	cbuf := bytes.NewBuffer([]byte{})
	b64 := base64.NewEncoder(base64.StdEncoding, cbuf)
	deflate, err := zlib.NewWriterLevel(b64, zlib.BestCompression)
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to configure zlib deflate")
	}

	if _, err := io.Copy(deflate, buf); err != nil {
		return nil, "", errors.Wrap(err, "failed to deflate message")
	}

	deflate.Close()
	b64.Close()

	return cbuf, "application/octet-stream", nil
}
