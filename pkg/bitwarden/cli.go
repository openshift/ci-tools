package bitwarden

import (
	"bytes"
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
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
	addSecret  func(s string)
}

func newCliClient(username, password string, addSecret func(s string)) (Client, error) {
	return newCliClientWithRun(username, password, addSecret, func(args ...string) ([]byte, error) {
		// bw-password is protected, session in args is not
		logrus.WithField("args", args).Debug("running bw command ...")
		out, err := exec.Command("bw", args...).CombinedOutput()
		if err != nil {
			logrus.WithError(err).Errorf("bw cmd failed: %v", string(out))
		}
		return out, err
	})
}

func newCliClientWithRun(username, password string, addSecret func(s string), run func(args ...string) (bytes []byte, err error)) (Client, error) {
	client := cliClient{
		username:  username,
		password:  password,
		run:       run,
		addSecret: addSecret,
	}
	return &client, client.loginAndListItems()
}

type bwLoginResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Raw string `json:"raw"`
	} `json:"data"`
}

func (c *cliClient) runWithSession(args ...string) ([]byte, error) {
	argsList := []string{"--session", c.session}
	argsList = append(argsList, args...)
	return c.run(argsList...)
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
			c.addSecret(c.session)
			var items []Item
			out, err := c.runWithSession("list", "items")
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

func (c *cliClient) GetAttachmentOnItem(itemName, attachmentName string) (bytes []byte, retErr error) {
	file, err := ioutil.TempFile("", "attachmentName")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil {
			retErr = err
		}
	}()
	return c.getAttachmentOnItemToFile(itemName, attachmentName, file.Name())
}

func (c *cliClient) getAttachmentOnItemToFile(itemName, attachmentName, filename string) ([]byte, error) {
	for _, item := range c.savedItems {
		if itemName == item.Name {
			for _, attachment := range item.Attachments {
				if attachment.FileName == attachmentName {
					_, err := c.runWithSession("get", "attachment", attachment.ID, "--itemid", item.ID, "--output", filename)
					if err != nil {
						return nil, err
					}
					return ioutil.ReadFile(filename)
				}
			}
		}
	}
	return nil, fmt.Errorf("failed to find attachment %s in item %s", attachmentName, itemName)
}

func (c *cliClient) GetPassword(itemName string) ([]byte, error) {
	for _, item := range c.savedItems {
		if itemName == item.Name {
			if item.Login != nil {
				return []byte(item.Login.Password), nil
			}
		}
	}
	return nil, fmt.Errorf("failed to find password in item %s", itemName)
}

func (c *cliClient) Logout() ([]byte, error) {
	return c.run("logout")
}

func (c *cliClient) createItem(itemTemplate string, targetItem *Item) error {
	// the bitwarden cli expects the item to be base64 encoded
	encItem := b64.StdEncoding.EncodeToString([]byte(itemTemplate))
	out, err := c.runWithSession("create", "item", encItem)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, targetItem)
}

func (c *cliClient) createAttachment(fileContents []byte, fileName string, itemID string, newAttachment *Attachment) error {
	// Not tested
	tempDir, err := ioutil.TempDir("", "attachment")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for new attachment: %w", err)
	}
	defer os.RemoveAll(tempDir)
	filePath := filepath.Join(tempDir, fileName)
	if err := ioutil.WriteFile(filePath, fileContents, 0644); err != nil {
		return fmt.Errorf("failed to create temporary file for new attachment: %w", err)
	}
	out, err := c.runWithSession("create", "attachment", "--itemid", itemID, "--file", filePath)
	if err != nil {
		return fmt.Errorf("bw create failed: %w", err)
	}
	if err != nil {
		return fmt.Errorf("failed to delete file %s: %w", filePath, err)
	}
	if err = json.Unmarshal(out, newAttachment); err != nil {
		return fmt.Errorf("failed to parse bw output: %w", err)
	}
	return nil
}

func (c *cliClient) createEmptyItem(itemName string, targetItem *Item) error {
	item := Item{
		Type:  1,
		Name:  itemName,
		Login: &Login{},
	}
	itemBytes, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to serialize item: %w", err)
	}
	return c.createItem(string(itemBytes), targetItem)
}

func (c *cliClient) createItemWithPassword(itemName string, password []byte, targetItem *Item) error {
	item := Item{
		Type:  1,
		Name:  itemName,
		Login: &Login{string(password)},
	}
	itemBytes, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to serialize item: %w", err)
	}
	return c.createItem(string(itemBytes), targetItem)
}

func (c *cliClient) editItem(targetItem Item) error {
	targetJSON, err := json.Marshal(targetItem)
	if err != nil {
		return fmt.Errorf("failed to marshal object: %w", err)
	}
	encodedItem := b64.StdEncoding.EncodeToString(targetJSON)
	_, err = c.runWithSession("edit", "item", targetItem.ID, encodedItem)
	if err != nil {
		return err
	}
	return nil
}

func (c *cliClient) deleteAttachment(attachmentID, itemID string) error {
	if _, err := c.runWithSession("delete", "attachment", attachmentID, "--itemid", itemID); err != nil {
		return fmt.Errorf("failed to delete attachment, attachmentID: %s, itemID: %s: %w", attachmentID, itemID, err)
	}
	return nil
}

func (c *cliClient) SetFieldOnItem(itemName, fieldName string, fieldValue []byte) error {
	var targetItem *Item
	var targetField *Field
	for index, item := range c.savedItems {
		if itemName == item.Name {
			targetItem = &c.savedItems[index]
			for fieldIndex, field := range item.Fields {
				if field.Name == fieldName {
					targetField = &c.savedItems[index].Fields[fieldIndex]
					break
				}
			}
			break
		}
	}
	if targetItem == nil {
		newItem := &Item{}
		if err := c.createEmptyItem(itemName, newItem); err != nil {
			return fmt.Errorf("failed to create new	 bw entry: %w", err)
		}
		c.savedItems = append(c.savedItems, *newItem)
		targetItem = &c.savedItems[len(c.savedItems)-1]
	}
	isNewField := false
	if targetField == nil {
		targetItem.Fields = append(targetItem.Fields, Field{fieldName, string(fieldValue)})
		targetField = &targetItem.Fields[len(targetItem.Fields)-1]
		isNewField = true
	}
	if isNewField || targetField.Value != string(fieldValue) {
		targetField.Value = string(fieldValue)
		if err := c.editItem(*targetItem); err != nil {
			return fmt.Errorf("failed to set field, itemName: %s, fieldName: %s - %w", itemName, fieldName, err)
		}
	}
	return nil
}

func (c *cliClient) SetAttachmentOnItem(itemName, attachmentName string, fileContents []byte) error {
	var targetItem *Item
	var targetAttachment *Attachment
	var targetAttachmentIndex int
	for index, item := range c.savedItems {
		if itemName == item.Name {
			targetItem = &c.savedItems[index]
			for attachmentIndex, attachment := range item.Attachments {
				if attachment.FileName == attachmentName {
					targetAttachmentIndex = attachmentIndex
					targetAttachment = &c.savedItems[index].Attachments[attachmentIndex]
					break
				}
			}
			break
		}
	}
	if targetItem == nil {
		newItem := &Item{}
		if err := c.createEmptyItem(itemName, newItem); err != nil {
			return fmt.Errorf("failed to create new	 bw entry: %w", err)
		}
		c.savedItems = append(c.savedItems, *newItem)
		targetItem = &c.savedItems[len(c.savedItems)-1]
	}
	attachmentChanged := true
	if targetAttachment != nil {
		// read the attachment file
		tempDir, err := ioutil.TempDir("", "attachment")
		if err != nil {
			return fmt.Errorf("failed to create temporary file for getting: %w", err)
		}
		defer os.RemoveAll(tempDir)
		filePath := filepath.Join(tempDir, attachmentName)
		existingFileContents, err := c.getAttachmentOnItemToFile(itemName, attachmentName, filePath)
		if err != nil {
			return fmt.Errorf("error reading attachment: %w", err)
		}
		if bytes.Equal(fileContents, existingFileContents) {
			attachmentChanged = false
		} else {
			targetItem.Attachments = append(targetItem.Attachments[:targetAttachmentIndex], targetItem.Attachments[targetAttachmentIndex+1:]...)
			// If attachment exists delete it
			if err := c.deleteAttachment(targetAttachment.ID, targetItem.ID); err != nil {
				return fmt.Errorf("failed to set new attachment on item")
			}

		}
	}
	newAttachment := &Attachment{}
	// attachment is also considered to be changed if it hadnt existed earlier
	if attachmentChanged {
		if err := c.createAttachment(fileContents, attachmentName, targetItem.ID, newAttachment); err != nil {
			return fmt.Errorf("error creating attachment: %w", err)
		}
		targetItem.Attachments = append(targetItem.Attachments, *newAttachment)
	}
	return nil
}

func (c *cliClient) SetPassword(itemName string, password []byte) error {
	var targetItem *Item
	for index, item := range c.savedItems {
		if itemName == item.Name {
			targetItem = &c.savedItems[index]
			break
		}
	}
	if targetItem == nil {
		newItem := &Item{}
		if err := c.createItemWithPassword(itemName, password, newItem); err != nil {
			return fmt.Errorf("failed to create new	 bw entry: %w", err)
		}
		c.savedItems = append(c.savedItems, *newItem)
	} else {
		if targetItem.Login.Password != string(password) {
			targetItem.Login.Password = string(password)
			if err := c.editItem(*targetItem); err != nil {
				return fmt.Errorf("failed to set password for %s: %w", itemName, err)
			}
		}
	}
	return nil
}
