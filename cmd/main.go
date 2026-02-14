package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mchatman/tenant-orchestrator/api"
	"github.com/mchatman/tenant-orchestrator/internal/k8s"
)

func main() {
	// Get namespace from env or use default
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = "tenants"
	}

	// Initialize K8s manager
	k8sManager, err := k8s.NewManager(namespace)
	if err != nil {
		log.Fatalf("Failed to initialize K8s manager: %v", err)
	}

	// Initialize API handler
	handler := api.NewHandler(k8sManager)

	// Setup routes
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	r.Route("/tenants/{tenant-id}/instance", func(r chi.Router) {
		r.Post("/", handler.CreateInstance)
		r.Get("/", handler.GetInstance)
		r.Delete("/", handler.DeleteInstance)
		r.Post("/start", handler.StartInstance)
		r.Post("/stop", handler.StopInstance)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting tenant-orchestrator on port %s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}