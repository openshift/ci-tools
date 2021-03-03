package main

type managedVaultPolicy struct {
	Path map[string]managedVaultPolicyCapabilityList `json:"path,omitempty"`
}

type managedVaultPolicyCapabilityList struct {
	Capabilities []string `json:"capabilities,omitempty"`
}

type secretCollection struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Members []string `json:"members,omitempty"`
}

type secretCollectionUpdateBody struct {
	Members []string `json:"members,omitempty"`
}
