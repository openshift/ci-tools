package helpdesk_faq

type FaqItem struct {
	Question  string   `json:"question"`
	Timestamp string   `json:"timestamp"`
	Author    string   `json:"author"`
	Answers   []Answer `json:"answers"`
}

type Answer struct {
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
	Body      string `json:"body"`
}

//TODO(sgoeddel): We probably need a "contributing info" emoji and section as well for when the question isn't entirely summarized in one prompt
//TODO(sgoeddel): It would also be good to link to the original full thread for additional context
