package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

const (
	agentsWSReadLimit = 64 << 10
)

var agentsWSUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// agentsWSInMessage is the JSON shape sent from the client.
type agentsWSInMessage struct {
	Type      string                 `json:"type"`
	APIKey    string                 `json:"api_key"`
	Transport string                 `json:"transport"`
	Action    string                 `json:"action"`
	Params    map[string]interface{} `json:"params"`
}

// agentsWSOutMessage is the JSON shape sent to the client.
type agentsWSOutMessage struct {
	Type     string      `json:"type"`
	Request  interface{} `json:"request,omitempty"`
	Response interface{} `json:"response,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// AgentsWS handles GET /agents/ws — WebSocket endpoint for long-running agent calls.
func (h *Handler) AgentsWS(w http.ResponseWriter, r *http.Request) {
	if h.agentsClient == nil {
		http.Error(w, "Agents service not configured", http.StatusServiceUnavailable)
		return
	}
	conn, err := agentsWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warn().Err(err).Msg("agents ws upgrade failed")
		return
	}
	defer conn.Close()

	conn.SetReadLimit(agentsWSReadLimit)
	conn.SetReadDeadline(time.Now().Add(60 * time.Minute))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Minute))
		return nil
	})

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			log.Debug().Err(err).Msg("agents ws read")
			return
		}
		conn.SetReadDeadline(time.Now().Add(60 * time.Minute))

		var in agentsWSInMessage
		if err := json.Unmarshal(raw, &in); err != nil {
			_ = writeWSJSON(conn, agentsWSOutMessage{Type: "result", Error: "invalid JSON: " + err.Error()})
			continue
		}
		if in.Type != "call" {
			_ = writeWSJSON(conn, agentsWSOutMessage{Type: "result", Error: "expected type: call"})
			continue
		}
		if in.APIKey == "" {
			_ = writeWSJSON(conn, agentsWSOutMessage{Type: "result", Error: "api_key required"})
			continue
		}
		if in.Transport != "grpc" && in.Transport != "mcp" {
			_ = writeWSJSON(conn, agentsWSOutMessage{Type: "result", Error: "transport must be grpc or mcp"})
			continue
		}
		if in.Action == "" {
			_ = writeWSJSON(conn, agentsWSOutMessage{Type: "result", Error: "action required"})
			continue
		}
		if in.Params == nil {
			in.Params = make(map[string]interface{})
		}
		in.Params["api_key"] = in.APIKey

		reqRedacted, response, callErr := h.agentsClient.Call(context.Background(), in.APIKey, in.Transport, in.Action, in.Params)
		out := agentsWSOutMessage{Type: "result", Request: reqRedacted, Response: response}
		if callErr != nil {
			out.Error = callErr.Error()
		}
		if err := writeWSJSON(conn, out); err != nil {
			log.Debug().Err(err).Msg("agents ws write")
			return
		}
	}
}

func writeWSJSON(conn *websocket.Conn, v interface{}) error {
	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return conn.WriteJSON(v)
}
