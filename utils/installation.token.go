package utils

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type GHInstalltion struct {
	Token string `json:"token"`
}

func GetInstallationToken(jwt string, installId int) (string, error) {

	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installId)

	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic("getting installation token error")
	}

	defer resp.Body.Close()
	if err != nil {
		panic(err)
	}
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github api error: status %d", resp.StatusCode)
	}

	var instlToken GHInstalltion
	if err := json.NewDecoder(resp.Body).Decode(&instlToken); err != nil {
		return "", err
	}

	return instlToken.Token, nil

}
