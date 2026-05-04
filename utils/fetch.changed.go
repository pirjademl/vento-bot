package utils

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/google/go-github/v85/github"
)

func FetchChangedFiles(
	ctx context.Context,
	ghClient *github.Client,
	owner, repo, sha string,
	changedFiles map[string]bool,
) (string, error) {
	// Create a temp directory exactly like CloneRepo
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("vento-sync-%s-*", repo))
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	for filePath := range changedFiles {
		// Fetch content from GitHub API
		fileContent, _, _, err := ghClient.Repositories.GetContents(
			ctx,
			owner,
			repo,
			filePath,
			&github.RepositoryContentGetOptions{Ref: sha},
		)
		if err != nil {
			log.Printf("Failed to fetch %s: %v", filePath, err)
			continue
		}

		content, err := fileContent.GetContent()
		if err != nil {
			log.Printf("Failed to decode %s: %v", filePath, err)
			continue
		}

		// Recreate directory structure locally
		localPath := filepath.Join(tempDir, filePath)
		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			os.RemoveAll(tempDir)
			return "", fmt.Errorf("failed to create directory for %s: %w", filePath, err)
		}

		// Write the file
		if err := os.WriteFile(localPath, []byte(content), 0644); err != nil {
			os.RemoveAll(tempDir)
			return "", fmt.Errorf("failed to write file %s: %w", filePath, err)
		}
	}

	return tempDir, nil
}
