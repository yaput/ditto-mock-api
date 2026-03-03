package handler

import (
	"encoding/json"
	"net/http"

	"github.com/example/user-service/models"
	"github.com/go-chi/chi/v5"
)

// TeamHandler handles team-related HTTP requests.
type TeamHandler struct{}

// ListTeams returns all teams.
func (h *TeamHandler) ListTeams(w http.ResponseWriter, r *http.Request) {
	teams := []models.Team{}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(teams)
}

// GetTeam returns a single team by ID.
func (h *TeamHandler) GetTeam(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	team := models.Team{ID: id, Name: "Engineering"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(team)
}

// CreateTeam creates a new team.
func (h *TeamHandler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	var req models.CreateTeamRequest
	json.NewDecoder(r.Body).Decode(&req)

	team := models.Team{ID: "new-team", Name: req.Name, OwnerID: req.OwnerID}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(team)
}
