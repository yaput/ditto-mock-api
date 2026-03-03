package models

import "time"

// Team represents a team/group of users.
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OwnerID   string    `json:"owner_id"`
	MemberIDs []string  `json:"member_ids"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateTeamRequest is the payload for creating a new team.
type CreateTeamRequest struct {
	Name    string   `json:"name"`
	OwnerID string   `json:"owner_id"`
	Members []string `json:"members,omitempty"`
}
