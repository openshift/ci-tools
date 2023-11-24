package main

import (
	"github.com/montanaflynn/stats"
	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

type secretStats struct {
	// count is the total number of the secrets
	count int
	// median is the median of the size of the data of the secrets
	// the size is counted by the sum of length of each key and its value in the secret's data
	median float64
}

func generateSecretStats(secretsByClusterAndName map[string]map[types.NamespacedName]coreapi.Secret) secretStats {
	if len(secretsByClusterAndName) == 0 {
		return secretStats{}
	}
	count := 0
	var size []float64
	for _, secretMap := range secretsByClusterAndName {
		count += len(secretMap)
		for _, secret := range secretMap {
			size = append(size, float64(getSize(secret)))
		}
	}
	median, err := stats.Median(size)
	if err != nil {
		logrus.WithError(err).Error("Failed to calculate the median")
		return secretStats{}
	}
	result := secretStats{count: count, median: median}
	return result
}

func getSize(secret coreapi.Secret) int {
	size := 0
	for k, v := range secret.Data {
		size = size + len(k) + len(v)
	}
	return size
}
