package buildspec

type BuildSpec struct {
	Env     Env    `json:"env"`
	Phases  Phases `json:"phases"`
	Version string `json:"version"`
}

type Env struct {
	Variables Variables `json:"variables"`
}
type Phases struct {
	Build BuildPhase `json:"build"`
}

type Variables struct {
	DockerFile  string `json:"dockerfile"`
	Credentials string `json:"credentials"`
}
type BuildPhase struct {
	OnFailure string   `json:"on-failure"`
	Commands  []string `json:"commands"`
}
