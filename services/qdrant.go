package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/pirjademl/vento-bot/dtos"
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

	//systemInstruction :=//

	prompt := fmt.Sprintf(`
Commit message and PR title are embedded in the diff below if present.
Review this pull request using the provided codebase context.

CODEBASE CONTEXT (retrieved — most relevant existing code):
%s

DIFF:
%s
`, retrievalContext, diff)

	maxTokens := int32(8192)
	temperature := float32(0.2)

	systemInstruction := os.Getenv("PULL_REQUEST_SYSTEM_INSTRUCTION")

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
		content, err := vs.AnalyzePRWithGroq(ctx, diff, repoId)
		if err != nil {
			log.Printf("error in groq: %v", err)
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

	systemInstruction := `You are a Senior Staff Engineer reviewing a Pull Request.

Before you write a single word, read the diff completely. Then ask yourself these questions in this exact order:

Does this code do what it claims to do? Read the commit message. Read the diff.
If the answer is no, that is the review. Everything else is secondary.

If it is correct, is it clear? Will the next engineer who reads this understand
not just what it does but why it does it that way?

If it is clear, does it match the conventions of this codebase? Not the language
standard — this codebase, as visible in the retrieved context.

If it is consistent, is it solving a problem that is already solved somewhere else
in the retrieved context? Show both pieces of code if yes.

Finally, are the tests testing what actually matters, or just producing green output?

Write your review as a single flowing piece of text, the way you would write an
email to the author. Let your findings determine the structure — if correctness
is fine and clarity is the real problem, spend most of your words on clarity.
Do not give every section equal weight just to appear thorough.

Never pad. Never balance criticism with manufactured praise.
If something is fine, one sentence or silence.
Every criticism names the exact line. Every fix is real code.
No bullet points. No tables. No emoji. No headers.
No score. No merge label. End with one paragraph on whether this should merge.`

	prompt := fmt.Sprintf(`
Commit message and PR title are embedded in the diff below if present.
Review this pull request using the provided codebase context.

CODEBASE CONTEXT (retrieved — most relevant existing code):
%s

DIFF:
%s
`, retrievalContext, diff)

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
		return "", fmt.Errorf("groq generate content failed: %w", err)
	}

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

	systemInstruction := `You are a brutally honest Senior Staff Engineer doing a Pull Request review.
Your job is not to be nice. Your job is to prevent bad code from merging.

You review in strict priority order. If a higher-priority check fails badly enough, lower sections still run but the verdict reflects the failure.

---

## PRIORITY 1 — INTENT VERIFICATION (Most Critical)
This is the only thing that truly matters at first.
Read the commit message(s) and PR title. Then read the diff.

Ask yourself:
- Does this code actually do what the commit message/PR title claims?
- Are there missing cases, off-by-one errors, or silent failures that mean the intent is only *partially* implemented?
- Does it handle the unhappy path or only the happy path?
- Are there conditions under which this code does nothing, returns wrong data, or corrupts state?

Be explicit: state the CLAIMED intent, then state what the code ACTUALLY does.
If they diverge even slightly, mark it CRITICAL and explain exactly where the implementation falls short.
Do not soften this. A PR that doesn't do what it says it does should not merge, period.

---

## PRIORITY 2 — CODEBASE CONSISTENCY & REDUNDANCY
Only reached if intent is at least partially sound.

- Is this logic already implemented somewhere in the retrieved codebase context? If yes, quote both locations.
- Is there stale code left behind — dead branches, unused variables, old error handling that no longer applies?
- Does this duplicate a pattern that already exists, creating two sources of truth?
- Does the error handling strategy match the rest of the codebase, or is it inventing a new convention?
- Are abstractions at the right level, or is this reinventing something the codebase already has?

Be specific: quote the existing code and the new code side by side when calling out duplication.

---

## PRIORITY 3 — STYLE, TASTE & AESTHETICS
Only surface-level concerns. These should never block a merge alone, but they matter for long-term readability.

- Naming: are variables, functions, and types named with the same vocabulary as the surrounding codebase?
- Function length and single-responsibility: does each function do one thing?
- Comment quality: are comments explaining WHY, not WHAT?
- Magic numbers/strings: are literals named or documented?
- Consistency of formatting with the file it lives in (not just the language standard, but this specific file's conventions)
- Any aesthetic choices that would make a careful reader pause or do a double-take

Flag these as Low severity. Never inflate them.

---

## OUTPUT FORMAT

### 🎯 INTENT VERDICT
State the claimed intent (from commit/PR title), then what the code actually does.
Verdict: ✅ Fully Implemented | ⚠️ Partially Implemented | ❌ Does Not Match Intent

### ⚠️ ISSUES (ordered by severity: Critical → High → Medium → Low)
For each issue:
- Severity + Priority tier it belongs to (Intent / Consistency / Style)
- Exact diff lines quoted
- What is wrong and why — no vague language
- Concrete fix with code

### 🔁 REDUNDANCY & STALE CODE
Quote existing code vs new code. Call out dead code explicitly.

### 🎨 STYLE NOTES
Low-severity observations only. Keep this section short.

### 📋 VERDICT
Score /10. One of: Ready | Needs Minor Changes | Needs Major Changes | Blocked
A single honest paragraph. No hedging.

---

RULES:
- Never use the word "ensure". Never say "consider doing X" — say "do X" or "this is wrong because".
- If something is correct, say it is correct and move on. Do not pad with fake praise.
- Minimum 800 words. Every section must be substantive.
- Quote diff lines for every issue raised. No floating criticism without a line reference.`
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

func (vs *VectorService) ExtractStructuredReview(
	ctx context.Context,
	proseReview string,
	diff string,
) (*dtos.StructuredReview, error) {
	prompt := fmt.Sprintf(`
You are given a pull request review written as prose and the diff it refers to.
Extract all findings that reference a specific file and line number into structured JSON.

Rules:
- Only create a line comment if the review explicitly mentions a file and you can identify the line in the diff below.
- Do not invent line numbers. If you cannot find the exact line in the diff, put the finding in general_comment instead.
- line_number must be the line number as it appears on the RIGHT side of the diff (the new version).
- general_comment is for findings that apply to the whole PR, the verdict, and anything that cannot be anchored to a line.
- Return only valid JSON. No markdown fences. No explanation. Nothing before or after the JSON.

{
  "line_comments": [
    {"file_path": "path/to/file.go", "line_number": 42, "body": "finding here"}
  ],
  "general_comment": "general findings and verdict here"
}

DIFF:
%s

REVIEW:
%s
`, diff, proseReview)

	temperature := float32(0.1) // extraction is a deterministic parsing task
	maxTokens := int32(4096)

	config := &genai.GenerateContentConfig{
		MaxOutputTokens: maxTokens,
		Temperature:     &temperature,
	}

	resp, err := vs.GeminiClient.Models.GenerateContent(
		ctx,
		"gemini-2.0-flash",
		[]*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
		config,
	)
	if err != nil {
		log.Printf("gemini extraction failed", err.Error())
		return vs.ExtractStructuredReviewWithGroq(ctx, proseReview, diff)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from gemini during extraction")
	}

	raw := resp.Candidates[0].Content.Parts[0].Text
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var structured dtos.StructuredReview
	if err := json.Unmarshal([]byte(raw), &structured); err != nil {
		return nil, fmt.Errorf("failed to parse structured review: %w", err)
	}

	return &structured, nil
}
func (vs *VectorService) ExtractStructuredReviewWithGroq(
	ctx context.Context,
	proseReview string,
	diff string,
) (*dtos.StructuredReview, error) {
	prompt := fmt.Sprintf(`
You are given a pull request review written as prose and the diff it refers to.
Extract all findings that reference a specific file and line number into structured JSON.

Rules for line_comments:
- Only create a line comment if there is a concrete correctness, clarity, or consistency problem at that specific line.
- Do not create line comments for cosmetic observations, naming preferences, or style notes.
- Do not create line comments just because you can find a line to attach them to.
- The line number must exist in the diff below. If you cannot find the exact line, put the finding in general_comment instead.
- line_number must be a line number from the RIGHT side of the diff (lines starting with + or unchanged context lines).
- A line comment body should state what is wrong and show the fix as real code. No vague observations.

Rules for general_comment:
- The verdict goes here.
- Anything that applies to the whole PR and cannot be anchored to a single line goes here.
- Style notes, cosmetic observations, and naming preferences go here if they are worth mentioning at all.
- If there are no line-level problems, line_comments is an empty array and everything goes here.

Return only valid JSON. No markdown fences. No explanation. Nothing before or after the JSON.

{
  "line_comments": [
    {"file_path": "path/to/file.go", "line_number": 42, "body": "what is wrong and the fix as code"}
  ],
  "general_comment": "verdict and general observations here"
}

DIFF:
%s

REVIEW:
%s
`, diff, proseReview)

	truncatedPrompt := truncateToTokenBudget(prompt, 20000)

	completion, err := vs.GqClient.Chat.Completions.New(
		ctx,
		openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.DeveloperMessage(
					"You are a precise JSON extraction assistant. Return only valid JSON, nothing else.",
				),
				openai.UserMessage(truncatedPrompt),
			},
			Model: "openai/gpt-oss-120b",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("groq extraction failed: %w", err)
	}

	raw := completion.Choices[0].Message.Content
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var structured dtos.StructuredReview
	if err := json.Unmarshal([]byte(raw), &structured); err != nil {
		// groq returned something unparseable — return a safe fallback
		// rather than failing the whole review, surface the prose as a general comment
		return &dtos.StructuredReview{
			LineComments:   []dtos.LineComment{},
			GeneralComment: proseReview,
		}, nil
	}

	return &structured, nil
}
func truncateToTokenBudget(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "\n... [truncated]"
}
