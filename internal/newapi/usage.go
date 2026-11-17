package newapi

import "fmt"

// usagePage is the subset of one /api/log/self page the statistics
// aggregation needs. The site sends many more fields (token name, addresses,
// request details); none of them are parsed or retained.
type usagePage struct {
	Items []struct {
		ModelName        string `json:"model_name"`
		PromptTokens     int64  `json:"prompt_tokens"`
		CompletionTokens int64  `json:"completion_tokens"`
		Quota            int64  `json:"quota"`
		CreatedAt        int64  `json:"created_at"`
		UseTime          int64  `json:"use_time"`
		Other            string `json:"other"`
	} `json:"items"`
	Total int64 `json:"total"`
}

const usagePageSize = 100

// usageLogPath is the consumption-log query used by the statistics
// aggregation.
func usageLogPath(p int, start, end int64) string {
	return fmt.Sprintf("/api/log/self?p=%d&page_size=%d&type=2&start_timestamp=%d&end_timestamp=%d", p, usagePageSize, start, end)
}
