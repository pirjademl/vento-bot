package dtos

type LineComment struct {
	FilePath   string `json:"file_path"`
	LineNumber int    `json:"line_number"`
	Body       string `json:"body"`
}
type StructuredReview struct {
	LineComments   []LineComment `json:"line_comments"`
	GeneralComment string        `json:"general_comment"`
}
