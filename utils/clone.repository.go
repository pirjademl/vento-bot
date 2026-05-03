package utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
)

func CloneRepo(instlToken, owner, repo string) (string, error) {
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("vento-%s-*", repo))

	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	url := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", instlToken, owner, repo)

	cmd := exec.Command(
		"git",
		"clone",
		"--single-branch",
		"--depth",
		"1",
		url,
		tempDir,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		//os.RemoveAll(tempDir)
		log.Printf("Git clone failed: %s\nOutput: %s", err.Error(), string(output))
		return "", fmt.Errorf("git clone failed: %w", err)
	}

	return tempDir, nil
}
