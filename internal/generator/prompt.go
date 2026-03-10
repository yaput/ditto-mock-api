package generator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

func buildResponsePrompt(endpoint models.Endpoint, req RequestContext) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Generate a realistic mock JSON response for this API endpoint:\n\n"))
	sb.WriteString(fmt.Sprintf("Endpoint: %s %s\n", endpoint.Method, endpoint.Path))
	sb.WriteString(fmt.Sprintf("Description: %s\n", endpoint.Description))
	sb.WriteString(fmt.Sprintf("Expected Status: %d\n", endpoint.StatusCode))

	if len(req.PathParams) > 0 {
		sb.WriteString("\nPath Parameters:\n")
		for k, v := range req.PathParams {
			sb.WriteString(fmt.Sprintf("  %s = %s\n", k, v))
		}
	}

	if len(req.QueryParams) > 0 {
		sb.WriteString("\nQuery Parameters:\n")
		for k, v := range req.QueryParams {
			sb.WriteString(fmt.Sprintf("  %s = %s\n", k, v))
		}
	}

	if req.Body != "" {
		sb.WriteString(fmt.Sprintf("\nRequest Body:\n%s\n", req.Body))
	}

	if endpoint.ResponseBody != nil {
		schema, _ := json.MarshalIndent(endpoint.ResponseBody, "", "  ")
		sb.WriteString(fmt.Sprintf("\nResponse Schema:\n%s\n", schema))
	}

	if endpoint.RequestBody != nil {
		schema, _ := json.MarshalIndent(endpoint.RequestBody, "", "  ")
		sb.WriteString(fmt.Sprintf("\nRequest Body Schema:\n%s\n", schema))
	}

	sb.WriteString("\nRules:\n")
	sb.WriteString("- Return ONLY valid JSON (no markdown, no explanation)\n")
	sb.WriteString("- You MUST strictly follow the Response Schema if provided — use EXACTLY the field names (json_key values) from the schema\n")
	sb.WriteString("- Do NOT add fields that are not in the schema\n")
	sb.WriteString("- Do NOT wrap the response in extra objects not defined in the schema\n")
	sb.WriteString("- Use realistic values (proper names, valid UUIDs, real-looking emails)\n")
	sb.WriteString("- If path params are provided, reflect them in the response\n")
	sb.WriteString("- For list endpoints, return 2-3 items\n")
	sb.WriteString("- Respect the schema field types and required fields\n")

	return sb.String()
}

func systemPrompt() string {
	return "You are a mock API server that generates realistic JSON responses. " +
		"Return ONLY valid JSON — no markdown fences, no explanation, no wrapping."
}
