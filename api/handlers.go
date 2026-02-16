// Package api implements the HTTP handlers for the tenant-provisioner
// REST API.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/mchatman/tenant-provisioner/internal/k8s"
)

// Handler groups the HTTP handlers and their shared dependencies.
type Handler struct {
	k8sManager *k8s.Manager
}

// NewHandler creates a Handler backed by the given k8s Manager.
func NewHandler(k8sManager *k8s.Manager) *Handler {
	return &Handler{
		k8sManager: k8sManager,
	}
}

// ---------- response helpers ----------

// writeJSON serialises v as JSON and writes it with the given HTTP status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: failed to encode response: %v", err)
	}
}

// writeError sends a JSON-formatted error message to the client.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ---------- request / response types ----------

// InstanceResponse is the standard JSON envelope returned for instance
// operations.
type InstanceResponse struct {
	Name         string `json:"name"`
	Endpoint     string `json:"endpoint"`
	Status       string `json:"status"`
	GatewayToken string `json:"gateway_token,omitempty"`
}

// CreateInstanceRequest is the optional JSON body accepted by CreateInstance.
type CreateInstanceRequest struct {
	GatewayToken string `json:"gateway_token"`
}

// ---------- route handlers ----------

// uuidRe matches a standard UUID.
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// tenantID extracts and validates the tenant-id path parameter. On validation
// failure it writes an error response and returns an empty string.
func tenantID(w http.ResponseWriter, r *http.Request) string {
	id := chi.URLParam(r, "tenant-id")
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid tenant ID: must be a valid UUID")
		return ""
	}
	return id
}

// CreateInstance handles POST /tenants/{tenant-id}/instance — provisions a new
// OpenClaw instance for the tenant.
func (h *Handler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	id := tenantID(w, r)
	if id == "" {
		return
	}

	var req CreateInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.GatewayToken == "" {
		req.GatewayToken = generateToken()
	}

	log.Printf("CreateInstance: tenant=%s", id)

	info, err := h.k8sManager.CreateInstance(r.Context(), id, req.GatewayToken)
	if err != nil {
		log.Printf("CreateInstance error: tenant=%s err=%v", id, err)
		writeError(w, http.StatusInternalServerError, "failed to create instance")
		return
	}

	writeJSON(w, http.StatusCreated, InstanceResponse{
		Name:     info.Name,
		Endpoint: info.Endpoint,
		Status:   info.Status,
	})
}

// GetInstance handles GET /tenants/{tenant-id}/instance — returns the current
// status and endpoint of a tenant's instance.
func (h *Handler) GetInstance(w http.ResponseWriter, r *http.Request) {
	id := tenantID(w, r)
	if id == "" {
		return
	}

	log.Printf("GetInstance: tenant=%s", id)

	info, err := h.k8sManager.GetInstance(r.Context(), id)
	if err != nil {
		log.Printf("GetInstance error: tenant=%s err=%v", id, err)
		writeError(w, http.StatusInternalServerError, "failed to retrieve instance")
		return
	}

	if info == nil {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}

	writeJSON(w, http.StatusOK, InstanceResponse{
		Name:         info.Name,
		Endpoint:     info.Endpoint,
		Status:       info.Status,
		GatewayToken: info.GatewayToken,
	})
}

// DeleteInstance handles DELETE /tenants/{tenant-id}/instance — tears down all
// instances for the tenant.
func (h *Handler) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	id := tenantID(w, r)
	if id == "" {
		return
	}

	log.Printf("DeleteInstance: tenant=%s", id)

	if err := h.k8sManager.DeleteInstance(r.Context(), id); err != nil {
		log.Printf("DeleteInstance error: tenant=%s err=%v", id, err)
		writeError(w, http.StatusInternalServerError, "failed to delete instance")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}



// ---------- helpers ----------

// generateToken creates a cryptographically random 32-byte hex gateway token.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
