package helpdesk_faq

type FaqItem struct {
	Question         Question `json:"question"`
	Timestamp        string   `json:"timestamp"`
	ContributingInfo []Reply  `json:"contributing_info"`
	Answers          []Reply  `json:"answers"`
}

// ReplyExists takes a timestamp and returns true if the reply at that timestamp is included
// in the Answers or ContributingInfo on this FaqItem
func (f FaqItem) ReplyExists(timestamp string) bool {
	for _, answer := range f.Answers {
		if answer.Timestamp == timestamp {
			return true
		}
	}
	for _, contributingInfo := range f.ContributingInfo {
		if contributingInfo.Timestamp == timestamp {
			return true
		}
	}

	return false
}

type Question struct {
	Author  string `json:"author"`
	Topic   string `json:"topic"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type Reply struct {
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
	Body      string `json:"body"`
}

//TODO(sgoeddel): It would also be good to link to the original full thread for additional context
