package helpdesk_faq

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	faqConfigMap = "helpdesk-faq"
)

type FaqItemClient interface {
	GetSerializedFAQItems() ([]string, error)
	GetFAQItemIfExists(timestamp string) (*FaqItem, error)
	UpsertItem(item FaqItem) error
	RemoveItem(timestamp string) error
}

func NewCMClient(kubeClient ctrlruntimeclient.Client, namespace string, logger *logrus.Entry) ConfigMapClient {
	return ConfigMapClient{kubeClient: kubeClient, namespace: namespace, logger: logger}
}

type ConfigMapClient struct {
	kubeClient  ctrlruntimeclient.Client
	namespace   string
	cachedItems []string
	lastReload  time.Time
	logger      *logrus.Entry
}

func (c *ConfigMapClient) GetSerializedFAQItems() ([]string, error) {
	fifteenMinutesFromLastCacheReload := c.lastReload.Add(time.Minute * 15)
	if len(c.cachedItems) > 0 && time.Now().Before(fifteenMinutesFromLastCacheReload) {
		c.logger.Debug("returning faq items from cache")
		return c.cachedItems, nil
	}
	c.logger.Debug("reloading faq items from configmap")
	configMap, err := c.getConfigMap()
	if err != nil {
		return nil, err
	}
	if configMap.Data == nil {
		return nil, nil
	}
	items := convertDataToSortedSlice(configMap.Data)
	c.cachedItems = items
	c.lastReload = time.Now()
	return items, nil
}

// convertDataToSortedSlice takes the data in the configmap, sorts it by timestamp, and returns the ordered slice of items
func convertDataToSortedSlice(data map[string]string) []string {
	var keys []string
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var items []string
	for _, key := range keys {
		items = append(items, data[key])
	}
	return items
}

func (c *ConfigMapClient) GetFAQItemIfExists(timestamp string) (*FaqItem, error) {
	configMap, err := c.getConfigMap()
	if err != nil {
		return nil, fmt.Errorf("unable to get configmap: %w", err)
	}
	rawFaqItem := configMap.Data[timestamp]
	if rawFaqItem == "" {
		return nil, nil
	}
	faqItem := &FaqItem{}
	if err = json.Unmarshal([]byte(rawFaqItem), faqItem); err != nil {
		return nil, fmt.Errorf("unable to unmarshall faqItem: %w", err)
	}
	return faqItem, nil
}

func (c *ConfigMapClient) UpsertItem(item FaqItem) error {
	c.sortReplies(&item)
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("unable to marshal faqItem to json: %w", err)
	}
	configMap, err := c.getConfigMap()
	if err != nil {
		return fmt.Errorf("unable to get configmap: %w", err)
	}
	configMap.Data[item.Timestamp] = string(data)
	err = c.kubeClient.Update(context.TODO(), configMap)
	if err != nil {
		return fmt.Errorf("unable to update helpdesk-faq config map: %w", err)
	}

	return nil
}

func (c *ConfigMapClient) RemoveItem(timestamp string) error {
	configMap, err := c.getConfigMap()
	if err != nil {
		return fmt.Errorf("unable to get configmap: %w", err)
	}
	delete(configMap.Data, timestamp)
	err = c.kubeClient.Update(context.TODO(), configMap)
	if err != nil {
		return fmt.Errorf("unable to update helpdesk-faq config map: %w", err)
	}

	return nil
}

func (c *ConfigMapClient) getConfigMap() (*v1.ConfigMap, error) {
	configMap := &v1.ConfigMap{}
	if err := c.kubeClient.Get(context.TODO(), types.NamespacedName{Namespace: c.namespace, Name: faqConfigMap}, configMap); err != nil {
		return nil, fmt.Errorf("failed to get configMap %s: %w", faqConfigMap, err)
	}
	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}
	return configMap, nil
}

func (c *ConfigMapClient) sortReplies(item *FaqItem) {
	cmp := func(a Reply, b Reply) int {
		aTime, err := strconv.Atoi(strings.Split(a.Timestamp, ".")[0])
		if err != nil {
			c.logger.WithError(err).Errorf("couldn't convert timestamp to int")
			return 0
		}
		bTime, err := strconv.Atoi(strings.Split(b.Timestamp, ".")[0])
		if err != nil {
			c.logger.WithError(err).Errorf("couldn't convert timestamp to int")
			return 0
		}
		return aTime - bTime
	}
	slices.SortFunc(item.Answers, cmp)
	slices.SortFunc(item.ContributingInfo, cmp)
}
