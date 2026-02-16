package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/mchatman/bottegeppetto/api"
	"github.com/mchatman/bottegeppetto/internal/config"
	"github.com/mchatman/bottegeppetto/internal/k8s"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := config.Load()
	log.Printf("config: namespace=%s domain=%s port=%s", cfg.Namespace, cfg.Domain, cfg.Port)

	// Initialize K8s manager
	k8sManager, err := k8s.NewManager(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize K8s manager: %v", err)
	}

	// Initialize API handler
	handler := api.NewHandler(k8sManager)

	// Setup routes
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/tenants/{tenant-id}/instance", func(r chi.Router) {
		r.Post("/", handler.CreateInstance)
		r.Get("/", handler.GetInstance)
		r.Delete("/", handler.DeleteInstance)
	})

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 90 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
		}
	}()

	log.Printf("starting bottegeppetto on :%s", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server failed: %v", err)
	}
	log.Println("server stopped")
}
