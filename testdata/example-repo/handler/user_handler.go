package handler

import (
	"encoding/json"
	"net/http"

	"github.com/example/user-service/models"
	"github.com/go-chi/chi/v5"
)

// UserHandler handles user-related HTTP requests.
type UserHandler struct{}

// ListUsers returns a paginated list of users.
func (h *UserHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	resp := models.UserListResponse{
		Users:      []models.User{},
		TotalCount: 0,
		Page:       1,
		PageSize:   20,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// GetUser returns a single user by ID.
func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user := models.User{ID: id, Name: "John Doe", Email: "john@example.com"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(user)
}

// CreateUser creates a new user.
func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req models.CreateUserRequest
	json.NewDecoder(r.Body).Decode(&req)

	user := models.User{ID: "new-id", Name: req.Name, Email: req.Email, Role: req.Role}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

// UpdateUser updates an existing user.
func (h *UserHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req models.UpdateUserRequest
	json.NewDecoder(r.Body).Decode(&req)

	user := models.User{ID: id, Name: req.Name, Email: req.Email}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(user)
}

// DeleteUser deletes a user by ID.
func (h *UserHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
