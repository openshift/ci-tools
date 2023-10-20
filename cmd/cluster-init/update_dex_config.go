package main

import (
	"encoding/base32"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
)

type OAuth2Client struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name string `json:"name"`
	} `json:"metadata"`
	RedirectURIs []string `json:"redirectURIs"`
	Id           string   `json:"id"`
	Name         string   `json:"name"`
}

func getHash(clusterName string) string {
	var encoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567")
	toDecodeString := []byte(clusterName)
	hash := strings.TrimRight(encoding.EncodeToString(fnv.New64().Sum(toDecodeString)), "=")
	return hash
}

func getCallBackUrls(baseUrl string) []string {
	return []string{
		"https://oauth-openshift.apps." + baseUrl + "/oauth2callback/RedHat_Internal_SSO",
	}
}

func updateDexConfig(o options, buildClusters *BuildClusters) error {
	logrus.Info("Updating Dex oauth client config")
	dirName := filepath.Join(o.releaseRepo, "clusters", "app.ci", "dex", "clients")
	clusterName := o.clusterName
	baseDomain := buildClusters.Config[clusterName].BaseUrl
	if baseDomain == "" {
		baseDomain = o.baseDomain
	}

	// Convert cluster name to hash
	// Ref: https://github.com/dexidp/dex/blob/3b78752ab17e2ca8f6bdc9b10c7405a56b5640eb/storage/kubernetes/client.go#L76

	yamlFile := filepath.Join(dirName, clusterName+".yaml")

	logrus.Infof("Creating Dex oauth client config file %s", yamlFile)

	// TODO: convert this to be a template using vault
	client := OAuth2Client{
		APIVersion: "dex.coreos.com/v1",
		Kind:       "OAuth2Client",
	}
	client.Metadata.Name = getHash(clusterName)
	client.Id = clusterName
	client.Name = clusterName
	client.RedirectURIs = getCallBackUrls(baseDomain)

	yamlData, err := yaml.Marshal(client)
	if err != nil {
		return err
	}

	if err := os.WriteFile(yamlFile, yamlData, 0644); err != nil {
		return err
	}
	return nil
}
