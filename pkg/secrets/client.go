package secrets

import (
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
)

type ReadOnlyClient interface {
	GetFieldOnItem(itemName, fieldName string) ([]byte, error)
	GetInUseInformationForAllItems(optionalPrefix string) (map[string]SecretUsageComparer, error)
	GetUserSecrets() (map[types.NamespacedName]map[string]string, error)
	HasItem(itemname string) (bool, error)
}

type Client interface {
	ReadOnlyClient
	SetFieldOnItem(itemName, fieldName string, fieldValue []byte) error
	UpdateNotesOnItem(itemName string, notes string) error
	UpdateIndexSecret(itemName string, payload []byte) error
}

type SecretUsageComparer interface {
	LastChanged() time.Time
	UnusedFields(inUse sets.Set[string]) (Difference sets.Set[string])
	SuperfluousFields() sets.Set[string]
}
