package handler

import (
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// generateTempPassword returns a 12-character password drawn from a URL-safe
// alphabet. It's used by ResetUserPassword when the caller doesn't supply a
// password and an admin needs a temporary credential to hand to the user.
const tempPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

func generateTempPassword() (string, error) {
	const length = 12
	buf := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = tempPasswordAlphabet[int(b)%len(tempPasswordAlphabet)]
	}
	return string(buf), nil
}

// CreateUserRequest represents the request body for creating a user.
type CreateUserRequest struct {
	Email       string `json:"email"`
	Username    string `json:"username"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Password    string `json:"password"`
	IsActive    *bool  `json:"is_active"`
	IsStaff     bool   `json:"is_staff"`
	IsSuperuser bool   `json:"is_superuser"`
}

// UpdateUserRequest represents the request body for updating a user.
type UpdateUserRequest struct {
	Email     string `json:"email"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	IsActive  *bool  `json:"is_active"`
}

// ResetPasswordRequest represents the request body for password reset.
type ResetPasswordRequest struct {
	Password string `json:"password"`
}

// CreateUser handles POST /api/v1/users/.
func (h *ResourceHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "users_error", "user store not configured")
		return
	}
	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Username = strings.TrimSpace(req.Username)
	if req.Email == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Email is required")
		return
	}
	if req.Username == "" {
		req.Username = req.Email
	}
	if req.Password == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Password is required")
		return
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "hash_error", "Failed to hash password")
		return
	}
	// Default to active when not specified.
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	user, err := h.queries.CreateUser(r.Context(), sqlc.CreateUserParams{
		Email:       req.Email,
		Username:    req.Username,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		Password:    string(hashed),
		IsActive:    isActive,
		IsStaff:     req.IsStaff,
		IsSuperuser: req.IsSuperuser,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create user")
		return
	}
	recordAudit(r, h.queries, "user.create", "user", user.ID.String(), user.Username, map[string]any{
		"email":        user.Email,
		"is_active":    user.IsActive,
		"is_staff":     user.IsStaff,
		"is_superuser": user.IsSuperuser,
	})
	RespondJSON(w, http.StatusCreated, mapUser(user))
}

// UpdateUser handles PUT/PATCH /api/v1/users/{id}/.
func (h *ResourceHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "users_error", "user store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid user ID")
		return
	}
	current, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}
	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		email = current.Email
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = current.Username
	}
	firstName := req.FirstName
	if firstName == "" {
		firstName = current.FirstName
	}
	lastName := req.LastName
	if lastName == "" {
		lastName = current.LastName
	}
	isActive := current.IsActive
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	user, err := h.queries.UpdateUser(r.Context(), sqlc.UpdateUserParams{
		ID:        id,
		Email:     email,
		Username:  username,
		FirstName: firstName,
		LastName:  lastName,
		IsActive:  isActive,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update user")
		return
	}
	recordAudit(r, h.queries, "user.update", "user", user.ID.String(), user.Username, map[string]any{
		"email":     user.Email,
		"is_active": user.IsActive,
	})
	RespondJSON(w, http.StatusOK, mapUser(user))
}

// DeleteUser handles DELETE /api/v1/users/{id}/.
func (h *ResourceHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "users_error", "user store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid user ID")
		return
	}
	existing, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}
	if err := h.queries.DeleteUser(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "delete_error", "Failed to delete user")
		return
	}
	recordAudit(r, h.queries, "user.delete", "user", existing.ID.String(), existing.Username, map[string]any{
		"email": existing.Email,
	})
	w.WriteHeader(http.StatusNoContent)
}

// ResetUserPassword handles POST /api/v1/users/{id}/reset-password/.
func (h *ResourceHandler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "users_error", "user store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid user ID")
		return
	}
	existing, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}
	// Body is optional: if empty / no `password` field, we generate a random
	// temporary password and return it to the caller. The frontend's "Reset
	// password" admin action POSTs an empty body and expects a temp password
	// back to display once.
	var (
		req     ResetPasswordRequest
		generated bool
	)
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
			return
		}
	}
	if req.Password == "" {
		tmp, err := generateTempPassword()
		if err != nil {
			RespondError(w, http.StatusInternalServerError, "generate_error", "Failed to generate temporary password")
			return
		}
		req.Password = tmp
		generated = true
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "hash_error", "Failed to hash password")
		return
	}
	if err := h.queries.UpdateUserPassword(r.Context(), sqlc.UpdateUserPasswordParams{
		ID:       id,
		Password: string(hashed),
	}); err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to reset password")
		return
	}
	recordAudit(r, h.queries, "user.reset_password", "user", existing.ID.String(), existing.Username, map[string]any{
		"generated": generated,
	})
	resp := map[string]any{"success": true, "message": "Password updated"}
	if generated {
		// Returned exactly once — the frontend captures this and shows it to
		// the admin who initiated the reset.
		resp["temporary_password"] = req.Password
	}
	RespondJSON(w, http.StatusOK, resp)
}
