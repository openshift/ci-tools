package sentry

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/pkg/errors"
)

func init() {
	AddDefaultOptions(DSN(os.Getenv("SENTRY_DSN")))
}

// DSN lets you specify the unique Sentry DSN used to submit events for
// your application. Specifying an empty DSN will disable the client.
func DSN(dsn string) Option {
	return &dsnOption{dsn}
}

type dsnOption struct {
	dsn string
}

func (o *dsnOption) Class() string {
	return "sentry-go.dsn"
}

func (o *dsnOption) Omit() bool {
	return true
}

const (
	// ErrBadURL is returned when a DSN cannot be parsed due to
	// formatting errors in its URL
	ErrBadURL = ErrType("sentry: bad DSN URL")

	// ErrMissingPublicKey is returned when a DSN does not have
	// a valid public key contained within its URL
	ErrMissingPublicKey = ErrType("sentry: missing public key")

	// ErrMissingPrivateKey is returned when a DSN does not have
	// a valid private key contained within its URL
	// [DEPRECATED] error is never thrown since Sentry 9 has deprecated the secret key requirement
	ErrMissingPrivateKey = ErrType("sentry: missing private key")

	// ErrMissingProjectID is returned when a DSN does not have a valid
	// project ID contained within its URL
	ErrMissingProjectID = ErrType("sentry: missing project ID")
)

type dsn struct {
	URL        string
	PublicKey  string
	PrivateKey string
	ProjectID  string
}

func newDSN(url string) (*dsn, error) {
	d := &dsn{}
	if err := d.Parse(url); err != nil {
		return nil, err
	}

	return d, nil
}

func (d *dsn) AuthHeader() string {
	if d.PublicKey == "" {
		return ""
	}

	if d.PrivateKey == "" {
		return fmt.Sprintf("Sentry sentry_version=4, sentry_key=%s", d.PublicKey)
	}

	return fmt.Sprintf("Sentry sentry_version=4, sentry_key=%s, sentry_secret=%s", d.PublicKey, d.PrivateKey)
}

func (d *dsn) Parse(dsn string) error {
	if dsn == "" {
		return nil
	}

	uri, err := url.Parse(dsn)
	if err != nil {
		return errors.Wrap(err, ErrBadURL.Error())
	}

	if uri.User == nil {
		return errors.Wrap(fmt.Errorf("missing URL user"), ErrMissingPublicKey.Error())
	}

	d.PublicKey = uri.User.Username()

	privateKey, ok := uri.User.Password()
	if ok {
		d.PrivateKey = privateKey
	}

	uri.User = nil

	if idx := strings.LastIndex(uri.Path, "/"); idx != -1 {
		d.ProjectID = uri.Path[idx+1:]
		uri.Path = fmt.Sprintf("%s/", path.Join(uri.Path[:idx+1], "api", d.ProjectID, "store"))
	}

	if d.ProjectID == "" {
		return errors.Wrap(fmt.Errorf("missing Project ID"), ErrMissingProjectID.Error())
	}

	d.URL = uri.String()

	return nil
}
