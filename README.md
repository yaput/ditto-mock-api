# Ditto Mock API

An intelligent mock server for Go microservices. Point Ditto at your dependency's Go source code — it scans the code, discovers endpoints and response shapes via AST parsing + LLM analysis, and auto-generates realistic mock responses. Zero specs, zero fixtures.

## Quick Start

```bash
# 1. Set your OpenAI API key
export OPENAI_API_KEY=sk-...

# 2. Edit configs/ditto.yaml with your dependency repos

# 3. Run
make run
```

## How It Works

**Scan Phase (startup):**
1. Parses Go source code using `go/ast` to extract structs, route registrations, and handler functions
2. Auto-detects HTTP framework (chi, gin, echo, gorilla/mux, stdlib)
3. Sends extracted artifacts to OpenAI to resolve ambiguity and produce a structured endpoint registry

**Serve Phase (runtime):**
1. Incoming requests are matched against the endpoint registry
2. Cache is checked (SQLite) — if hit, return immediately
3. On cache miss, LLM generates a realistic mock response
4. Response is cached for future requests

## Configuration

See [configs/ditto.yaml](configs/ditto.yaml) for a complete example.

```yaml
dependencies:
  - name: user-service
    prefix: /api/users       # URL prefix for this dependency
    repo_path: ../user-service  # Path to Go source
    scan_paths:               # Directories to scan (optional)
      - internal/handler
      - internal/model
```

Environment variables are supported via `${VAR_NAME}` syntax.

## Admin API

| Endpoint | Method | Description |
|---|---|---|
| `/_ditto/health` | GET | Health check |
| `/_ditto/registry` | GET | List all dependency registries |
| `/_ditto/registry/{name}` | GET | Full registry for a dependency |
| `/_ditto/scan` | POST | Trigger re-scan of all dependencies |
| `/_ditto/cache` | DELETE | Clear entire cache |
| `/_ditto/cache/stats` | GET | Cache statistics |
| `/_ditto/config` | GET | Current config (API keys redacted) |

## Development

```bash
make build        # Build binary
make test         # Run tests with race detector
make test-verbose # Run tests verbose
make lint         # Run go vet
make tidy         # go mod tidy
make docker       # Build Docker image
```

## Architecture

```
cmd/ditto/          CLI entrypoint
internal/
  config/           YAML config loading + validation
  scanner/          Go AST extraction + LLM analysis
  matcher/          Request-to-endpoint matching
  cache/            SQLite response cache
  generator/        LLM mock response generation
  llm/              OpenAI API client
  server/           HTTP server + admin API + middleware
  models/           Shared domain types
```

## Tech Stack

- **Go** (1.22+)
- **OpenAI** gpt-4o-mini
- **SQLite** via modernc.org/sqlite (pure Go, no CGo)
- Standard library HTTP server

## License

MIT
