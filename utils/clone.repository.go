package utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
)

func CloneRepo(instlToken, owner, repo, ref string) (string, error) {
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("vento-%s-*", repo))
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	url := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", instlToken, owner, repo)

	// --branch works for both branch names and tags.
	// --single-branch reduces the amount of data fetched (standard for RAG indexing).
	cmd := exec.Command(
		"git",
		"clone",
		"--branch",
		ref,
		"--single-branch",
		"--depth",
		"1",
		url,
		tempDir,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(tempDir)
		log.Printf("Git clone failed: %s\nOutput: %s", err.Error(), string(output))
		return "", fmt.Errorf("git clone failed: %w", err)
	}

	return tempDir, nil
}
