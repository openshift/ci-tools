package bitwarden

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"

	"github.com/sirupsen/logrus"
)

type cliClient struct {
	username string
	password string
	sync.Mutex
	session    string
	savedItems []Item
	run        func(args ...string) ([]byte, error)
}

func newCliClient(username, password string) (Client, error) {
	return newCliClientWithRun(username, password, func(args ...string) ([]byte, error) {
		// bw-password is protected, session in args is not
		logrus.WithField("args", args).Info("running bw command ...")
		out, err := exec.Command("bw", args...).CombinedOutput()
		if err != nil {
			logrus.WithError(err).Errorf("bw cmd failed: %v", string(out))
		}
		return out, err
	})
}

func newCliClientWithRun(username, password string, run func(args ...string) (bytes []byte, err error)) (Client, error) {
	client := cliClient{
		username: username,
		password: password,
		run:      run,
	}
	return &client, client.loginAndListItems()
}

type bwLoginResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Raw string `json:"raw"`
	} `json:"data"`
}

func (c *cliClient) loginAndListItems() error {
	c.Lock()
	defer c.Unlock()
	output, err := c.run("login", c.username, c.password, "--response")
	if err != nil {
		return err
	}
	r := bwLoginResponse{}
	if err := json.Unmarshal(output, &r); err != nil {
		return err
	}
	if r.Success {
		raw := r.Data.Raw
		if raw != "" {
			c.session = raw
			var items []Item
			out, err := c.run("--session", c.session, "list", "items")
			if err != nil {
				return err
			}
			err = json.Unmarshal(out, &items)
			if err != nil {
				return err
			}
			c.savedItems = items
			return nil
		}
		// should never happen
		return fmt.Errorf("bw-login succeeded with empty '.data.raw'")
	}
	// should never happen
	return fmt.Errorf("bw-login failed without run's error")
}

func (c *cliClient) GetFieldOnItem(itemName, fieldName string) ([]byte, error) {
	for _, item := range c.savedItems {
		if itemName == item.Name {
			for _, field := range item.Fields {
				if field.Name == fieldName {
					return []byte(field.Value), nil
				}
			}
		}
	}
	return nil, fmt.Errorf("failed to find field %s in item %s", fieldName, itemName)
}

func (c *cliClient) GetAttachmentOnItem(itemName, attachmentName string) ([]byte, error) {
	for _, item := range c.savedItems {
		if itemName == item.Name {
			for _, attachment := range item.Attachments {
				if attachment.FileName == attachmentName {
					return c.run("--session", c.session, "get", "attachment", attachment.ID, "--itemid", item.ID, "--raw")
				}
			}
		}
	}
	return nil, fmt.Errorf("failed to find attachment %s in item %s", attachmentName, itemName)
}

func (c *cliClient) Logout() ([]byte, error) {
	return c.run("logout")
}
