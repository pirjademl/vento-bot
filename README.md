# Vento Bot

An AI code reviewer that lives inside your GitHub repo. Install it as a GitHub App, and it reviews every pull request and push using context from your *actual codebase* — not just the diff in isolation.

Most AI review bots only look at the changed lines. Vento Bot indexes your whole repo into a vector database first, so when it reviews a PR it can tell you "this duplicates the retry logic in `utils/retry.go`" or "this breaks the error-handling convention used everywhere else in this package." It's reviewing against your codebase's actual patterns, not a generic style guide.

## How it works

1. **You install the GitHub App** on a repo.
2. Vento Bot clones the repo and chunks it — for Go files, it walks the AST and chunks per function/struct/interface/global var, so each chunk is a complete, meaningful unit instead of an arbitrary 50-line slice. Other languages (`.ts`, `.tsx`, `.js`, `.jsx`, `.py`, `.java`) fall back to fixed-size line chunking.
3. Each chunk gets embedded (Gemini `gemini-embedding-2`, truncated to 768 dims) and upserted into Qdrant, scoped by `repo_id`.
4. On every **pull request** (opened/reopened) and every **push**, Vento Bot pulls the diff, embeds it, and queries Qdrant for the most relevant existing code in that repo.
5. That retrieved context + the diff goes to an LLM with a strict reviewer system prompt (intent verification → consistency/redundancy → style, in that priority order). Primary model is Gemini 2.0 Flash; if that call fails, it falls back to Groq (`openai/gpt-oss-120b`) automatically.
6. The prose review gets run through a second extraction pass that pulls out file+line-anchored findings as structured JSON, so issues get posted as **real inline PR review comments** on the exact lines, with a general comment for anything that doesn't anchor to a line. If extraction fails, it gracefully degrades to one big comment instead of losing the review.
7. On every push to the default branch, only the changed/removed files get re-chunked and re-synced into Qdrant — it doesn't re-index the whole repo on every commit.
8. Mention `@vento-bot` in any issue or PR comment and it'll answer using the repo's indexed code, the history of past AI insights on that PR, and the last few comments in the thread for conversational context.

## Architecture

```
GitHub Webhook (installation / push / pull_request / issue_comment)
        │
        ▼
  JWT auth (RS256) → GitHub App installation token
        │
        ▼
   handlers.WebHookHandler (event router)
        │
   ┌────┴─────────────────────────────────┐
   │                                       │
install/push/PR                     issue_comment (@vento-bot)
   │                                       │
   ▼                                       ▼
clone/fetch changed files          fetch past insights + thread
AST-aware chunking (Go) /                  │
line chunking (other langs)                ▼
   │                              SearchRelatedCode (Qdrant)
   ▼                                       │
Gemini embeddings → Qdrant upsert          ▼
   │                              Gemini/Groq → answer
   ▼                                       │
SearchRelatedCode (Qdrant)                 ▼
   │                              posted as issue comment
   ▼
Gemini → (fallback) Groq review
   │
   ▼
Extract structured JSON (line comments + general comment)
   │
   ▼
Posted as inline PR review + general comment
```

## Stack

- **Go** — entire service
- **GitHub Apps API** (`go-github`) — webhooks, installation auth, PR/issue comments, line-level reviews
- **Qdrant** — vector store, per-repo filtered search
- **Google Gemini** — embeddings (`gemini-embedding-2`) and primary review/extraction model (`gemini-2.0-flash`)
- **Groq** (`openai/gpt-oss-120b`) — fallback review and extraction model when Gemini fails
- **PostgreSQL** (`pgx`) — installations, indexed repos, AI insight history
- **JWT (RS256)** — GitHub App authentication

## Features

- Real AST-level chunking for Go — functions, structs, interfaces, imports, and global vars are indexed as distinct, complete units
- Per-repo vector isolation — no cross-repo leakage in retrieval
- Incremental re-indexing — pushes only re-embed what changed, not the whole repo
- Dual-LLM fallback — Gemini first, Groq second, so a single provider outage doesn't kill reviews
- Inline, line-anchored PR comments — not just a wall of text dumped in one comment
- Conversational memory — `@vento-bot` Q&A pulls in past AI insights and recent thread comments
- Graceful degradation — if structured extraction fails, the review still posts as prose instead of disappearing

## Setup

You'll need:

```
DATABASE_URL
DATABASE_USER
DATABASE_NAME
DATABASE_PASSWORD
QDRANT_HOST
QDRANT_API_KEY
GEMINI_API_KEY
GROQ_API_KEY
GROQ_ENDPOINT
CLIENT_ID                          # GitHub App client ID
PEM_FILE_PATH                      # path to your GitHub App private key
PULL_REQUEST_SYSTEM_INSTRUCTION    # reviewer system prompt for the Gemini path
```

```bash
go mod download
go run main.go
```

The server exposes a single webhook endpoint at `/webhook` — point your GitHub App's webhook URL there.

## Status

Actively developed. Currently single-binary, no test suite yet, no deployment config checked in.
