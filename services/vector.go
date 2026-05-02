package services

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/genai"
)

var wait bool = true
var OutputDimensionality int32 = 768

type Chunk struct {
	Path    string
	Content string
}

func CreateChunks(root string) ([]Chunk, error) {
	var chunks []Chunk

	excludeDirs := map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true}
	allowedFileExtensions := map[string]bool{
		".go":   true,
		".ts":   true,
		".js":   true,
		".py":   true,
		".java": true,
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if d, ok := excludeDirs[filepath.Base(path)]; ok && d {
			return filepath.SkipDir
		}
		if !allowedFileExtensions[filepath.Ext(path)] {
			return nil
		}
		filechunks, err := processFile(path, root)
		if err != nil {
			log.Printf("Warning: failed to process file %s: %v", path, err)
			return err
		}
		chunks = append(chunks, filechunks...)
		return nil

	})
	if err != nil {
		log.Println(err.Error())
	}
	return chunks, nil
}

func processFile(path, root string) ([]Chunk, error) {
	file, err := os.Open(path)

	if err != nil {
		return nil, err
	}

	defer file.Close()

	var fileChunks []Chunk
	var currentChunk strings.Builder
	scanner := bufio.NewScanner(file)
	lineCount := 0

	const chunkSize = 50

	relPath, _ := filepath.Rel(root, path)

	for scanner.Scan() {
		currentChunk.WriteString(scanner.Text() + "\n")
		lineCount++

		if lineCount >= chunkSize {
			fileChunks = append(fileChunks, Chunk{
				Path:    relPath,
				Content: currentChunk.String(),
			})
			currentChunk.Reset()
			lineCount = 0
		}
	}

	if currentChunk.Len() > 0 {
		fileChunks = append(fileChunks, Chunk{
			Path:    relPath,
			Content: currentChunk.String(),
		})
	}

	return fileChunks, nil
}

func (vs *VectorService) UpsertVectors(ctx context.Context, repoID int64, chunks []Chunk) error {
	const batchSize = 100

	for i := 0; i < len(chunks); i += batchSize {
		end := i + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}

		batchChunks := chunks[i:end]

		// Use a slice of pointers as required by the new SDK
		var contents []*genai.Content

		for _, chunk := range batchChunks {
			contextualContent := fmt.Sprintf("File: %s\n\n%s", chunk.Path, chunk.Content)

			// New SDK helper returns *genai.Content
			contents = append(contents, genai.NewContentFromText(contextualContent, genai.RoleUser))
		}

		// The new SDK call structure
		res, err := vs.AIClient.Models.EmbedContent(ctx,
			"gemini-embedding-2",
			contents,
			&genai.EmbedContentConfig{
				// Native truncation to 768 dimensions
				OutputDimensionality: genai.Ptr[int32](768),
			},
		)
		if err != nil {
			return fmt.Errorf("gemini batch embedding failed: %w", err)
		}

		// Map to Qdrant Points
		var points []*qdrant.PointStruct
		for j, emb := range res.Embeddings {
			points = append(points, &qdrant.PointStruct{
				Id:      qdrant.NewIDUUID(uuid.NewString()),
				Vectors: qdrant.NewVectors(emb.Values...),
				Payload: map[string]*qdrant.Value{
					"repo_id":   qdrant.NewValueInt(repoID),
					"file_path": qdrant.NewValueString(batchChunks[j].Path),
					"content":   qdrant.NewValueString(batchChunks[j].Content),
				},
			})
		}

		// Upsert to Qdrant (ensure vs.CollectionName is correct)
		_, err = vs.QDClient.Upsert(ctx, &qdrant.UpsertPoints{
			CollectionName: vs.CollectionName,
			Points:         points,
			Wait:           &wait,
		})
		if err != nil {
			return fmt.Errorf("qdrant upsert failed: %w", err)
		}
		if i+batchSize < len(chunks) {
			log.Printf("Batch %d uploaded, pacing for 1.5s...", i/batchSize+1)
			time.Sleep(1500 * time.Millisecond)
		}

	}
	return nil
}
func (vs *VectorService) GetEmbedding(ctx context.Context, text string) ([]float32, error) {
	cntx := context.Background()

	//	model := vs.AIClient.EmbeddingModel("gemini-embedding-2")
	//	res, err := model.EmbedContent(ctx, genai.Text(text))
	contents := []*genai.Content{
		genai.NewContentFromText(text, genai.RoleUser),
	}

	res, err := vs.AIClient.Models.EmbedContent(
		cntx,
		"gemini-embedding-2",
		contents,
		&genai.EmbedContentConfig{OutputDimensionality: &OutputDimensionality},
	)
	if err != nil {
		return nil, err
	}
	return res.Embeddings[0].Values, nil

}
