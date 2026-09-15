package api

// JobQueueConfig defines the globally available Prow job queues.
type JobQueueConfig struct {
	JobQueues map[string]JobQueue `json:"job_queues"`
}

// JobQueue defines the capacity and purpose of a Prow job queue.
type JobQueue struct {
	Capacity    int    `json:"capacity"`
	Description string `json:"description"`
}
