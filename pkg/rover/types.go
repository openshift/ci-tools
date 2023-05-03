package rover

type User struct {
	GitHubUsername string `json:"github_username,omitempty"`
	UID            string `json:"uid,omitempty"`
	CostCenter     string `json:"cost_center,omitempty"`
}
