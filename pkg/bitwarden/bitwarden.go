package bitwarden

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

// Item represents an item in BitWarden
// It has only fields we are interested in
type Item struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Fields      []Field      `json:"fields"`
	Attachments []Attachment `json:"attachments"`
}

// Client is used to communicate with BitWarden
type Client interface {
	GetFieldOnItem(itemName, fieldName string) ([]byte, error)
	GetAttachmentOnItem(itemName, attachmentName string) ([]byte, error)
	Logout() ([]byte, error)
}

// NewBitwardenClient generates a BitWarden client
func NewClient(username, password string) (Client, error) {
	return newCliClient(username, password)
}
