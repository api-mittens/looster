package task

// ColumnPageSize is fixed so a dashboard request cannot hydrate an unbounded list.
const ColumnPageSize = 15

type ColumnQuery struct {
	Key    string
	Search string
	Page   int
	// ActiveTaskIDs comes from the server's live session metadata, not the client.
	ActiveTaskIDs []int64
}

type ColumnPage struct {
	Key      string `json:"key"`
	Tasks    []Task `json:"tasks"`
	Total    int64  `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}
