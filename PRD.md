# Ditto Mock API — Product Requirements Document (PRD)

**Version:** 2.0
**Date:** March 3, 2026
**Author:** Product & Architecture Lead
**Status:** Ready for Implementation

---

## 1. Executive Summary

**Ditto Mock API** is a developer-productivity service that acts as an intelligent mock server for local development and testing. It **scans Go dependency source code repositories** to automatically discover HTTP endpoints, request/response structs, and route registrations — then uses OpenAI's LLM to generate realistic mock responses on-the-fly. No OpenAPI specs required. Responses are cached in SQLite so identical requests return deterministic results instantly.

**One-liner:** Point Ditto at your dependency's Go repo → it scans the code, discovers endpoints, and auto-generates realistic mock responses. Zero specs, zero fixtures.

**Tech stack:** Go, Go AST (`go/ast` + `go/parser`), SQLite, OpenAI API.

**Key differentiator:** Most mock tools require OpenAPI/Swagger specs. Real-world Go teams rarely maintain those. Ditto works directly with Go source code — the actual source of truth.

---

## 2. Problem Statement

### 2.1 User Pain

Backend and fullstack engineers spend significant time writing, updating, and maintaining mock responses for every dependency endpoint during local development. These hand-written mocks:

- **Drift from reality** as upstream specs change
- **Are tedious to create** — especially for large APIs with dozens of endpoints
- **Are error-prone** — engineers guess at realistic data shapes
- **Multiply per dependency** — a service with 5 dependencies needs 5 sets of mocks
- **No OpenAPI specs exist** — most Go teams don't maintain formal API specs; the contract lives in Go structs and handler code

### 2.2 Jobs-To-Be-Done (JTBD)

> "When I'm developing locally, I want to point a tool at my dependency's Go repo and have it automatically figure out the endpoints and response shapes, so I can get realistic mock responses without writing specs or fixtures."

### 2.3 Target User

| Attribute | Detail |
|---|---|
| **Role** | Backend / Fullstack engineer |
| **Context** | Local development, integration testing, CI pipelines |
| **Skill level** | Comfortable with CLI tools, YAML config, Docker |
| **Team size** | Individual to mid-size teams (1–30 engineers) |

---

## 3. Business Objectives & Success Metrics

| KPI | Target | Measurement |
|---|---|---|
| Time to mock a new dependency | < 5 minutes | From `ditto.yaml` config to first successful mock response |
| Scan accuracy (endpoints discovered) | > 90% | Endpoints correctly mapped vs. actual endpoints in repo |
| Scan duration | < 60 seconds | Time for full scan of a typical Go microservice repo |
| Response field accuracy | > 95% | Response JSON fields match actual Go struct json tags |
| Cache hit rate (cost control) | > 80% after warm-up | Cache hits / total requests |
| Cold-request latency (LLM) | < 3 seconds | p95 for first-time LLM-generated responses |
| Cached-request latency | < 10ms | p95 for cache-hit responses |
| Developer adoption | Team-wide within 1 sprint | Usage tracking via admin endpoints |

---

## 4. Architecture Overview

```
                     ┌─────────────────────────────────────┐
                     │         STARTUP / SCAN PHASE         │
                     │                                     │
  Go Repo(s)         │  ┌─────────────┐  ┌─────────────┐  │
  (dependency        │  │  Go Scanner  │  │ LLM Analyzer│  │
   source code) ────▶│  │  (AST-based) │─▶│ (resolves   │  │
                     │  │  Extracts:   │  │  ambiguity) │  │
                     │  │  • structs   │  │             │  │
                     │  │  • routes    │  └──────┬──────┘  │
                     │  │  • handlers  │         │         │
                     │  └──────────────┘         ▼         │
                     │              ┌─────────────────┐    │
                     │              │ Endpoint Registry│    │
                     │              │ (in-memory index)│    │
                     │              └────────┬────────┘    │
                     └──────────────────────┼─────────────┘
                                            │
┌─────────────┐         ┌──────────────────▼───────────────────┐
│  Your App    │──HTTP──▶│         RUNTIME / SERVE PHASE        │
│  (client)    │◀─resp───│                                      │
└─────────────┘         │  ┌───────────┐   ┌──────────────┐    │
                        │  │  Router    │   │  Matching    │    │
                        │  │(catch-all) │──▶│  Engine      │    │
                        │  └───────────┘   └──────┬───────┘    │
                        │                         │             │
                        │  ┌──────────────────────▼──────────┐  │
                        │  │        Cache Layer (SQLite)      │  │
                        │  └──────────────────────┬──────────┘  │
                        │          miss?          │  hit?→return │
                        │  ┌──────────────────────▼──────────┐  │
                        │  │   LLM Response Generator        │  │
                        │  │   (OpenAI gpt-4o-mini)          │  │
                        │  └─────────────────────────────────┘  │
                        └──────────────────────────────────────┘
```

### 4.1 Two-Phase Architecture

Ditto operates in two distinct phases:

**Phase A: Scan (startup)** — Analyze Go source code to discover endpoints and build an endpoint registry.
**Phase B: Serve (runtime)** — Handle incoming HTTP requests using the registry and LLM.

### 4.2 Component Responsibilities

| Component | Responsibility |
|---|---|
| **Go Scanner** | Parses Go source files using `go/ast` + `go/parser`; extracts struct definitions (with JSON tags), HTTP route registrations, and handler function signatures. Supports chi, gin, echo, gorilla/mux, and stdlib `net/http` router patterns. |
| **LLM Analyzer** | Takes raw AST scan output (structs, routes, handler bodies) and uses OpenAI to resolve ambiguities: which struct is the response for which endpoint, what status codes are returned, nested type resolution, etc. Produces a clean endpoint registry. |
| **Endpoint Registry** | In-memory index of discovered endpoints: `(HTTP method, path pattern) → {request struct schema, response struct schema, status code}`. Persisted to a `.ditto/registry.json` file for fast re-startup. |
| **Router** | Catch-all HTTP handler; receives any method/path/body from the client application |
| **Matching Engine** | Maps an incoming request (method + path) to a registered endpoint, extracting path params and query params |
| **Cache Layer** | SQLite-backed key-value store; key = request signature hash; value = generated response JSON + metadata |
| **LLM Generator** | Builds a structured prompt from the matched endpoint's response struct schema, calls OpenAI, validates the output, stores in cache |
| **Admin API** | Exposes `/_ditto/*` endpoints for health checks, cache management, registry inspection, scan triggering, and configuration |

---

## 5. Detailed Requirements

### 5.1 Configuration (YAML-based)

The service is configured via a single `ditto.yaml` file.

**Example configuration:**

```yaml
server:
  port: 8080
  host: "0.0.0.0"

llm:
  provider: "openai"
  model: "gpt-4o-mini"
  api_key: "${OPENAI_API_KEY}"    # Supports env var substitution
  temperature: 0.7
  max_tokens: 4096
  timeout: 30s
  max_retries: 2

cache:
  enabled: true
  db_path: "./ditto-cache.db"
  ttl: "24h"                      # 0 = never expire

scanner:
  registry_path: "./.ditto/registry.json"   # Persisted scan results
  scan_on_startup: true                      # Re-scan repos on every start
  go_frameworks:                             # Frameworks to detect (auto-detected if omitted)
    - "chi"
    - "gin"
    - "echo"
    - "gorilla"
    - "stdlib"

dependencies:
  - name: "user-service"
    prefix: "/api/users"
    repo_path: "../user-service"             # Path to the Go source repo
    # Optional: narrow down which packages to scan
    scan_paths:
      - "./internal/handler"
      - "./internal/api"
      - "./internal/model"

  - name: "payment-service"
    prefix: "/api/payments"
    repo_path: "../payment-service"
    scan_paths:
      - "./internal/handler"
      - "./pkg/api"

  - name: "notification-service"
    prefix: "/api/notifications"
    repo_path: "../notification-service"
    # If scan_paths omitted, scans entire repo

logging:
  level: "info"                    # debug | info | warn | error
  format: "text"                   # text | json
```

**Requirements:**

| ID | Requirement | Priority |
|---|---|---|
| CFG-1 | Load config from `ditto.yaml` in working directory or path specified via `--config` flag | Must |
| CFG-2 | Support environment variable substitution in config values (`${VAR}` syntax) | Must |
| CFG-3 | Validate config at startup; fail fast with clear error messages | Must |
| CFG-4 | Support multiple dependencies, each with its own path prefix and Go repo path | Must |
| CFG-5 | Sensible defaults for all optional fields (port 8080, cache enabled, etc.) | Must |
| CFG-6 | Optional `scan_paths` per dependency to narrow scanning scope for large repos | Should |
| CFG-7 | `scan_on_startup` flag to control whether repos are re-scanned or registry cache is used | Must |

---

### 5.2 Go Source Code Scanner

The scanner is the core innovation of Ditto. It extracts API contracts directly from Go source code using a two-stage process:

**Stage 1: AST Extraction (mechanical, deterministic)**
Uses Go's `go/ast` and `go/parser` packages to extract raw artifacts from the codebase.

**Stage 2: LLM Analysis (intelligent, resolves ambiguity)**
Feeds the extracted artifacts to OpenAI to produce a clean, structured endpoint registry.

#### 5.2.1 What the AST Scanner Extracts

```
┌─────────────────────────────────────────────────────────────┐
│                    Go Source Repo                            │
│                                                             │
│  ┌─────────────────┐  ┌──────────────────┐  ┌───────────┐  │
│  │ Struct Defs      │  │ Route Registr.   │  │ Handler   │  │
│  │                  │  │                  │  │ Functions │  │
│  │ type User struct │  │ r.Get("/users/   │  │           │  │
│  │ {                │  │   {id}",         │  │ func Get  │  │
│  │   ID   string    │  │   h.GetUser)     │  │ User(...) │  │
│  │   Name string    │  │                  │  │ {         │  │
│  │   ...            │  │ r.Post("/users", │  │  encode(  │  │
│  │ }                │  │   h.CreateUser)  │  │   resp)   │  │
│  │                  │  │                  │  │ }         │  │
│  └─────────────────┘  └──────────────────┘  └───────────┘  │
│         │                      │                   │        │
│         ▼                      ▼                   ▼        │
│  ┌──────────────────────────────────────────────────────┐   │
│  │              Raw Scan Output (JSON)                   │   │
│  │  • All struct types with json tags + field types      │   │
│  │  • All route registration calls with method + path    │   │
│  │  • Handler function signatures + body summaries       │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

**A. Struct Extraction:**

| What | How | Example |
|---|---|---|
| Struct name | `go/ast` TypeSpec | `UserResponse` |
| Field names | StructType.Fields | `ID`, `Name`, `Email` |
| Field types | Field.Type (resolve named types) | `string`, `int64`, `time.Time`, `*Address` |
| JSON tags | Field.Tag (parse `json:"..."`) | `json:"id"`, `json:"name,omitempty"` |
| Nested structs | Recursive resolution | `Profile` inside `UserResponse` |
| Embedded structs | Anonymous fields | `BaseResponse` embedded |
| Validation tags | Field.Tag (parse `validate:"..."`) | `validate:"required,email"` |
| Enums/constants | `const` blocks with iota or string values | `StatusActive`, `StatusInactive` |

**B. Route Registration Extraction:**

Detect route patterns from popular Go HTTP frameworks:

| Framework | Pattern to Detect | Example |
|---|---|---|
| **chi** | `r.Get(`, `r.Post(`, `r.Put(`, `r.Delete(`, `r.Route(`, `r.Group(` | `r.Get("/users/{id}", h.GetUser)` |
| **gin** | `r.GET(`, `r.POST(`, `group.GET(` | `r.GET("/users/:id", h.GetUser)` |
| **echo** | `e.GET(`, `e.POST(`, `g.GET(` | `e.GET("/users/:id", h.GetUser)` |
| **gorilla/mux** | `r.HandleFunc(`, `.Methods("GET")` | `r.HandleFunc("/users/{id}", h.GetUser).Methods("GET")` |
| **stdlib** | `http.HandleFunc(`, `mux.Handle(` | `http.HandleFunc("/users/", handler)` |

For each route, extract: **HTTP method**, **path pattern**, **handler function reference**.

**C. Handler Function Analysis:**

| What | How |
|---|---|
| Function name | FuncDecl.Name |
| Receiver type | FuncDecl.Recv (which struct/service it belongs to) |
| Parameters | FuncDecl.Type.Params (request types) |
| What it decodes | Look for `json.Decoder`, `json.Unmarshal`, `c.Bind`, `c.ShouldBindJSON` calls → identifies request body struct |
| What it encodes | Look for `json.Encoder`, `json.Marshal`, `c.JSON`, `c.Render` calls → identifies response body struct |
| Status codes | Look for `http.StatusOK`, `w.WriteHeader(200)`, `c.JSON(200, ...)` literals |

#### 5.2.2 LLM Analysis Phase

The raw AST output (structs + routes + handler summaries) is sent to OpenAI with a structured prompt to produce the endpoint registry.

**LLM Analysis Prompt Template:**

```
You are an expert Go developer analyzing a microservice codebase.
I will provide you with extracted code artifacts from a Go HTTP service.
Your job is to produce a structured endpoint registry.

## Extracted Structs
{structs_json}

## Extracted Routes  
{routes_json}

## Handler Function Summaries
{handlers_json}

## Task
For each route, determine:
1. The HTTP method and path pattern (normalize path params to {param} format)
2. The request body struct (if any — for POST/PUT/PATCH)
3. The response body struct for the success case
4. The expected success HTTP status code
5. A brief description of what the endpoint does

## Output Format
Return a JSON array:
[
  {
    "method": "GET",
    "path": "/users/{id}",
    "description": "Get a user by ID",
    "request_body": null,
    "response_body": {
      "type": "object",
      "fields": [
        {"name": "id", "type": "string", "json_key": "id", "required": true},
        {"name": "name", "type": "string", "json_key": "name", "required": true},
        {"name": "email", "type": "string", "json_key": "email", "format": "email", "required": true},
        {"name": "created_at", "type": "string", "json_key": "created_at", "format": "date-time", "required": true},
        {"name": "profile", "type": "object", "json_key": "profile", "required": false, "fields": [...]}
      ]
    },
    "status_code": 200
  }
]

Rules:
- Resolve ALL nested struct types recursively into inline field definitions
- Use json tag names as "json_key", not Go field names
- Mark fields without "omitempty" as required: true
- Infer formats from Go types: time.Time → "date-time", uuid.UUID → "uuid"
- If a handler's response struct is ambiguous, make your best inference from the handler body
- Return ONLY valid JSON
```

#### 5.2.3 Endpoint Registry (Output)

The final registry is stored at `.ditto/registry.json`:

```json
{
  "scanned_at": "2026-03-03T10:30:00Z",
  "dependency": "user-service",
  "repo_path": "../user-service",
  "framework_detected": "chi",
  "endpoints": [
    {
      "method": "GET",
      "path": "/users/{id}",
      "description": "Get a user by ID",
      "request_body": null,
      "response_body": {
        "type": "object",
        "fields": [
          {"name": "id", "type": "string", "json_key": "id", "required": true},
          {"name": "name", "type": "string", "json_key": "name", "required": true},
          {"name": "email", "type": "string", "json_key": "email", "format": "email", "required": true},
          {"name": "created_at", "type": "string", "json_key": "created_at", "format": "date-time", "required": true}
        ]
      },
      "status_code": 200
    },
    {
      "method": "POST",
      "path": "/users",
      "description": "Create a new user",
      "request_body": {
        "type": "object",
        "fields": [
          {"name": "name", "type": "string", "json_key": "name", "required": true},
          {"name": "email", "type": "string", "json_key": "email", "format": "email", "required": true}
        ]
      },
      "response_body": {
        "type": "object",
        "fields": [
          {"name": "id", "type": "string", "json_key": "id", "required": true},
          {"name": "name", "type": "string", "json_key": "name", "required": true},
          {"name": "email", "type": "string", "json_key": "email", "format": "email", "required": true}
        ]
      },
      "status_code": 201
    }
  ]
}
```

#### 5.2.4 Scanner Requirements

| ID | Requirement | Priority |
|---|---|---|
| SCAN-1 | Parse all `.go` files in configured repo paths using `go/ast` and `go/parser` | Must |
| SCAN-2 | Extract struct definitions with field names, types, json tags, and nested structs | Must |
| SCAN-3 | Detect route registrations for chi, gin, echo, gorilla/mux, and stdlib `net/http` | Must |
| SCAN-4 | Extract handler function signatures and identify request/response struct usage | Must |
| SCAN-5 | Support `scan_paths` config to narrow scanning to specific packages | Must |
| SCAN-6 | Auto-detect which HTTP framework the repo uses based on import statements | Must |
| SCAN-7 | Handle embedded structs and type aliases in struct extraction | Must |
| SCAN-8 | Send AST scan output to LLM for intelligent mapping (route → request struct → response struct) | Must |
| SCAN-9 | Persist endpoint registry to `.ditto/registry.json` for fast re-startup | Must |
| SCAN-10 | Skip re-scan if registry exists and `scan_on_startup: false` | Must |
| SCAN-11 | Allow manual editing of `registry.json` for corrections/overrides | Should |
| SCAN-12 | Log all discovered endpoints at INFO level during scan | Must |
| SCAN-13 | Handle scan failures gracefully — partial results are usable | Must |
| SCAN-14 | Support re-scan via admin endpoint `POST /_ditto/scan` | Should |
| SCAN-15 | Resolve Go type aliases and named types (e.g., `type UserID = string`) | Should |
| SCAN-16 | Extract constants/enums (iota patterns, string const blocks) for enum-like fields | Should |

#### 5.2.5 LLM Analysis Requirements

| ID | Requirement | Priority |
|---|---|---|
| ANLZ-1 | Send extracted structs + routes + handler summaries to OpenAI in a single structured prompt | Must |
| ANLZ-2 | Handle large repos by chunking: send per-package if token count exceeds model limit | Must |
| ANLZ-3 | Validate LLM output is valid JSON conforming to registry schema; retry on failure | Must |
| ANLZ-4 | Support manual override: if user edits `registry.json`, respect those edits on re-scan (merge strategy) | Should |
| ANLZ-5 | Log the LLM analysis prompt and response at debug level | Must |

---

### 5.3 Request Matching

| ID | Requirement | Priority |
|---|---|---|
| MATCH-1 | Match incoming requests by HTTP method + URL path against endpoint registry entries | Must |
| MATCH-2 | Strip the dependency prefix before matching against the registry's paths | Must |
| MATCH-3 | Support path parameter extraction (e.g., `/users/123` matches `/users/{id}`, `/users/:id`) | Must |
| MATCH-4 | Normalize path param syntax across frameworks: `{id}`, `:id` → unified `{id}` format | Must |
| MATCH-5 | If no match found, return `501 Not Implemented` with a descriptive JSON error body | Must |
| MATCH-6 | If multiple registries could match (overlapping prefixes), use longest-prefix-match | Must |
| MATCH-7 | Pass extracted path params and query params to the LLM generator for context-aware responses | Must |

---

### 5.4 Response Caching

**Cache Key (Request Signature):**
SHA-256 hash of: `method + path + sorted_query_params + canonicalized_request_body`

| ID | Requirement | Priority |
|---|---|---|
| CACHE-1 | Store generated responses in SQLite with schema: `(key_hash, method, path, query, request_body_hash, response_status, response_headers, response_body, created_at, expires_at)` | Must |
| CACHE-2 | On cache hit, return stored response immediately (skip LLM) | Must |
| CACHE-3 | On cache miss, generate via LLM, store, then return | Must |
| CACHE-4 | Support TTL-based expiration (configurable, default 24h, 0 = infinite) | Must |
| CACHE-5 | Admin endpoint to clear entire cache: `DELETE /_ditto/cache` | Must |
| CACHE-6 | Admin endpoint to clear cache for a specific dependency: `DELETE /_ditto/cache/{dependency}` | Should |
| CACHE-7 | Admin endpoint to view cache stats: `GET /_ditto/cache/stats` | Should |
| CACHE-8 | Cache DB auto-created on first run; no manual setup required | Must |

---

### 5.5 LLM Response Generation (OpenAI)

The response generator uses the endpoint registry (produced by the scanner) to build prompts.

**Prompt Template:**

```
You are a mock API server. Generate a realistic JSON response that strictly
conforms to the following Go struct-based response schema.

## Endpoint
- Description: {description}
- HTTP Method: {method}
- Path: {path}
- Status Code: {statusCode}

## Request Info (for context)
- Path Parameters: {pathParams}
- Query Parameters: {queryParams}
- Request Body: {requestBody}

## Response Schema (derived from Go structs)
{responseFields}

## Rules
1. Return ONLY valid JSON — no markdown, no explanation, no wrapping
2. Populate ALL fields marked as required
3. Use realistic but fake data:
   - Names: realistic human names
   - Emails: format like user@example.com
   - UUIDs: valid v4 UUIDs
   - Dates: ISO 8601 format (time.Time → "2026-03-03T10:30:00Z")
   - URLs: use https://example.com base
   - Phone numbers: valid format with fake numbers
   - IDs: if path param has an ID value, use that same value in the response
4. Respect all constraints: enums, formats, min/max values
5. For arrays (Go slices), generate 2-3 items
6. For pointer fields (*Type / omitempty), include with realistic data unless context suggests null
7. Use json tag names as JSON keys (not Go field names)
8. If the request body provides specific values, reflect those in the response where logical
```

| ID | Requirement | Priority |
|---|---|---|
| LLM-1 | Use OpenAI API (gpt-4o-mini default) for response generation | Must |
| LLM-2 | Build structured prompt from matched endpoint's response schema (from registry) | Must |
| LLM-3 | Include request context (path params, query params, body) in prompt for realistic responses | Must |
| LLM-4 | Validate generated response is valid JSON; retry up to `max_retries` times on failure | Must |
| LLM-5 | Set configurable timeout for LLM calls (default 30s) | Must |
| LLM-6 | If LLM fails after retries, return `502 Bad Gateway` with descriptive error | Must |
| LLM-7 | Log LLM request/response at debug level for troubleshooting | Must |
| LLM-8 | Support configurable model, temperature, and max_tokens | Must |
| LLM-9 | Use JSON mode / structured output if available for the selected model | Should |
| LLM-10 | Echo path parameter values (e.g., requested ID) back in the response for consistency | Should |

---

### 5.6 Admin API

All admin endpoints are served under the `/_ditto` prefix.

| Endpoint | Method | Description | Priority |
|---|---|---|---|
| `/_ditto/health` | GET | Health check; returns `{"status": "ok"}` | Must |
| `/_ditto/registry` | GET | List all endpoint registries with endpoint counts per dependency | Must |
| `/_ditto/registry/{dependency}` | GET | Show full endpoint registry for a specific dependency | Must |
| `/_ditto/scan` | POST | Trigger a re-scan of all dependency repos | Should |
| `/_ditto/scan/{dependency}` | POST | Trigger re-scan of a specific dependency repo | Should |
| `/_ditto/cache` | DELETE | Clear entire response cache | Must |
| `/_ditto/cache/{dependency}` | DELETE | Clear cache for specific dependency | Should |
| `/_ditto/cache/stats` | GET | Cache statistics (total entries, hit rate, size) | Should |
| `/_ditto/config` | GET | Return current running config (redact API keys) | Should |

---

### 5.7 HTTP Server

| ID | Requirement | Priority |
|---|---|---|
| SRV-1 | Serve on configurable host:port (default `0.0.0.0:8080`) | Must |
| SRV-2 | Catch-all handler: any method, any path not under `/_ditto` is treated as a mock request | Must |
| SRV-3 | Return proper `Content-Type: application/json` headers on all mock responses | Must |
| SRV-4 | Return appropriate HTTP status codes from the spec (200, 201, etc.) | Must |
| SRV-5 | Graceful shutdown on SIGINT/SIGTERM | Must |
| SRV-6 | Request logging with method, path, status, latency, cache hit/miss | Must |
| SRV-7 | CORS headers enabled by default (permissive for local dev) | Should |

---

### 5.8 Logging & Observability

| ID | Requirement | Priority |
|---|---|---|
| LOG-1 | Structured logging with configurable level (debug/info/warn/error) | Must |
| LOG-2 | Log format: text (human-readable) or JSON (machine-readable) | Must |
| LOG-3 | Log on startup: loaded specs, operation counts, listening address | Must |
| LOG-4 | Log per request: method, path, matched operation, cache hit/miss, latency | Must |
| LOG-5 | Log LLM interactions at debug level | Must |

---

## 6. Project Structure

```
ditto-mock-api/
├── cmd/
│   └── ditto/
│       └── main.go                  # Entry point, CLI flags, bootstrap
├── internal/
│   ├── config/
│   │   ├── config.go                # Config struct & YAML loader
│   │   └── config_test.go
│   ├── server/
│   │   ├── server.go                # HTTP server setup & lifecycle
│   │   ├── handler.go               # Catch-all mock request handler
│   │   ├── admin.go                 # /_ditto/* admin endpoints
│   │   └── middleware.go            # Logging, CORS, recovery middleware
│   ├── scanner/
│   │   ├── scanner.go               # Main scanner orchestrator
│   │   ├── ast_extractor.go         # Go AST parsing (structs, routes, handlers)
│   │   ├── framework_detect.go      # Auto-detect chi/gin/echo/gorilla/stdlib
│   │   ├── analyzer.go              # LLM-based analysis (AST output → registry)
│   │   ├── registry.go              # Endpoint registry type + persistence
│   │   └── scanner_test.go
│   ├── matcher/
│   │   ├── matcher.go               # Request → Endpoint matching
│   │   └── matcher_test.go
│   ├── generator/
│   │   ├── generator.go             # Generator interface
│   │   ├── openai.go                # OpenAI implementation
│   │   ├── prompt.go                # Prompt builder & templates
│   │   └── openai_test.go
│   ├── cache/
│   │   ├── cache.go                 # Cache interface
│   │   ├── sqlite.go                # SQLite implementation
│   │   └── sqlite_test.go
│   └── models/
│       └── models.go                # Shared domain types
├── configs/
│   └── ditto.yaml                   # Example/default config
├── .ditto/                          # Generated at runtime
│   └── registry.json                # Persisted scan results (gitignored)
├── go.mod
├── go.sum
├── Dockerfile
├── Makefile
├── .gitignore
└── README.md
```

---

## 7. Go Dependencies (Key Libraries)

| Library | Purpose | Rationale |
|---|---|---|
| `net/http` (stdlib) | HTTP server | Zero dependencies, production-grade |
| `go/ast`, `go/parser`, `go/token` (stdlib) | Go source code parsing | Native AST parsing; no external dependency needed |
| `github.com/sashabaranov/go-openai` | OpenAI API client | Well-maintained, idiomatic Go client |
| `modernc.org/sqlite` | SQLite driver | Pure Go (no CGo), cross-platform, easy to build |
| `gopkg.in/yaml.v3` | YAML config parsing | Standard Go YAML library |
| `github.com/rs/zerolog` | Structured logging | Fast, zero-allocation, supports text & JSON output |

---

## 8. Request Flow (Detailed)

### 8.1 Startup / Scan Flow

```
1. Load ditto.yaml config

2. For each dependency:
   a. Check if .ditto/registry.json exists AND scan_on_startup is false
      → If yes, load cached registry (fast path)
   b. Otherwise, run full scan:
      i.   Walk configured repo_path (or scan_paths)
      ii.  Parse all .go files with go/ast
      iii. Extract: structs (with json tags), route registrations, handler functions
      iv.  Auto-detect HTTP framework from import statements
      v.   Send extracted artifacts to OpenAI for intelligent mapping
      vi.  Receive structured endpoint registry
      vii. Validate and persist to .ditto/registry.json

3. Build in-memory endpoint index from all registries

4. Initialize SQLite cache

5. Start HTTP server
   → Log: "Loaded user-service: 8 endpoints (scanned from ../user-service)"
   → Log: "Loaded payment-service: 12 endpoints (from cached registry)"
   → Log: "Server listening on 0.0.0.0:8080"
```

### 8.2 Runtime / Request Flow

```
1. Client sends: GET /api/users/123?include=profile

2. Router receives request (catch-all handler)
   → Not /_ditto/* prefix → treat as mock request

3. Matching Engine:
   a. Find dependency by prefix: "/api/users" → user-service
   b. Strip prefix: "/api/users/123?include=profile" → "/123?include=profile"
   c. Match against registry: GET /123 → GET /{id} in user-service registry
   d. Extract: pathParams={id: "123"}, queryParams={include: "profile"}
   e. Retrieve response schema from registry entry

4. Cache Lookup:
   a. Compute key: SHA256("GET" + "/api/users/123" + "include=profile" + "")
   b. Query SQLite: SELECT * FROM cache WHERE key_hash = ? AND expires_at > NOW()
   c. If HIT → return cached response (skip to step 6)

5. LLM Generation (cache MISS):
   a. Build prompt with endpoint metadata + response struct schema + request context
   b. Call OpenAI API (gpt-4o-mini)
   c. Validate response is valid JSON
   d. If invalid → retry (up to max_retries)
   e. Store in SQLite cache

6. Return Response:
   → HTTP 200
   → Content-Type: application/json
   → Body: {"id": "123", "name": "Sarah Chen", "email": "sarah.chen@example.com", ...}
```

---

## 9. Error Handling Strategy

| Scenario | HTTP Status | Response Body |
|---|---|---|
| No matching dependency (prefix) | 404 | `{"error": "no_dependency_match", "message": "No dependency configured for path /api/unknown", "available_prefixes": ["/api/users", "/api/payments"]}` |
| Dependency found, no matching operation | 501 | `{"error": "no_operation_match", "message": "No operation matches GET /foo in user-service spec", "available_operations": ["GET /users", "GET /users/{id}", ...]}` |
| LLM generation failed (all retries) | 502 | `{"error": "generation_failed", "message": "Failed to generate mock response after 3 attempts", "detail": "..."}` |
| Invalid config / spec parse error | — | Fail fast at startup with clear stderr message |
| LLM timeout | 504 | `{"error": "generation_timeout", "message": "LLM response generation timed out after 30s"}` |

All error responses include the `X-Ditto-Error: true` header for easy identification.

---

## 10. User Stories

### US-1: Scan & Mock Setup (Primary Flow)
**As a** developer,
**I want to** point Ditto at my dependency's Go repo and have it automatically discover endpoints and response shapes,
**So that** I can get mock responses without writing specs or fixtures.

**Acceptance Criteria:**
- [ ] I can create a `ditto.yaml` with one dependency pointing to a local Go repo
- [ ] Running `ditto` scans the repo, discovers endpoints, and starts the server
- [ ] Startup logs show discovered endpoints: "Scanned user-service: GET /users/{id}, POST /users, ..."
- [ ] My app's HTTP calls to `localhost:8080/api/users/123` return valid mock JSON
- [ ] The response fields match the Go structs in the dependency repo

### US-2: Cached Responses
**As a** developer,
**I want** repeated identical requests to return the same response instantly,
**So that** my tests are deterministic and I don't waste LLM API calls.

**Acceptance Criteria:**
- [ ] First request generates via LLM and caches
- [ ] Second identical request returns from cache (< 10ms)
- [ ] Cache persists across server restarts (SQLite file)
- [ ] I can clear cache via `DELETE /_ditto/cache`

### US-3: Multiple Dependencies
**As a** developer working on a service with multiple dependencies,
**I want to** configure multiple Go repo paths with different path prefixes,
**So that** all my dependencies are mocked from a single Ditto instance.

**Acceptance Criteria:**
- [ ] Config supports multiple entries in `dependencies` array, each with its own `repo_path`
- [ ] Each dependency is scanned independently
- [ ] Each dependency is matched by longest path prefix
- [ ] Requests route to the correct registry based on prefix

### US-4: Fast Restart with Cached Registry
**As a** developer,
**I want** Ditto to remember previous scan results so it starts up instantly,
**So that** I don't wait for a full re-scan every time I restart.

**Acceptance Criteria:**
- [ ] After first scan, `.ditto/registry.json` is created
- [ ] If `scan_on_startup: false`, Ditto loads from registry file (< 1 second startup)
- [ ] I can force a re-scan via `POST /_ditto/scan`
- [ ] I can manually edit `registry.json` to correct/override endpoints

### US-5: Troubleshooting
**As a** developer,
**I want** clear error messages when something doesn't match,
**So that** I can quickly diagnose and fix configuration issues.

**Acceptance Criteria:**
- [ ] Unmatched paths return 404/501 with helpful details
- [ ] Error response lists available prefixes or operations
- [ ] Startup logs show all scanned endpoints per dependency
- [ ] Request logs show cache hit/miss and latency

### US-6: Admin Operations
**As a** developer,
**I want** admin endpoints to inspect and manage Ditto's state,
**So that** I can debug issues and reset state when needed.

**Acceptance Criteria:**
- [ ] `GET /_ditto/health` returns 200
- [ ] `GET /_ditto/registry` lists all dependencies and their discovered endpoints
- [ ] `DELETE /_ditto/cache` clears all cached responses
- [ ] `POST /_ditto/scan` triggers a fresh re-scan of all repos

---

## 11. Risks & Mitigations

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| 1 | **LLM generates invalid JSON (response generation)** | Medium | High | JSON validation + retry loop (up to max_retries); use JSON mode when available |
| 2 | **LLM misidentifies struct-to-endpoint mapping (scan phase)** | Medium | High | Allow manual override via `registry.json` editing; log all mappings for review; re-scan capability |
| 3 | **AST scanner misses routes (unusual framework usage)** | Medium | Medium | Support top 5 frameworks; fallback to LLM-only analysis of raw source files for unrecognized patterns |
| 4 | **Large repos exceed LLM token limits during scan** | Medium | Medium | Chunk by package; send most relevant packages first; summarize less relevant code |
| 5 | **LLM latency slows development (runtime)** | Medium | Medium | Aggressive response caching; gpt-4o-mini for speed |
| 6 | **OpenAI API cost (scan + generation)** | Medium | Low | Cache-first architecture; persist registry to avoid re-scan; gpt-4o-mini is cost-efficient |
| 7 | **Complex Go patterns (interfaces, generics, embedded types)** | Medium | Medium | Handle common patterns in AST; delegate edge cases to LLM analysis |
| 8 | **Dependency repo code changes** | High | Low | Re-scan via admin endpoint or `scan_on_startup: true`; registry diffing in V2 |
| 9 | **SQLite concurrency under load** | Low | Low | Single-writer with WAL mode; adequate for local dev workloads |

---

## 12. Out of Scope (V2 / Future)

These are explicitly **NOT** in the MVP but are tracked for future iterations:

| Feature | Rationale for Deferral |
|---|---|
| Git repo cloning / remote repo scanning | Adds complexity; local file paths are sufficient for MVP |
| gRPC / protobuf support | Different parsing stack; HTTP/REST covers majority of use cases |
| OpenAPI spec ingestion (as alternative to Go scanning) | Can be added as a second "spec source" type alongside Go scanning |
| Response override / pinning (manual fixtures) | Nice-to-have; LLM + cache + registry editing covers the primary use case |
| Web UI dashboard | CLI + admin API is sufficient for developer audience |
| Record & replay mode (proxy to real service) | Different architecture pattern; separate feature |
| Multiple LLM providers (Anthropic, Ollama) | Adapter interface will be in place; only OpenAI implemented in MVP |
| Auto-detect repo changes & incremental re-scan | File watcher adds complexity; manual re-scan via admin API is sufficient |
| Non-Go language support (Java, Python, TypeScript) | Go-first; other languages can be added with additional AST parsers |
| Request body validation against scanned structs | Read-only mock server; validating incoming requests is out of scope |
| Registry diffing (show what changed between scans) | Useful but not critical for MVP |

---

## 13. Implementation Priorities (Suggested Order)

| Phase | Components | Estimated Effort |
|---|---|---|
| **Phase 1: Foundation** | Config loader, project structure, go.mod, Makefile | 1 hour |
| **Phase 2: AST Scanner** | Go file walker, struct extractor, route extractor, handler analyzer, framework detection | 3–4 hours |
| **Phase 3: LLM Analyzer** | Prompt builder for scan analysis, registry output parsing, registry persistence | 2–3 hours |
| **Phase 4: Request Matcher** | Path matching, param extraction, prefix-based dependency routing | 1–2 hours |
| **Phase 5: Cache** | SQLite cache with full CRUD | 1–2 hours |
| **Phase 6: LLM Response Generator** | OpenAI integration for response generation, prompt builder, JSON validation | 2–3 hours |
| **Phase 7: Server** | HTTP server, catch-all handler, middleware, admin API | 2–3 hours |
| **Phase 8: Integration** | Wire all components, end-to-end flow, example test repo | 2–3 hours |
| **Phase 9: Polish** | Dockerfile, README, Makefile targets, example config | 1 hour |

**Total estimated: ~15–22 hours of implementation**

---

## 14. How to Use (Target Developer Experience)

```bash
# 1. Install
go install github.com/your-org/ditto-mock-api/cmd/ditto@latest

# 2. Create config — just point at your dependency repos!
cat > ditto.yaml <<EOF
server:
  port: 8080
llm:
  provider: openai
  api_key: "\${OPENAI_API_KEY}"
dependencies:
  - name: user-service
    prefix: /api/users
    repo_path: ../user-service          # ← just point at the Go repo
    scan_paths:
      - ./internal/handler
      - ./internal/model
  - name: payment-service
    prefix: /api/payments
    repo_path: ../payment-service
EOF

# 3. Run (first time: scans repos, subsequent: uses cached registry)
export OPENAI_API_KEY=sk-...
ditto

# Output:
# INFO  Loading config from ditto.yaml
# INFO  Scanning user-service at ../user-service...
# INFO    Framework detected: chi
# INFO    Found 15 structs, 8 route registrations, 8 handler functions
# INFO    LLM analysis complete: 8 endpoints mapped
# INFO    Registry saved to .ditto/registry.json
# INFO  Scanning payment-service at ../payment-service...
# INFO    Framework detected: gin
# INFO    Found 22 structs, 12 route registrations, 12 handler functions
# INFO    LLM analysis complete: 12 endpoints mapped
# INFO  Cache: SQLite at ./ditto-cache.db
# INFO  Server listening on 0.0.0.0:8080
# INFO
# INFO  Endpoints:
# INFO    user-service    (8 endpoints)  /api/users/*
# INFO    payment-service (12 endpoints) /api/payments/*

# 4. Point your app's base URL to localhost:8080 and develop!
curl http://localhost:8080/api/users/123
# → {"id": "123", "name": "Sarah Chen", "email": "sarah.chen@example.com", ...}

# 5. Inspect what was discovered
curl http://localhost:8080/_ditto/registry/user-service
# → Full endpoint registry with all discovered endpoints and response schemas

# 6. Re-scan after dependency code changes
curl -X POST http://localhost:8080/_ditto/scan
# → {"status": "ok", "scanned": ["user-service", "payment-service"]}

# 7. Override a response schema if LLM got it wrong
# Edit .ditto/registry.json manually, then restart (or hot-reload in V2)
```

---

## 15. Definition of Done (MVP)

- [ ] `go build ./cmd/ditto` produces a working binary
- [ ] Config loading from YAML with env var substitution works
- [ ] AST scanner can parse Go files and extract structs, routes, and handlers
- [ ] Framework auto-detection works for chi, gin, echo, gorilla/mux, and stdlib
- [ ] LLM analyzer maps scan output to structured endpoint registry
- [ ] Endpoint registry persists to `.ditto/registry.json` and loads on restart
- [ ] Catch-all HTTP handler matches requests to registered endpoints
- [ ] LLM generates valid JSON responses conforming to discovered Go struct schemas
- [ ] Responses are cached in SQLite; identical requests return cached results
- [ ] Admin endpoints (`health`, `registry`, `cache clear`, `scan`) are functional
- [ ] Structured logging at all key points
- [ ] Graceful shutdown on SIGINT/SIGTERM
- [ ] README with setup instructions
- [ ] Dockerfile for containerized usage
- [ ] Example config included in repo

---

## 16. Appendix: Scanner Technical Deep-Dive

### A. Go Type → JSON Schema Mapping

| Go Type | JSON Representation | Notes |
|---|---|---|
| `string` | `"string"` | |
| `int`, `int32`, `int64` | `number` (integer) | |
| `float32`, `float64` | `number` (float) | |
| `bool` | `boolean` | |
| `time.Time` | `"string"` format `date-time` | ISO 8601 |
| `uuid.UUID` | `"string"` format `uuid` | v4 UUID |
| `[]T` | `array` of T | |
| `map[string]T` | `object` with value type T | |
| `*T` | T (nullable) | Maps to `omitempty` behavior |
| `json.RawMessage` | `any` | LLM generates contextual data |
| `interface{}` / `any` | `any` | LLM generates contextual data |
| Named type (e.g., `type Status string`) | Resolve to underlying type | Extract const values as enum |
| Embedded struct | Inline all fields | Flatten into parent |

### B. Framework Detection Heuristics

The scanner checks import statements across all Go files:

| Import Path Contains | Framework |
|---|---|
| `github.com/go-chi/chi` | chi |
| `github.com/gin-gonic/gin` | gin |
| `github.com/labstack/echo` | echo |
| `github.com/gorilla/mux` | gorilla |
| None of the above + uses `net/http` | stdlib |

### C. Scan Output Schema (what gets sent to LLM)

```json
{
  "repo": "user-service",
  "framework": "chi",
  "structs": [
    {
      "name": "User",
      "package": "model",
      "file": "internal/model/user.go",
      "fields": [
        {"name": "ID", "type": "string", "json_tag": "id", "omitempty": false},
        {"name": "Name", "type": "string", "json_tag": "name", "omitempty": false},
        {"name": "Email", "type": "string", "json_tag": "email", "omitempty": false},
        {"name": "Profile", "type": "*Profile", "json_tag": "profile", "omitempty": true},
        {"name": "CreatedAt", "type": "time.Time", "json_tag": "created_at", "omitempty": false}
      ]
    },
    {
      "name": "CreateUserRequest",
      "package": "handler",
      "file": "internal/handler/user.go",
      "fields": [
        {"name": "Name", "type": "string", "json_tag": "name", "omitempty": false},
        {"name": "Email", "type": "string", "json_tag": "email", "omitempty": false}
      ]
    }
  ],
  "routes": [
    {"method": "GET", "path": "/users/{id}", "handler": "UserHandler.GetUser", "file": "internal/handler/routes.go", "line": 25},
    {"method": "POST", "path": "/users", "handler": "UserHandler.CreateUser", "file": "internal/handler/routes.go", "line": 26}
  ],
  "handlers": [
    {
      "name": "GetUser",
      "receiver": "UserHandler",
      "file": "internal/handler/user.go",
      "decodes": null,
      "encodes": "User",
      "status_codes": [200, 404]
    },
    {
      "name": "CreateUser",
      "receiver": "UserHandler",
      "file": "internal/handler/user.go",
      "decodes": "CreateUserRequest",
      "encodes": "User",
      "status_codes": [201, 400]
    }
  ]
}
```

---

*This PRD is ready to be passed to the implementation agent. All decisions have been made — no ambiguity remains. Build it.*
