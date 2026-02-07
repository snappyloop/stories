package handlers

import (
	"encoding/json"
	"net/http"
)

// agentsPageData is passed to the agents page template.
type agentsPageData struct {
	GRPCAvailable bool
	MCPAvailable  bool
}

// AgentsPage serves GET /agents — page with forms to call agents via gRPC or MCP.
// When both URLs are empty, shows "not configured" and hides the Agents nav link.
// When only one protocol is set, hides the other transport option and gRPC-only agent panels if gRPC is off.
func (h *Handler) AgentsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	data := agentsPageData{
		GRPCAvailable: h.agentsGRPCURL != "",
		MCPAvailable:  h.agentsMCPURL != "",
	}
	if err := executeTemplate(w, "agents", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// AgentsCall handles POST /agents/call — calls the agents service (gRPC or MCP) and returns request (redacted) and response.
func (h *Handler) AgentsCall(w http.ResponseWriter, r *http.Request) {
	if h.agentsClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "Agents service not configured (AGENTS_GRPC_URL / AGENTS_MCP_URL)",
		})
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		APIKey    string                 `json:"api_key"`
		Transport string                 `json:"transport"`
		Action    string                 `json:"action"`
		Params    map[string]interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.APIKey == "" {
		writeJSONError(w, http.StatusBadRequest, "api_key required")
		return
	}
	if body.Transport != "grpc" && body.Transport != "mcp" {
		writeJSONError(w, http.StatusBadRequest, "transport must be grpc or mcp")
		return
	}
	if body.Transport == "grpc" && h.agentsGRPCURL == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "gRPC not configured (AGENTS_GRPC_URL empty)",
		})
		return
	}
	if body.Transport == "mcp" && h.agentsMCPURL == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "MCP not configured (AGENTS_MCP_URL empty)",
		})
		return
	}
	if body.Action == "" {
		writeJSONError(w, http.StatusBadRequest, "action required")
		return
	}
	if body.Params == nil {
		body.Params = make(map[string]interface{})
	}
	body.Params["api_key"] = body.APIKey

	reqRedacted, response, err := h.agentsClient.Call(r.Context(), body.APIKey, body.Transport, body.Action, body.Params)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"request":  reqRedacted,
			"response": nil,
			"error":    err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"request":  reqRedacted,
		"response": response,
		"error":    nil,
	})
}

