package secrets

import (
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
)

type ReadOnlyClient interface {
	GetFieldOnItem(itemName, fieldName string) ([]byte, error)
	GetAttachmentOnItem(itemName, attachmentName string) ([]byte, error)
	GetPassword(itemName string) ([]byte, error)
	GetInUseInformationForAllItems(optionalPrefix string) (map[string]SecretUsageComparer, error)
	GetUserSecrets() (map[types.NamespacedName]map[string]string, error)
	Logout() ([]byte, error)
	HasItem(itemname string) (bool, error)
}

type Client interface {
	ReadOnlyClient
	SetFieldOnItem(itemName, fieldName string, fieldValue []byte) error
	SetAttachmentOnItem(itemName, attachmentName string, fileContents []byte) error
	SetPassword(itemName string, password []byte) error
	UpdateNotesOnItem(itemName string, notes string) error
}

type SecretUsageComparer interface {
	LastChanged() time.Time
	UnusedFields(inUse sets.String) (Difference sets.String)
	UnusedAttachments(inUse sets.String) (Difference sets.String)
	HasPassword() bool
	SuperfluousFields() sets.String
}
