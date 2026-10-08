package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"alfaos/alfad/internal/ai"
	"alfaos/alfad/internal/store"
)

func (s *Server) aiError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, ai.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, ai.ErrNoProvider):
		writeError(w, http.StatusServiceUnavailable, "no_provider", err.Error())
	case errors.Is(err, ai.ErrBusy):
		writeError(w, http.StatusConflict, "busy", err.Error())
	case errors.Is(err, ai.ErrNotLocal):
		writeError(w, http.StatusBadRequest, "not_local", err.Error())
	default:
		s.internalError(w, r, err)
	}
}

// ---------- providers ----------

func (s *Server) aiListProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := s.AI.Providers(r.Context())
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) aiCreateProvider(w http.ResponseWriter, r *http.Request) {
	var in ai.ProviderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := s.AI.SaveProvider(r.Context(), "", in)
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "ai.provider.create", v.ID, true, v.Kind)
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) aiUpdateProvider(w http.ResponseWriter, r *http.Request) {
	var in ai.ProviderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := s.AI.SaveProvider(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "ai.provider.update", v.ID, true, "")
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) aiDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.AI.DeleteProvider(r.Context(), id); err != nil {
		s.aiError(w, r, err)
		return
	}
	s.audit(r, userFrom(r.Context()).ID, "ai.provider.delete", id, true, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) aiModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.AI.Models(r.Context(), r.URL.Query().Get("provider"))
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (s *Server) aiRunning(w http.ResponseWriter, r *http.Request) {
	models, err := s.AI.RunningModels(r.Context(), r.URL.Query().Get("provider"))
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, models)
}

type modelRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (s *Server) aiLoadModel(w http.ResponseWriter, r *http.Request) {
	var req modelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.AI.LoadModel(r.Context(), req.Provider, req.Model); err != nil {
		s.aiError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) aiUnloadModel(w http.ResponseWriter, r *http.Request) {
	var req modelRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.AI.UnloadModel(r.Context(), req.Provider, req.Model); err != nil {
		s.aiError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- conversations ----------

func (s *Server) aiListConversations(w http.ResponseWriter, r *http.Request) {
	list, err := s.AI.ListConversations(r.Context(), userFrom(r.Context()).ID, r.URL.Query().Get("q"))
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createConversationRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (s *Server) aiCreateConversation(w http.ResponseWriter, r *http.Request) {
	var req createConversationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := s.AI.CreateConversation(r.Context(), userFrom(r.Context()).ID, req.Provider, req.Model)
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) aiGetConversation(w http.ResponseWriter, r *http.Request) {
	c, err := s.AI.Conversation(r.Context(), userFrom(r.Context()).ID, r.PathValue("id"))
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

type patchConversationRequest struct {
	Title  *string `json:"title"`
	Pinned *bool   `json:"pinned"`
}

func (s *Server) aiPatchConversation(w http.ResponseWriter, r *http.Request) {
	var req patchConversationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	uid, id := userFrom(r.Context()).ID, r.PathValue("id")
	if req.Title != nil {
		if err := s.AI.RenameConversation(r.Context(), uid, id, *req.Title); err != nil {
			s.aiError(w, r, err)
			return
		}
	}
	if req.Pinned != nil {
		if err := s.AI.SetPinned(r.Context(), uid, id, *req.Pinned); err != nil {
			s.aiError(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) aiDeleteConversation(w http.ResponseWriter, r *http.Request) {
	if err := s.AI.DeleteConversation(r.Context(), userFrom(r.Context()).ID, r.PathValue("id")); err != nil {
		s.aiError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type sendRequest struct {
	Text string `json:"text"`
}

// aiSend streams the reply as newline-delimited JSON:
//
//	{"delta":"chunk of text"}
//	...
//	{"done":true,"message":{...}}  or  {"error":"..."}
func (s *Server) aiSend(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	var mu sync.Mutex
	write := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(v)
		flusher.Flush()
	}

	msg, err := s.AI.Send(r.Context(), ai.SendOptions{
		UserID:         userFrom(r.Context()).ID,
		Username:       userFrom(r.Context()).Username,
		ConversationID: r.PathValue("id"),
		Text:           req.Text,
	}, func(delta string) {
		write(map[string]string{"delta": delta})
	})
	if err != nil && (msg == nil || msg.Content == "") {
		write(map[string]string{"error": userFacingAIError(err)})
		return
	}
	write(map[string]any{"done": true, "message": msg})
}

func userFacingAIError(err error) string {
	switch {
	case errors.Is(err, ai.ErrNoProvider):
		return "No AI provider is configured. An administrator can add one in Settings."
	case errors.Is(err, ai.ErrBusy):
		return "This conversation is already generating a reply."
	default:
		return err.Error()
	}
}

// ---------- memory ----------

func (s *Server) aiListMemory(w http.ResponseWriter, r *http.Request) {
	uid := userFrom(r.Context()).ID
	mems, err := s.AI.Memories(r.Context(), uid)
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	enabled, err := s.AI.MemoryEnabled(r.Context(), uid)
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "memories": mems})
}

type memoryRequest struct {
	Content string `json:"content"`
}

func (s *Server) aiAddMemory(w http.ResponseWriter, r *http.Request) {
	var req memoryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	m, err := s.AI.AddMemory(r.Context(), userFrom(r.Context()).ID, req.Content)
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) aiUpdateMemory(w http.ResponseWriter, r *http.Request) {
	var req memoryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.AI.UpdateMemory(r.Context(), userFrom(r.Context()).ID, r.PathValue("id"), req.Content); err != nil {
		s.aiError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) aiDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.AI.DeleteMemory(r.Context(), userFrom(r.Context()).ID, r.PathValue("id")); err != nil {
		s.aiError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type memoryEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) aiSetMemoryEnabled(w http.ResponseWriter, r *http.Request) {
	var req memoryEnabledRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.AI.SetMemoryEnabled(r.Context(), userFrom(r.Context()).ID, req.Enabled); err != nil {
		s.aiError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
