package bitwarden

import "fmt"

type fakeClient struct {
	items       []Item
	attachments map[string]string
}

func (c fakeClient) GetFieldOnItem(itemName, fieldName string) ([]byte, error) {
	for _, item := range c.items {
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

func (c fakeClient) GetAttachmentOnItem(itemName, attachmentName string) ([]byte, error) {
	for _, item := range c.items {
		if itemName == item.Name {
			for _, attachment := range item.Attachments {
				if attachment.FileName == attachmentName {
					if value, ok := c.attachments[attachment.ID]; ok {
						return []byte(value), nil
					}
				}
			}
		}
	}
	return nil, fmt.Errorf("failed to find attachment %s in item %s", attachmentName, itemName)
}

func (c fakeClient) Logout() ([]byte, error) {
	return []byte("logged out"), nil
}

func (c fakeClient) GetPassword(itemName string) ([]byte, error) {
	for _, item := range c.items {
		if itemName == item.Name {
			if item.Login != nil {
				return []byte(item.Login.Password), nil
			}
		}
	}
	return nil, fmt.Errorf("failed to find password in item %s", itemName)
}

// NewFakeClient generates a fake BitWarden client which is supposed to used only for testing
func NewFakeClient(items []Item, attachments map[string]string) Client {
	return fakeClient{items: items, attachments: attachments}
}
