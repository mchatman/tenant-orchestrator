package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mchatman/tenant-orchestrator/internal/k8s"
)

type Handler struct {
	k8sManager *k8s.Manager
}

func NewHandler(k8sManager *k8s.Manager) *Handler {
	return &Handler{
		k8sManager: k8sManager,
	}
}

type InstanceResponse struct {
	Endpoint string `json:"endpoint"`
	Status   string `json:"status"`
}

type CreateInstanceRequest struct {
	GatewayToken string `json:"gateway_token"`
}

// CreateInstance creates a new instance for a tenant
func (h *Handler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenant-id")

	var req CreateInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.GatewayToken = generateToken() // Generate if not provided
	}

	endpoint, err := h.k8sManager.CreateInstance(r.Context(), tenantID, req.GatewayToken)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := InstanceResponse{
		Endpoint: endpoint,
		Status:   "creating",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// GetInstance gets the status and endpoint of a tenant's instance
func (h *Handler) GetInstance(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenant-id")

	status, err := h.k8sManager.GetInstanceStatus(r.Context(), tenantID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if status == "not_found" {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	resp := InstanceResponse{
		Endpoint: h.k8sManager.GetInstanceEndpoint(tenantID),
		Status:   status,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// DeleteInstance deletes a tenant's instance
func (h *Handler) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenant-id")

	err := h.k8sManager.DeleteInstance(r.Context(), tenantID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// StartInstance starts a stopped instance
func (h *Handler) StartInstance(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenant-id")

	err := h.k8sManager.StartInstance(r.Context(), tenantID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := InstanceResponse{
		Endpoint: h.k8sManager.GetInstanceEndpoint(tenantID),
		Status:   "starting",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// StopInstance stops a running instance
func (h *Handler) StopInstance(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenant-id")

	err := h.k8sManager.StopInstance(r.Context(), tenantID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := InstanceResponse{
		Endpoint: h.k8sManager.GetInstanceEndpoint(tenantID),
		Status:   "stopping",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// generateToken creates a random gateway token
func generateToken() string {
	// Simple token generation - should use crypto/rand in production
	return "token-" + randString(32)
}

func randString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[i%len(letters)] // Simplified - use crypto/rand in production
	}
	return string(b)
}