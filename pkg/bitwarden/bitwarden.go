package bitwarden

import (
	"os"
	"time"
)

// Field represents a field in BitWarden
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Attachment represents an attachment in BitWarden
type Attachment struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
}

// Login represents login in BitWarden
type Login struct {
	Password string `json:"password,omitempty"`
}

// Item represents an item in BitWarden
// It has only fields we are interested in
type Item struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Type  int    `json:"type"`
	Notes string `json:"notes,omitempty"`
	// Login does NOT exist on some BitWarden entries, e.g, secure notes.
	Login       *Login       `json:"login,omitempty"`
	Fields      []Field      `json:"fields"`
	Attachments []Attachment `json:"attachments"`
	// RevisionTime is a pointer so that omitempty works. The field is set only
	// when we get the record from BW but not e.g. when we create or update records
	RevisionTime *time.Time `json:"revisionDate,omitempty"`

	Organization string   `json:"organizationId,omitempty"`
	Collections  []string `json:"collectionIds,omitempty"`
}

// Client is used to communicate with BitWarden
type Client interface {
	GetFieldOnItem(itemName, fieldName string) ([]byte, error)
	GetAllItems() []Item
	GetAttachmentOnItem(itemName, attachmentName string) ([]byte, error)
	GetPassword(itemName string) ([]byte, error)
	Logout() ([]byte, error)
	SetFieldOnItem(itemName, fieldName string, fieldValue []byte) error
	SetAttachmentOnItem(itemName, attachmentName string, fileContents []byte) error
	SetPassword(itemName string, password []byte) error
	UpdateNotesOnItem(itemName string, notes string) error

	OnCreate(func(*Item) error)
	HasItem(itemName string) bool
}

// NewClient generates a BitWarden client
func NewClient(username, password string, addSecrets func(s ...string)) (Client, error) {
	return newCliClient(username, password, addSecrets)
}

// NewDryRunClient generates a BitWarden client
func NewDryRunClient(outputFile *os.File) (Client, error) {
	return newDryRunClient(outputFile)
}
