package official

// Response is what Cincinnati sends us when querying for releases in a channel
type Response struct {
	Nodes []Release `json:"nodes"`
}

// Release describes a release payload
type Release struct {
	Version string `json:"version"`
	Payload string `json:"payload"`
}
