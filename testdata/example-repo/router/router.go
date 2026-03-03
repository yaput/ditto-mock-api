package router

import (
	"github.com/example/user-service/handler"
	"github.com/go-chi/chi/v5"
)

// NewRouter sets up all HTTP routes for the user service.
func NewRouter() *chi.Mux {
	r := chi.NewRouter()

	uh := &handler.UserHandler{}
	th := &handler.TeamHandler{}

	// User routes
	r.Get("/users", uh.ListUsers)
	r.Get("/users/{id}", uh.GetUser)
	r.Post("/users", uh.CreateUser)
	r.Put("/users/{id}", uh.UpdateUser)
	r.Delete("/users/{id}", uh.DeleteUser)

	// Team routes
	r.Get("/teams", th.ListTeams)
	r.Get("/teams/{id}", th.GetTeam)
	r.Post("/teams", th.CreateTeam)

	return r
}
