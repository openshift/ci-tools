package secrets

import (
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/bitwarden"
)

type bitwardenClient struct {
	bitwarden.Client
}

func NewBitwardenClient(bw bitwarden.Client) Client {
	return &bitwardenClient{Client: bw}
}

func (bw *bitwardenClient) GetInUseInformationForAllItems() (map[string]SecretUsageComparer, error) {
	allItems := bw.GetAllItems()

	result := map[string]SecretUsageComparer{}
	for idx, item := range allItems {
		result[item.Name] = &bitwardenSecretUsageComparer{item: allItems[idx]}
	}

	return result, nil
}

type bitwardenSecretUsageComparer struct {
	item bitwarden.Item
}

func (bwc *bitwardenSecretUsageComparer) LastChanged() time.Time {
	if bwc.item.RevisionTime != nil {
		return *bwc.item.RevisionTime
	}
	return time.Time{}
}

func (bwc *bitwardenSecretUsageComparer) UnusedFields(inUse sets.String) (difference sets.String) {
	allFields := sets.String{}
	for _, field := range bwc.item.Fields {
		allFields.Insert(field.Name)
	}
	return allFields.Difference(inUse)
}

func (bwc *bitwardenSecretUsageComparer) UnusedAttachments(inUse sets.String) (difference sets.String) {
	allAttachments := sets.String{}
	for _, attachment := range bwc.item.Attachments {
		allAttachments.Insert(attachment.FileName)
	}
	return allAttachments.Difference(inUse)
}

func (bwc *bitwardenSecretUsageComparer) SuperfluousFields() sets.String {
	return nil
}

func (bwc *bitwardenSecretUsageComparer) HasPassword() bool {
	return bwc.item.Login != nil && bwc.item.Login.Password != ""
}
