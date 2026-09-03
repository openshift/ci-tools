package installconfig

// InstallConfig is a minimal representation of the OpenShift installer's
// InstallConfig type, containing only the fields that ci-tools reads. This
// avoids a direct dependency on github.com/openshift/installer, whose
// transitive dependency tree conflicts with other modules required by ci-tools.
type InstallConfig struct {
	BaseDomain string        `json:"baseDomain"`
	Platform   Platform      `json:"platform"`
	Compute    []MachinePool `json:"compute,omitempty"`
}

type Platform struct {
	AWS *AWSPlatform `json:"aws,omitempty"`
}

type AWSPlatform struct {
	Region string `json:"region"`
}

type MachinePool struct {
	Name     string              `json:"name"`
	Platform MachinePoolPlatform `json:"platform"`
}

type MachinePoolPlatform struct {
	AWS *AWSMachinePool `json:"aws,omitempty"`
}

type AWSMachinePool struct {
	Zones []string `json:"zones,omitempty"`
}
