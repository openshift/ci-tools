package certmanager

const (
	OpenshiftMarketplaceNS = "openshift-marketplace"
	RegistryCatalogPort    = "50051"
	RegistryCatalogPortInt = 50051
)

type Package struct {
	Name               string      `json:"name,omitempty"`
	Channels           []Channel   `json:"channels,omitempty"`
	DefaultChannelName string      `json:"defaultChannelName,omitempty"`
	Deprecation        Deprecation `json:"deprecation,omitempty"`
}

type Channel struct {
	Name        string      `json:"name,omitempty"`
	CSVName     string      `json:"csvName,omitempty"`
	Deprecation Deprecation `json:"deprecation,omitempty"`
}

type Deprecation struct {
	Message string `json:"Message,omitempty"`
}
