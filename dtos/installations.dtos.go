package dtos

// this is how installation is represented in the installations table
type InstallationDBDto struct {
	Id           int    `json:"id"`
	AccountId    int    `json:"account_id"`
	AccountLogin string `json:"account_login"`
	//created_at   time
}
type Installation struct {
	ID int64 `json:"id"`
}
