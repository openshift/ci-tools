package bitwarden

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	addSecrets func(s ...string)

	// onCreate is called before secrets are created by client methods, allowing
	// user code to default/validate created items
	onCreate func(*Item) error
}

func newCliClient(username, password string, addSecrets func(s ...string)) (Client, error) {
	return newCliClientWithRun(username, password, addSecrets, func(args ...string) ([]byte, error) {
		// bw-password is protected, session in args is not
		logger := logrus.WithField("args", args)
		logger.Debug("running bitwarden command ...")
		cmd := exec.Command("bw", args...)

		stderr, err := cmd.StderrPipe()
		if err != nil {
			logger.WithError(err).Error("could not open stderr pipe")
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			logger.WithError(err).Error("could not open stdout pipe")
			return nil, err
		}

		if err := cmd.Start(); err != nil {
			logger.WithError(err).Error("could not start command")
			return nil, err
		}

		stdoutContents, err := ioutil.ReadAll(stdout)
		if err != nil {
			logger.WithError(err).Error("could not read stdout pipe")
			return nil, err
		}
		stderrContents, err := ioutil.ReadAll(stderr)
		if err != nil {
			logger.WithError(err).Error("could not read stdout pipe")
			return nil, err
		}
		err = cmd.Wait()
		logger = logger.WithFields(logrus.Fields{
			"stdout": string(stdoutContents),
			"stderr": string(stderrContents),
		})
		if err != nil {
			logger.WithError(err).Error("bitwarden command failed")
		}

		return stdoutContents, err
	})
}

func newCliClientWithRun(username, password string, addSecrets func(s ...string), run func(args ...string) (bytes []byte, err error)) (Client, error) {
	client := cliClient{
		username:   username,
		password:   password,
		run:        run,
		addSecrets: addSecrets,
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
		return fmt.Errorf("failed to log in: %w", err)
	}
	r := bwLoginResponse{}
	if err := json.Unmarshal(output, &r); err != nil {
		return fmt.Errorf("failed to parse bw login output %s: %w", output, err)
	}
	if r.Success {
		raw := r.Data.Raw
		if raw != "" {
			c.session = raw
			c.addSecrets(c.session)
			var items []Item
			out, err := c.runWithSession("list", "items")
			if err != nil {
				return err
			}
			err = json.Unmarshal(out, &items)
			if err != nil {
				return fmt.Errorf("failed to parse bw item list output %s: %w", out, err)
			}
			c.savedItems = items
			return nil
		}
		// should never happen
		return errors.New("bw login succeeded with empty '.data.raw'")
	}
	// should never happen
	return errors.New("bw login failed without error from CLI")
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

func (c *cliClient) GetAllItems() []Item {
	return c.savedItems
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

func (c *cliClient) createItem(item Item, targetItem *Item) error {
	if c.onCreate != nil {
		if err := c.onCreate(&item); err != nil {
			return fmt.Errorf("OnCreate() failed on item: %w", err)
		}
	}

	itemBytes, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to serialize item: %w", err)
	}

	// the bitwarden cli expects the item to be base64 encoded
	encItem := base64.StdEncoding.EncodeToString(itemBytes)
	out, err := c.runWithSession("create", "item", encItem)
	if err != nil {
		return err
	}
	return json.Unmarshal(out, targetItem)
}

func (c *cliClient) createAttachment(fileContents []byte, fileName string, itemID string, newAttachment *Attachment) (retError error) {
	// Not tested
	tempDir, err := ioutil.TempDir("", "attachment")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for new attachment: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			retError = fmt.Errorf("failed to delete temporary file after use: %w", err)
		}
	}()
	filePath := filepath.Join(tempDir, fileName)
	if err := ioutil.WriteFile(filePath, fileContents, 0644); err != nil {
		return fmt.Errorf("failed to create temporary file for new attachment: %w", err)
	}
	out, err := c.runWithSession("create", "attachment", "--itemid", itemID, "--file", filePath)
	if err != nil {
		return fmt.Errorf("bw create failed: %w", err)
	}
	if err = json.Unmarshal(out, newAttachment); err != nil {
		return fmt.Errorf("failed to parse bw output %s: %w", out, err)
	}
	return nil
}

func (c *cliClient) createEmptyItem(itemName string, targetItem *Item) error {
	item := Item{
		Type:  1,
		Name:  itemName,
		Login: &Login{},
	}
	return c.createItem(item, targetItem)
}

func (c *cliClient) createItemWithPassword(itemName string, password []byte, targetItem *Item) error {
	item := Item{
		Type:  1,
		Name:  itemName,
		Login: &Login{string(password)},
	}
	return c.createItem(item, targetItem)
}

func (c *cliClient) createItemWithNotes(itemName, notes string, targetItem *Item) error {
	item := Item{
		Type:  1,
		Name:  itemName,
		Notes: notes,
		Login: &Login{},
	}
	return c.createItem(item, targetItem)
}

func (c *cliClient) editItem(targetItem Item) error {
	targetJSON, err := json.Marshal(targetItem)
	if err != nil {
		return fmt.Errorf("failed to marshal object: %w", err)
	}
	encodedItem := base64.StdEncoding.EncodeToString(targetJSON)
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
func (c *cliClient) UpdateNotesOnItem(itemName, notes string) error {
	var targetItem *Item
	for index, item := range c.savedItems {
		if itemName == item.Name {
			targetItem = &c.savedItems[index]
			break
		}
	}
	if targetItem == nil {
		newItem := &Item{}
		if err := c.createItemWithNotes(itemName, notes, newItem); err != nil {
			return fmt.Errorf("failed to create new bw entry: %w", err)
		}
		c.savedItems = append(c.savedItems, *newItem)
		return nil
	}

	if targetItem.Notes != notes && notes != "" {
		targetItem.Notes = notes
		if err := c.editItem(*targetItem); err != nil {
			return fmt.Errorf("failed to set password for %s: %w", itemName, err)
		}
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

func (c *cliClient) SetAttachmentOnItem(itemName, attachmentName string, fileContents []byte) (errorMsg error) {
	var targetItem *Item
	var targetAttachment *Attachment
	var targetAttachmentIndex int
	for index, item := range c.savedItems {
		if itemName != item.Name {
			continue
		}
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
	if targetItem == nil {
		newItem := &Item{}
		if err := c.createEmptyItem(itemName, newItem); err != nil {
			return fmt.Errorf("failed to create new	 bw entry: %w", err)
		}
		c.savedItems = append(c.savedItems, *newItem)
		targetItem = &c.savedItems[len(c.savedItems)-1]
	}
	if targetAttachment != nil {
		// read the attachment file
		tempDir, err := ioutil.TempDir("", "attachment")
		if err != nil {
			return fmt.Errorf("failed to create temporary file for getting: %w", err)
		}
		defer func() {
			if err := os.RemoveAll(tempDir); err != nil {
				errorMsg = fmt.Errorf("failed to delete temporary file after use: %w", err)
			}
		}()
		filePath := filepath.Join(tempDir, attachmentName)
		existingFileContents, err := c.getAttachmentOnItemToFile(itemName, attachmentName, filePath)
		if err != nil {
			return fmt.Errorf("error reading attachment: %w", err)
		}
		if bytes.Equal(fileContents, existingFileContents) {
			return nil
		}
		// If attachment exists delete it
		if err := c.deleteAttachment(targetAttachment.ID, targetItem.ID); err != nil {
			return fmt.Errorf("failed to delete current attachment on item: %w", err)
		}
		targetItem.Attachments = append(targetItem.Attachments[:targetAttachmentIndex], targetItem.Attachments[targetAttachmentIndex+1:]...)

	}
	newAttachment := &Attachment{}
	// attachment is also considered to be changed if it hadnt existed earlier
	if err := c.createAttachment(fileContents, attachmentName, targetItem.ID, newAttachment); err != nil {
		return fmt.Errorf("error creating attachment: %w", err)
	}
	targetItem.Attachments = append(targetItem.Attachments, *newAttachment)
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
			return fmt.Errorf("failed to create new bw entry: %w", err)
		}
		c.savedItems = append(c.savedItems, *newItem)
		return nil
	}
	if targetItem.Login.Password != string(password) {
		targetItem.Login.Password = string(password)
		if err := c.editItem(*targetItem); err != nil {
			return fmt.Errorf("failed to set password for %s: %w", itemName, err)
		}
	}
	return nil
}

func (c *cliClient) OnCreate(callback func(*Item) error) {
	c.onCreate = callback
}

type dryRunCliClient struct {
	file *os.File
}

func (d *dryRunCliClient) GetFieldOnItem(_, _ string) ([]byte, error) {
	return nil, nil
}

func (d *dryRunCliClient) GetAllItems() []Item {
	return nil
}

func (d *dryRunCliClient) GetAttachmentOnItem(_, _ string) ([]byte, error) {
	return nil, nil
}

func (d *dryRunCliClient) GetPassword(_ string) ([]byte, error) {
	return nil, nil
}

func (d *dryRunCliClient) Logout() ([]byte, error) {
	return nil, d.file.Close()
}

func (d *dryRunCliClient) SetFieldOnItem(itemName, fieldName string, fieldValue []byte) error {
	_, err := fmt.Fprintf(d.file, "ItemName: %s\n\tField: \n\t\t %s: %s\n", itemName, fieldName, string(fieldValue))
	return err
}

func (d *dryRunCliClient) SetAttachmentOnItem(itemName, attachmentName string, fileContents []byte) error {
	_, err := fmt.Fprintf(d.file, "ItemName: %s\n\tAttachment: \n\t\t %s: %s\n", itemName, attachmentName, string(fileContents))
	return err
}

func (d *dryRunCliClient) SetPassword(itemName string, password []byte) error {
	_, err := fmt.Fprintf(d.file, "ItemName: %s\n\tAttribute: \n\t\t Password: %s\n", itemName, string(password))
	return err
}

func (d *dryRunCliClient) UpdateNotesOnItem(itemName, notes string) error {
	_, err := fmt.Fprintf(d.file, "ItemName: %s\n\tNotes: %s\n", itemName, notes)
	return err
}

func (d *dryRunCliClient) OnCreate(func(*Item) error) {}

func newDryRunClient(file *os.File) (Client, error) {
	return &dryRunCliClient{
		file: file,
	}, nil
}

var _ Client = &dryRunCliClient{}
