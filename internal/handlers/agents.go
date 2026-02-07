package handlers

import (
	"encoding/json"
	"net/http"
)

// AgentsPage serves GET /agents — page with forms to call agents via gRPC or MCP.
func (h *Handler) AgentsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(agentsPageHTML))
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

const agentsPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Agents — gRPC / MCP</title>
  <style>
    * { box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; max-width: 560px; margin: 2rem auto; padding: 0 1rem; }
    h1 { font-size: 1.5rem; margin-bottom: 0.5rem; }
    section { margin-bottom: 2rem; padding: 1.25rem; border: 1px solid #e0e0e0; border-radius: 8px; }
    section h2 { font-size: 1.1rem; margin-top: 0; margin-bottom: 1rem; }
    label { display: block; margin-bottom: 0.25rem; font-weight: 500; }
    input, textarea, select { width: 100%; padding: 0.5rem; margin-bottom: 0.75rem; border: 1px solid #ccc; border-radius: 4px; }
    textarea { min-height: 80px; resize: vertical; }
    button { padding: 0.5rem 1rem; background: #333; color: #fff; border: none; border-radius: 4px; cursor: pointer; }
    button:hover { background: #555; }
    button:disabled { opacity: 0.6; cursor: not-allowed; }
    .transport-wrap { margin-bottom: 0.75rem; }
    .transport-wrap label { display: inline; margin-right: 1rem; }
    .transport-wrap input { margin-right: 0.25rem; width: auto; }
    .result { margin-top: 1rem; padding: 0.75rem; background: #f5f5f5; border-radius: 4px; font-size: 0.9rem; white-space: pre-wrap; word-break: break-all; }
    .result.error { background: #fee; color: #c00; }
    .result-media { margin-top: 1rem; }
    .result-media audio { max-width: 100%; }
    .result-media img { max-width: 100%; height: auto; border-radius: 4px; }
    .muted { font-size: 0.85rem; color: #666; }
    .nav-link { margin-right: 1rem; }
    a { color: #333; }
  </style>
</head>
<body>
  <h1>Agents (gRPC / MCP)</h1>
  <p><a href="/" class="nav-link">Tasks</a><a href="/generation" class="nav-link">Generation</a><a href="/agents" class="nav-link">Agents</a></p>

  <section>
    <h2>API Key (shared)</h2>
    <label for="agents-api-key">Use this key for all forms below</label>
    <input type="password" id="agents-api-key" placeholder="API key" autocomplete="off" data-1p-ignore>
  </section>

  <section>
    <h2>Segmentation</h2>
    <form id="form-segment" class="agent-form">
      <div class="transport-wrap">
        <label><input type="radio" name="transport-segment" value="grpc" checked> gRPC</label>
        <label><input type="radio" name="transport-segment" value="mcp"> MCP</label>
      </div>
      <label for="segment-text">Text</label>
      <textarea id="segment-text" name="text" placeholder="Text to segment..."></textarea>
      <label for="segment-segments">Segments count</label>
      <input type="number" id="segment-segments" name="segments_count" value="3" min="1" max="20">
      <label for="segment-type">Input type</label>
      <select id="segment-type" name="input_type">
        <option value="educational">educational</option>
        <option value="financial">financial</option>
        <option value="fictional">fictional</option>
      </select>
      <button type="submit">Segment</button>
    </form>
  </section>

  <section>
    <h2>Narration <span class="muted">(gRPC only)</span></h2>
    <form id="form-narration" class="agent-form">
      <div class="transport-wrap">
        <label><input type="radio" name="transport-narration" value="grpc" checked> gRPC</label>
      </div>
      <label for="narration-text">Text</label>
      <textarea id="narration-text" name="text" placeholder="Text to narrate..."></textarea>
      <label for="narration-audio-type">Audio type</label>
      <select id="narration-audio-type" name="audio_type">
        <option value="free_speech">free_speech</option>
        <option value="podcast">podcast</option>
      </select>
      <label for="narration-input-type">Input type</label>
      <select id="narration-input-type" name="input_type">
        <option value="educational">educational</option>
        <option value="financial">financial</option>
        <option value="fictional">fictional</option>
      </select>
      <button type="submit">Generate narration</button>
    </form>
  </section>

  <section>
    <h2>Audio (TTS) <span class="muted">(gRPC only)</span></h2>
    <form id="form-audio" class="agent-form">
      <div class="transport-wrap">
        <label><input type="radio" name="transport-audio" value="grpc" checked> gRPC</label>
      </div>
      <label for="audio-script">Script</label>
      <textarea id="audio-script" name="script" placeholder="Narration script for TTS..."></textarea>
      <label for="audio-type">Audio type</label>
      <select id="audio-type" name="audio_type">
        <option value="free_speech">free_speech</option>
        <option value="podcast">podcast</option>
      </select>
      <button type="submit">Generate audio</button>
    </form>
  </section>

  <section>
    <h2>Image prompt</h2>
    <form id="form-image-prompt" class="agent-form">
      <div class="transport-wrap">
        <label><input type="radio" name="transport-image-prompt" value="grpc" checked> gRPC</label>
        <label><input type="radio" name="transport-image-prompt" value="mcp"> MCP</label>
      </div>
      <label for="image-prompt-text">Text</label>
      <textarea id="image-prompt-text" name="text" placeholder="Text to describe as image..."></textarea>
      <label for="image-prompt-input-type">Input type</label>
      <select id="image-prompt-input-type" name="input_type">
        <option value="educational">educational</option>
        <option value="financial">financial</option>
        <option value="fictional">fictional</option>
      </select>
      <button type="submit">Generate image prompt</button>
    </form>
  </section>

  <section>
    <h2>Image</h2>
    <form id="form-image" class="agent-form">
      <div class="transport-wrap">
        <label><input type="radio" name="transport-image" value="grpc" checked> gRPC</label>
        <label><input type="radio" name="transport-image" value="mcp"> MCP</label>
      </div>
      <label for="image-prompt">Prompt</label>
      <textarea id="image-prompt" name="prompt" placeholder="Image generation prompt..."></textarea>
      <button type="submit">Generate image</button>
    </form>
  </section>

  <section>
    <h2>Result</h2>
    <p class="muted">Request and response (API key redacted as ***)</p>
    <div id="agents-result-wrap" style="display:none;">
      <div id="agents-result-media" class="result-media"></div>
      <pre id="agents-result-text" class="result"></pre>
    </div>
  </section>

  <script>
    (function() {
      var apiKeyEl = document.getElementById('agents-api-key');
      var resultWrap = document.getElementById('agents-result-wrap');
      var resultMedia = document.getElementById('agents-result-media');
      var resultText = document.getElementById('agents-result-text');
      var ws = null;
      var pendingCallback = null;

      function getWsUrl() {
        var scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
        return scheme + '//' + location.host + '/agents/ws';
      }

      function ensureWs(onOpen) {
        if (ws && ws.readyState === WebSocket.OPEN) {
          if (onOpen) onOpen();
          return;
        }
        if (ws) {
          ws.onopen = null;
          ws.onmessage = null;
          ws.onerror = null;
          ws.onclose = null;
          ws.close();
        }
        resultText.textContent = 'Connecting...';
        resultMedia.innerHTML = '';
        resultWrap.style.display = 'block';
        resultText.classList.remove('error');
        ws = new WebSocket(getWsUrl());
        ws.onopen = function() {
          resultText.textContent = 'Calling...';
          if (onOpen) onOpen();
        };
        ws.onmessage = function(ev) {
          var data;
          try {
            data = JSON.parse(ev.data);
          } catch (e) {
            if (pendingCallback) pendingCallback({ error: 'Invalid JSON from server' });
            pendingCallback = null;
            return;
          }
          if (data.type === 'result' && pendingCallback) {
            pendingCallback(data);
            pendingCallback = null;
          }
        };
        ws.onerror = function() {
          if (pendingCallback) {
            pendingCallback({ error: 'WebSocket error' });
            pendingCallback = null;
          }
        };
        ws.onclose = function() {
          if (pendingCallback) {
            pendingCallback({ error: 'Connection closed' });
            pendingCallback = null;
          }
          ws = null;
        };
      }

      function getTransport(form, transportName) {
        var r = form.querySelector('input[name="' + transportName + '"]:checked');
        return r ? r.value : 'grpc';
      }

      function submitForm(action, formId, transportName, paramNames) {
        var form = document.getElementById(formId);
        var apiKey = apiKeyEl.value.trim();
        if (!apiKey) {
          resultText.textContent = 'Please enter API key at the top.';
          resultText.classList.add('error');
          resultMedia.innerHTML = '';
          resultWrap.style.display = 'block';
          return;
        }
        var transport = getTransport(form, transportName);
        var params = {};
        paramNames.forEach(function(name) {
          var el = form.querySelector('[name="' + name + '"]');
          if (el && el.value !== undefined) {
            if (el.type === 'number') params[name] = parseInt(el.value, 10) || 0;
            else params[name] = el.value;
          }
        });
        var payload = { type: 'call', api_key: apiKey, transport: transport, action: action, params: params };
        resultWrap.style.display = 'block';
        resultText.classList.remove('error');
        resultMedia.innerHTML = '';
        resultText.textContent = 'Calling...';

        var buttons = document.querySelectorAll('.agent-form button[type="submit"]');
        buttons.forEach(function(b) { b.disabled = true; });

        pendingCallback = function(data) {
          buttons.forEach(function(b) { b.disabled = false; });
          resultMedia.innerHTML = '';
          var out = data.request ? ('Request (API key redacted):\n' + JSON.stringify(data.request, null, 2)) : '';
          if (data.error) {
            out += (out ? '\n\n' : '') + 'Error:\n' + data.error;
            resultText.classList.add('error');
          } else if (data.response !== undefined) {
            var ct = data.response.content_type || data.response.mime_type || '';
            var url = data.response.url;
            if (url && (ct.indexOf('audio/') === 0 || ct.indexOf('image/') === 0)) {
              if (ct.indexOf('audio/') === 0) {
                var audio = document.createElement('audio');
                audio.controls = true;
                audio.src = url;
                resultMedia.appendChild(audio);
              } else {
                var img = document.createElement('img');
                img.src = url;
                img.alt = 'Generated image';
                resultMedia.appendChild(img);
              }
            }
            out += '\n\nResponse:\n' + JSON.stringify(data.response, null, 2);
          }
          resultText.textContent = out || 'No response';
        };

        ensureWs(function() {
          ws.send(JSON.stringify(payload));
        });
      }

      document.getElementById('form-segment').addEventListener('submit', function(e) {
        e.preventDefault();
        submitForm('segment_text', 'form-segment', 'transport-segment', ['text', 'segments_count', 'input_type']);
      });
      document.getElementById('form-narration').addEventListener('submit', function(e) {
        e.preventDefault();
        submitForm('generate_narration', 'form-narration', 'transport-narration', ['text', 'audio_type', 'input_type']);
      });
      document.getElementById('form-audio').addEventListener('submit', function(e) {
        e.preventDefault();
        submitForm('generate_audio', 'form-audio', 'transport-audio', ['script', 'audio_type']);
      });
      document.getElementById('form-image-prompt').addEventListener('submit', function(e) {
        e.preventDefault();
        submitForm('generate_image_prompt', 'form-image-prompt', 'transport-image-prompt', ['text', 'input_type']);
      });
      document.getElementById('form-image').addEventListener('submit', function(e) {
        e.preventDefault();
        submitForm('generate_image', 'form-image', 'transport-image', ['prompt']);
      });
    })();
  </script>
</body>
</html>
`
