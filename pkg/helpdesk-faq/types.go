package helpdesk_faq

type FaqItem struct {
	Question  Question `json:"question"`
	Timestamp string   `json:"timestamp"`
	Answers   []Answer `json:"answers"`
}

type Question struct {
	Author  string `json:"author"`
	Topic   string `json:"topic"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type Answer struct {
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
	Body      string `json:"body"`
}

//TODO(sgoeddel): We probably need a "contributing info" emoji and section as well for when the question isn't entirely summarized in one prompt
//TODO(sgoeddel): It would also be good to link to the original full thread for additional context
