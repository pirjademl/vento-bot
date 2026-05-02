package dtos

type Owner struct {
	Login     string `json:"login"`
	Id        int    `json:"id"`
	AvatarUrl string `json:"avatar_url"`
}
