package main

type ocpImageConfig struct {
	Content        ocpImageConfigContent `json:"content"`
	From           ocpImageConfigFrom    `json:"from"`
	SourceFileName string                `json:"-"`
}

type ocpImageConfigContent struct {
	Source ocpImageConfigSource `json:"source"`
}

type ocpImageConfigSource struct {
	Dockerfile string `json:"dockerfile"`
}

type ocpImageConfigFrom struct {
	Builder []ocpImageConfigFromStream `json:"builder"`
	Stream  ocpImageConfigFromStream   `json:"stream"`
}

type ocpImageConfigFromStream struct {
	Stream string `json:"stream"`
}

func (oic ocpImageConfig) dockerfile() string {
	if oic.Content.Source.Dockerfile != "" {
		return oic.Content.Source.Dockerfile
	}
	return "Dockerfile"
}

func (oic ocpImageConfig) stages() []string {
	var result []string
	for _, builder := range oic.From.Builder {
		result = append(result, builder.Stream)
	}
	return append(result, oic.From.Stream.Stream)
}
