package handlers

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
	"github.com/snappy-loop/stories/internal/auth"
)

// UploadFile handles POST /v1/files (multipart/form-data, field name: file)
func (h *Handler) UploadFile(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	const maxMemory = 32 << 20 // 32MB
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to parse multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "missing or invalid file field (use form field name: file)")
		return
	}
	defer file.Close()

	filename := header.Filename
	if filename == "" {
		filename = "upload"
	}
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	sizeBytes := header.Size

	resp, err := h.fileService.UploadFile(r.Context(), userID, filename, mimeType, file, sizeBytes)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upload file")
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

// ListFiles handles GET /v1/files?status=ready
func (h *Handler) ListFiles(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	status := r.URL.Query().Get("status")

	files, err := h.fileService.ListFiles(r.Context(), userID, status)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list files")
		writeJSONError(w, http.StatusInternalServerError, "failed to list files")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"files": files,
	})
}

// DeleteFile handles DELETE /v1/files/{id}
func (h *Handler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.GetUserID(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	vars := mux.Vars(r)
	fileID, err := uuid.Parse(vars["id"])
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid file id")
		return
	}

	if err := h.fileService.DeleteFile(r.Context(), fileID, userID); err != nil {
		if err.Error() == "file not found" {
			writeJSONError(w, http.StatusNotFound, "file not found")
			return
		}
		log.Error().Err(err).Msg("Failed to delete file")
		writeJSONError(w, http.StatusInternalServerError, "failed to delete file")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
