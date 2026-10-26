package quiz

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (h *Handler) api(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "База квизов недоступна"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	switch {
	case r.Method == http.MethodPost && path == "register":
		h.register(w, r)
	case r.Method == http.MethodPost && path == "login":
		h.login(w, r)
	case r.Method == http.MethodGet && path == "me":
		h.me(w, r)
	case r.Method == http.MethodGet && path == "cabinet":
		h.cabinet(w, r)
	case r.Method == http.MethodPost && path == "quizzes":
		h.saveQuiz(w, r)
	default:
		http.NotFound(w, r)
	}
}

type authBody struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type quizBody struct {
	Title     string          `json:"title"`
	Category  string          `json:"category"`
	Rules     string          `json:"rules"`
	Seconds   int             `json:"seconds"`
	Questions []SavedQuestion `json:"questions"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var body authBody
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Не понял данные"})
		return
	}
	user, token, err := h.store.Register(body.Name, body.Email, body.Password, body.Role)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body authBody
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Не понял данные"})
		return
	}
	user, token, err := h.store.Login(body.Email, body.Password)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user, err := h.userFrom(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (h *Handler) cabinet(w http.ResponseWriter, r *http.Request) {
	user, err := h.userFrom(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": err.Error()})
		return
	}
	hosted, played := h.store.History(user)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":    user,
		"quizzes": h.store.Quizzes(user.ID),
		"hosted":  hosted,
		"played":  played,
	})
}

func (h *Handler) saveQuiz(w http.ResponseWriter, r *http.Request) {
	user, err := h.userFrom(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": err.Error()})
		return
	}
	var body quizBody
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Не понял квиз"})
		return
	}
	saved, err := h.store.SaveQuiz(user, SavedQuiz{
		Title:     body.Title,
		Category:  body.Category,
		Rules:     body.Rules,
		Seconds:   body.Seconds,
		Questions: body.Questions,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"quiz": saved})
}

func (h *Handler) userFrom(r *http.Request) (PublicUser, error) {
	header := r.Header.Get("Authorization")
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if token == "" || token == header {
		token = r.Header.Get("X-Quiz-Token")
	}
	return h.store.UserByToken(token)
}

func readJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 32<<10))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
