package services

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/genai"
)

var limit uint64 = 5

type VectorService struct {
	QDClient       *qdrant.Client
	AIClient       *genai.Client
	CollectionName string
}

func NewVectorService(collection string) (*VectorService, error) {
	cntx := context.Background()

	client, err := qdrant.NewClient(&qdrant.Config{
		Host:          os.Getenv("QDRANT_HOST"),
		Port:          6334,
		APIKey:        os.Getenv("QDRANT_API_KEY"),
		UseTLS:        true,
		KeepAliveTime: 60,
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant connection failed: %w", err)
	}
	aiClient, err := genai.NewClient(cntx, &genai.ClientConfig{
		APIKey:  os.Getenv("GEMINI_API_KEY"),
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Fatal("AI client inititalization is failed ", err)
	}

	return &VectorService{
		QDClient:       client,
		AIClient:       aiClient,
		CollectionName: collection,
	}, nil
}
func (vs *VectorService) InitCollection(ctx context.Context) error {
	exists, err := vs.QDClient.CollectionExists(ctx, vs.CollectionName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	return vs.QDClient.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: vs.CollectionName,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     768,
			Distance: qdrant.Distance_Cosine,
		}),
	})
}
func (vs *VectorService) CreateFieldIndex() {
	cntxt := context.Background()
	reuslt, err := vs.QDClient.CreateFieldIndex(cntxt, &qdrant.CreateFieldIndexCollection{
		CollectionName: "vento_vectors",
		FieldName:      "repo_id",
		FieldType:      qdrant.FieldType_FieldTypeInteger.Enum(),
	})
	if err != nil {
		log.Printf("failed to index repo_id", err.Error())
		return
	}

	log.Printf(reuslt.String())

}

func (vs *VectorService) AnalyzePR(ctx context.Context, diff string, repoId int64) (string, error) {

	retrievalContext, err := vs.SearchRelatedCode(ctx, repoId, diff)
	if err != nil {
		return "", err
	}
	prompt := fmt.Sprintf(`
        Act as a Senior Staff Engineer. You are reviewing a Pull Request.
        
        ### GOAL
        Analyze the DIFF provided below. Use the RELATED CODE CONTEXT to understand how these changes 
        interact with existing logic, dependencies, and patterns.

        ### RELATED CODE CONTEXT (Existing Codebase)
        %s

        ### DIFF (Current Changes)
        %s

        ### INSTRUCTIONS
        Provide a concise review covering:
        1. Logical Summary: What is actually changing?
        2. Impact Analysis: How does this affect the related files provided in the context?
        3. Concerns: Identify bugs, security issues, or breaking changes.
    `, retrievalContext, diff)
	fmt.Println(diff)
	fmt.Println(prompt)

	resp, err := vs.AIClient.Models.GenerateContent(ctx, "gemini-3.1-flash-lite-preview",
		[]*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)}, nil)

	return resp.Candidates[0].Content.Parts[0].Text, nil
}

func (vs *VectorService) SearchRelatedCode(
	ctx context.Context,
	repoID int64,
	query string,
) (string, error) {
	// 1. Convert the diff/query into a vector (Embedding)
	queryVector, err := vs.GetEmbedding(ctx, query)
	if err != nil {
		return "", err
	}

	// 2. Query Qdrant for the top 3-5 most similar code chunks
	// Filter by repoID so you don't get code from other users' projects!
	//
	//
	fmt.Println(qdrant.NewQuery(queryVector...))
	searchResult, err := vs.QDClient.Query(ctx, &qdrant.QueryPoints{
		Query:          qdrant.NewQueryDense(queryVector),
		CollectionName: vs.CollectionName,
		Limit:          &limit,
		Filter: &qdrant.Filter{
			Must: []*qdrant.Condition{
				{
					ConditionOneOf: &qdrant.Condition_Field{
						Field: &qdrant.FieldCondition{
							Key: "repo_id",
							Match: &qdrant.Match{
								MatchValue: &qdrant.Match_Integer{Integer: repoID},
							},
						},
					},
				},
			},
		},
		WithPayload: qdrant.NewWithPayload(true),
	})

	// 3. Format the results into a string for the LLM
	var context string
	for _, hit := range searchResult {
		fmt.Println(hit.Payload)
		context += fmt.Sprintf("\n--- File: %s ---\n%s\n",
			hit.Payload["file_path"], hit.Payload["content"])
	}
	fmt.Println("this is the retrieval context", context)

	return context, nil
}
