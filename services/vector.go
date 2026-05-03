package services

import (
	"bufio"
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go/parser"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/genai"
)

var wait bool = true
var OutputDimensionality int32 = 768

type Chunk struct {
	Path       string
	Content    string
	Signature  string
	SymbolType string
}

func CreateChunks(root string) ([]Chunk, error) {
	var chunks []Chunk

	excludeDirs := map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true}
	allowedFileExtensions := map[string]bool{
		".go":   true,
		".ts":   true,
		".js":   true,
		".tsx":  true,
		".jsx":  true,
		".py":   true,
		".java": true,
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if d, ok := excludeDirs[filepath.Base(path)]; ok && d {
			return filepath.SkipDir
		}
		if !allowedFileExtensions[filepath.Ext(path)] {
			log.Println("skipping this path")
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
	if filepath.Ext(path) != "go" {
		return processFileWithLines(path, root)
	}
	relpath, _ := filepath.Rel(root, path)
	content, err := os.ReadFile(path)
	if err != nil {
		log.Printf("error reading file", err.Error())
		return nil, err
	}
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		log.Printf("error parsing go files ", err.Error())
		return nil, err
	}
	var fileChunks []Chunk
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			sig := x.Name.Name
			if x.Recv != nil {
				sig = fmt.Sprintf("func %s", sig)
			}
			fileChunks = append(fileChunks, Chunk{
				Path:       relpath,
				Content:    string(content[x.Pos()-1 : x.End()]),
				Signature:  sig,
				SymbolType: "function",
			})
		case *ast.TypeSpec:
			// 2. Check what the underlying type actually is
			symbolType := "type_defination"
			switch x.Type.(type) {
			case *ast.StructType:
				symbolType = "struct"
			case *ast.InterfaceType:
				symbolType = "interface"
			}
			fileChunks = append(fileChunks, Chunk{
				Path:       relpath,
				Content:    string(content[x.Pos()-1 : x.End()]),
				Signature:  x.Name.Name,
				SymbolType: symbolType,
			})
		case *ast.GenDecl:
			if x.Tok == token.IMPORT {
				fileChunks = append(fileChunks, Chunk{
					Path:       relpath,
					Content:    string(content[x.Pos()-1 : x.End()]),
					Signature:  "imports",
					SymbolType: "environment",
				})
			}
			for _, spec := range x.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:

				case *ast.ValueSpec:
					//example-> var Endpoint = "api/v1" or const MaxRetries = 5
					for _, name := range s.Names {
						fileChunks = append(fileChunks, Chunk{
							Path:       relpath,
							Content:    string(content[x.Pos()-1 : x.End()]),
							Signature:  name.Name,
							SymbolType: "global_value",
						})
					}
				}
			}
		}
		return true
	})
	return fileChunks, nil
}
func processFileWithLines(path, root string) ([]Chunk, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var fileChunks []Chunk
	var currentChunk strings.Builder
	scanner := bufio.NewScanner(file)
	lineCount := 0
	chunkIndex := 1

	const chunkSize = 50
	relPath, _ := filepath.Rel(root, path)

	for scanner.Scan() {
		currentChunk.WriteString(scanner.Text() + "\n")
		lineCount++

		if lineCount >= chunkSize {
			fileChunks = append(fileChunks, Chunk{
				Path:       relPath,
				Content:    currentChunk.String(),
				Signature:  fmt.Sprintf("%s-part-%d", filepath.Base(relPath), chunkIndex),
				SymbolType: "file_fragment",
			})
			currentChunk.Reset()
			lineCount = 0
			chunkIndex++
		}
	}

	if currentChunk.Len() > 0 {
		fileChunks = append(fileChunks, Chunk{
			Path:       relPath,
			Content:    currentChunk.String(),
			Signature:  fmt.Sprintf("%s-part-%d", filepath.Base(relPath), chunkIndex),
			SymbolType: "file_fragment",
		})
	}

	return fileChunks, nil
}
func generatePointID(repoID int64, path, signature string) string {
	data := fmt.Sprintf("%d:%s:%s", repoID, path, signature)
	hash := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(data)).String()
	fmt.Println("generated hash ", hash)
	return hash
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
		res, err := vs.GeminiClient.Models.EmbedContent(ctx,
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

			pointId := generatePointID(repoID, batchChunks[j].Path, batchChunks[j].Signature)
			points = append(points, &qdrant.PointStruct{
				Id:      qdrant.NewIDUUID(pointId),
				Vectors: qdrant.NewVectors(emb.Values...),
				Payload: map[string]*qdrant.Value{
					"repo_id":     qdrant.NewValueInt(repoID),
					"file_path":   qdrant.NewValueString(batchChunks[j].Path),
					"content":     qdrant.NewValueString(batchChunks[j].Content),
					"symbol_type": qdrant.NewValueString(batchChunks[j].SymbolType), // Add this
					"signature":   qdrant.NewValueString(batchChunks[j].Signature),  // Add this
				}})
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

	res, err := vs.GeminiClient.Models.EmbedContent(
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
