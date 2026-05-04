package services

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/genai"
)

var limit uint64 = 5

type VectorService struct {
	QDClient       *qdrant.Client
	GeminiClient   *genai.Client
	GqClient       *openai.Client
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

	gqClient := openai.NewClient(
		option.WithAPIKey(os.Getenv("GROQ_API_KEY")),
		option.WithBaseURL(os.Getenv("GROQ_ENDPOINT")),
	)
	if err != nil {
		log.Fatal("AI client inititalization is failed ", err)
	}

	return &VectorService{
		QDClient:       client,
		GeminiClient:   aiClient,
		CollectionName: collection,
		GqClient:       &gqClient,
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
func (vs *VectorService) CreatePayloadIndex() error {
	cntxt := context.Background()
	res, err := vs.QDClient.CreateFieldIndex(
		cntxt,
		&qdrant.CreateFieldIndexCollection{
			CollectionName: "vento_vectors",
			FieldName:      "file_path",
			FieldType:      qdrant.FieldType_FieldTypeText.Enum(),
		},
	)
	fmt.Println(res.Status)
	if err != nil {
		fmt.Println(err.Error())

	}
	return err

}
func (vs *VectorService) AnalyzePR(ctx context.Context, diff string, repoId int64) (string, error) {
	retrievalContext, err := vs.SearchRelatedCode(ctx, repoId, diff)
	if err != nil {
		return "", err
	}

	systemInstruction := `You are an expert Senior Staff Engineer conducting a thorough Pull Request review.

RULES YOU MUST FOLLOW:
- NEVER truncate or shorten any section. Every section must be fully elaborated.
- Minimum response length is 1000 words. Aim for 1500-2000 words.
- Always quote the specific diff lines you are referencing.
- Always provide concrete code examples in your suggestions — never vague advice.
- Write for two audiences: a junior engineer who needs explanation, and a senior engineer who needs precision.
- Do NOT use phrases like "In summary" or "briefly" — complete every section fully.`

	prompt := fmt.Sprintf(`
You are reviewing a Pull Request as a Senior Staff Engineer. Below is the diff and related codebase context.

---

### 📚 RELATED CODE CONTEXT (Existing Codebase)
%s

---

### 🔀 DIFF (Current Changes)
%s

---

## YOUR TASK

Produce a DETAILED, STRUCTURED review using EXACTLY the following sections. Do NOT skip or shorten any section.

---

### 1. 🔍 DIFF SUMMARY
Write 6-8 sentences explaining:
- What is changing and why
- What problem this diff is solving
- Which parts of the system are affected
- Any non-obvious implications of these changes

### 2. 📚 REFERENCE CODE ANALYSIS
For EACH retrieved reference snippet:
- **File / Context**: Where it comes from and its role in the codebase
- **Relevance**: Why this snippet is related to the diff
- **Patterns Used**: Design patterns, idioms, or conventions visible in the reference
- **Alignment**: How the diff aligns with or diverges from this reference code

### 3. ⚠️ ISSUES & CONCERNS
For EACH issue found (bugs, security, performance, correctness):
- **Severity**: Critical / High / Medium / Low
- **Location**: Quote the exact diff lines
- **Problem**: 3-4 sentences explaining what is wrong and why
- **Impact**: What breaks at runtime, at scale, or during maintenance
- **Fix**: A corrected code snippet with explanation

If no issues exist, explain in detail why the code is robust and what edge cases it handles correctly.

### 4. ✅ WHAT'S DONE WELL
List at least 4-5 specific things done correctly. For each:
- Quote the exact code
- Explain WHY it is good practice
- Mention what problems it prevents

### 5. 🔄 CODEBASE CONSISTENCY CHECK
Compare the diff against the retrieved reference code:
- Naming conventions: Does it match the existing style?
- Error handling: Is it consistent with how errors are handled elsewhere?
- Abstraction level: Does it fit the architecture?
- Logging & observability: Is it consistent?
- Any inconsistencies that would make this code feel "out of place"?

### 6. 🚀 IMPROVEMENT SUGGESTIONS
Provide at least 4-5 actionable suggestions (even if the code is good). For each:
- **What**: What to change
- **Why**: The engineering rationale
- **How**: A concrete code example

### 7. 🧪 TESTING RECOMMENDATIONS
Describe in detail:
- What unit tests should be written
- What edge cases must be covered
- What integration or regression tests are relevant
- Provide at least 3 concrete test case examples with actual code

### 8. 📋 FINAL VERDICT
- **Overall Quality**: Score /10 with full justification
- **Merge Readiness**: Ready | Needs Minor Changes | Needs Major Changes | Blocked
- **Reasoning**: A full paragraph explaining the verdict
`, retrievalContext, diff)

	maxTokens := int32(8192)
	temperature := float32(0.7)

	config := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(systemInstruction, genai.RoleUser),
		MaxOutputTokens:   maxTokens,
		Temperature:       &temperature,
	}

	resp, err := vs.GeminiClient.Models.GenerateContent(
		ctx,
		"gemini-2.0-flash", // much better than flash-lite for detailed output
		[]*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
		config,
	)
	if err != nil {
		content, err := vs.AnalyzePRWithGroq(ctx, diff, repoId)
		if err != nil {
			log.Printf("error in groq", err.Error())
			return "", err
		}
		return content, nil

	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from gemini")
	}

	return resp.Candidates[0].Content.Parts[0].Text, nil
}

func (vs *VectorService) AnalyzePRWithGroq(
	ctx context.Context,
	diff string,
	repoId int64,
) (string, error) {
	retrievalContext, err := vs.SearchRelatedCode(ctx, repoId, diff)
	if err != nil {
		return "", err
	}

	systemInstruction := `You are an expert Senior Staff Engineer conducting a thorough Pull Request review.

RULES YOU MUST FOLLOW:
- NEVER truncate or shorten any section. Every section must be fully elaborated.
- Minimum response length is 1000 words. Aim for 1500-2000 words.
- Always quote the specific diff lines you are referencing.
- Always provide concrete code examples in your suggestions — never vague advice.
- Write for two audiences: a junior engineer who needs explanation, and a senior engineer who needs precision.
- Do NOT use phrases like "In summary" or "briefly" — complete every section fully.`

	prompt := fmt.Sprintf(`
You are reviewing a Pull Request as a Senior Staff Engineer. Below is the diff and related codebase context.

---

### 📚 RELATED CODE CONTEXT (Existing Codebase)
%s

---

### 🔀 DIFF (Current Changes)
%s

---

## YOUR TASK

Produce a DETAILED, STRUCTURED review using EXACTLY the following sections. Do NOT skip or shorten any section.

---

### 1. 🔍 DIFF SUMMARY
Write 6-8 sentences explaining:
- What is changing and why
- What problem this diff is solving
- Which parts of the system are affected
- Any non-obvious implications of these changes

### 2. 📚 REFERENCE CODE ANALYSIS
For EACH retrieved reference snippet:
- **File / Context**: Where it comes from and its role in the codebase
- **Relevance**: Why this snippet is related to the diff
- **Patterns Used**: Design patterns, idioms, or conventions visible in the reference
- **Alignment**: How the diff aligns with or diverges from this reference code

### 3. ⚠️ ISSUES & CONCERNS
For EACH issue found (bugs, security, performance, correctness):
- **Severity**: Critical / High / Medium / Low
- **Location**: Quote the exact diff lines
- **Problem**: 3-4 sentences explaining what is wrong and why
- **Impact**: What breaks at runtime, at scale, or during maintenance
- **Fix**: A corrected code snippet with explanation

If no issues exist, explain in detail why the code is robust and what edge cases it handles correctly.

### 4. ✅ WHAT'S DONE WELL
List at least 4-5 specific things done correctly. For each:
- Quote the exact code
- Explain WHY it is good practice
- Mention what problems it prevents

### 5. 🔄 CODEBASE CONSISTENCY CHECK
Compare the diff against the retrieved reference code:
- Naming conventions: Does it match the existing style?
- Error handling: Is it consistent with how errors are handled elsewhere?
- Abstraction level: Does it fit the architecture?
- Logging & observability: Is it consistent?
- Any inconsistencies that would make this code feel "out of place"?

### 6. 🚀 IMPROVEMENT SUGGESTIONS
Provide at least 4-5 actionable suggestions (even if the code is good). For each:
- **What**: What to change
- **Why**: The engineering rationale
- **How**: A concrete code example

### 7. 🧪 TESTING RECOMMENDATIONS
Describe in detail:
- What unit tests should be written
- What edge cases must be covered
- What integration or regression tests are relevant
- Provide at least 3 concrete test case examples with actual code

### 8. 📋 FINAL VERDICT
- **Overall Quality**: Score /10 with full justification
- **Merge Readiness**: Ready | Needs Minor Changes | Needs Major Changes | Blocked
- **Reasoning**: A full paragraph explaining the verdict
`, retrievalContext, diff)

	completion, err := vs.GqClient.Chat.Completions.New(
		context.Background(),
		openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{

				openai.DeveloperMessage(systemInstruction),
				openai.UserMessage(prompt),
			},
			Model: "openai/gpt-oss-120b",
		},
	)
	if err != nil {
		return "", fmt.Errorf("GROQ generate content failed: %w", err)
	}
	fmt.Println(completion.Choices[0].Message.Content)

	return completion.Choices[0].Message.Content, nil

}

func (vs *VectorService) SearchRelatedCode(
	ctx context.Context,
	repoID int64,
	query string,
) (string, error) {
	// 1. Convert the query into a vector
	queryVector, err := vs.GetEmbedding(ctx, query)
	if err != nil {
		return "", err
	}

	var limit uint64 = 5

	// 2. Query Qdrant with the strict repo_id filter
	searchResult, err := vs.QDClient.Query(ctx, &qdrant.QueryPoints{
		CollectionName: vs.CollectionName,
		Query:          qdrant.NewQueryDense(queryVector),
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
	if err != nil {
		return "", fmt.Errorf("qdrant search failed: %w", err)
	}

	// 3. Format context using the new GetStringValue() helpers
	var contextBuilder strings.Builder
	for _, hit := range searchResult {
		// Accessing the fields we defined in UpsertVectors: "file_path" and "content"
		filePath := hit.Payload["file_path"].GetStringValue()
		content := hit.Payload["content"].GetStringValue()

		// Optional: you can also pull "signature" or "symbol_type" if needed
		symbolType := hit.Payload["symbol_type"].GetStringValue()

		contextBuilder.WriteString(fmt.Sprintf("\n--- File: %s (%s) ---\n%s\n",
			filePath, symbolType, content))
	}

	finalContext := contextBuilder.String()
	fmt.Println("Retrieval complete. Context length:", len(finalContext))

	return finalContext, nil
}
func (vs *VectorService) ProvideAnswerOnComments(
	ctx context.Context,
	question string,
	repoId int64,
	previousInsights []string,
	recentComments []string,
) (string, error) {
	// 1. Retrieve semantically related code from vector DB
	retrievalContext, err := vs.SearchRelatedCode(ctx, repoId, question)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve related code: %w", err)
	}

	systemInstruction := `You are Vento Bot, an expert Senior Staff Engineer assistant embedded in a GitHub Pull Request.
You have access to:
- The codebase context retrieved from a vector database
- Previous AI-generated PR review insights
- The recent conversation history in this PR

RULES:
- Answer the developer's question directly and concisely.
- Always ground your answer in the provided codebase context and previous insights.
- If the question is about a specific file or function, reference it explicitly.
- Provide concrete code examples where applicable.
- If you cannot find relevant context, say so clearly instead of guessing.
- Keep the tone professional but conversational — you are in a PR comment thread.`

	insightsBlock := strings.Join(previousInsights, "\n\n---\n\n")
	commentsBlock := strings.Join(recentComments, "\n")

	prompt := fmt.Sprintf(`
### 📂 RELATED CODEBASE CONTEXT
%s

---

### 🧠 PREVIOUS AI REVIEW INSIGHTS
%s

---

### 💬 RECENT CONVERSATION (last 5 comments)
%s

---

### ❓ DEVELOPER QUESTION
%s

---

Answer the developer's question thoroughly using the context above.
If referencing code, quote it directly. If suggesting a fix, provide a complete snippet.
`, retrievalContext, insightsBlock, commentsBlock, question)

	maxTokens := int32(4096)
	temperature := float32(0.4) // lower temp for Q&A = more precise answers

	config := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(systemInstruction, genai.RoleUser),
		MaxOutputTokens:   maxTokens,
		Temperature:       &temperature,
	}

	resp, err := vs.GeminiClient.Models.GenerateContent(
		ctx,
		"gemini-2.0-flash",
		[]*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
		config,
	)
	if err != nil {
		return vs.ProvideAnswerOnCommentsWithGroq(
			ctx,
			question,
			retrievalContext,
			insightsBlock,
			commentsBlock,
		)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from gemini")
	}

	return resp.Candidates[0].Content.Parts[0].Text, nil
}

func (vs *VectorService) ProvideAnswerOnCommentsWithGroq(
	ctx context.Context,
	question string,
	retrievalContext string,
	insightsBlock string,
	commentsBlock string,
) (string, error) {
	prompt := fmt.Sprintf(`
### 📂 RELATED CODEBASE CONTEXT
%s

---

### 🧠 PREVIOUS AI REVIEW INSIGHTS
%s

---

### 💬 RECENT CONVERSATION (last 5 comments)
%s

---

### ❓ DEVELOPER QUESTION
%s

---

Answer the developer's question thoroughly using the context above.
If referencing code, quote it directly. If suggesting a fix, provide a complete snippet.
`, retrievalContext, insightsBlock, commentsBlock, question)

	systemInstruction := `You are Vento Bot, an expert Senior Staff Engineer assistant embedded in a GitHub Pull Request.
Answer questions concisely and accurately using the provided codebase context and review history.
Always ground answers in the provided context. Provide code examples where applicable.`

	completion, err := vs.GqClient.Chat.Completions.New(
		ctx,
		openai.ChatCompletionNewParams{

			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.DeveloperMessage(systemInstruction),
				openai.UserMessage(prompt),
			},
			Model: "openai/gpt-oss-120b",
		},
	)
	if err != nil {
		return "", fmt.Errorf("groq answer generation failed: %w", err)
	}

	return completion.Choices[0].Message.Content, nil
}
