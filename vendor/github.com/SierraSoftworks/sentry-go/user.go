package sentry

import "encoding/json"

// UserInfo provides the fields that may be specified to describe
// a unique user of your application. You should specify at least
// an `ID` or `IPAddress`.
type UserInfo struct {
	ID        string
	Email     string
	IPAddress string
	Username  string
	Extra     map[string]string
}

// User allows you to include the details of a user that was interacting
// with your application when the error occurred.
func User(user *UserInfo) Option {
	if user == nil {
		return nil
	}

	o := &userOption{
		fields: map[string]string{},
	}

	for k, v := range user.Extra {
		o.fields[k] = v
	}

	if user.ID != "" {
		o.fields["id"] = user.ID
	}

	if user.Username != "" {
		o.fields["username"] = user.Username
	}

	if user.IPAddress != "" {
		o.fields["ip_address"] = user.IPAddress
	}

	if user.Email != "" {
		o.fields["email"] = user.Email
	}

	return o
}

type userOption struct {
	fields map[string]string
}

func (o *userOption) Class() string {
	return "user"
}

func (o *userOption) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.fields)
}
