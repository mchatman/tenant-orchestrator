package api

import (
	"crypto/rand"
	"encoding/hex"
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

	info, err := h.k8sManager.GetInstance(r.Context(), tenantID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if info == nil {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	resp := InstanceResponse{
		Endpoint: info.Name,
		Status:   info.Status,
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

// generateToken creates a cryptographically random gateway token.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}