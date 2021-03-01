package main

type managedVaultPolicy struct {
	Path map[string]managedVaultPolicyCapabiltyList `json:"path,omitempty"`
}

type managedVaultPolicyCapabiltyList struct {
	Capabilities []string `json:"capabilities,omitempty"`
}

type secretCollection struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Members []string `json:"members,omitempty"`
}

type secretColectionUpdateBody struct {
	Members []string `json:"members,omitempty"`
}
