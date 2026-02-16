// Package config centralises all environment-driven configuration for the
// tenant-provisioner service so that no package needs to read os.Getenv
// directly.
package config

import "os"

// Config holds all runtime configuration values.
type Config struct {
	Namespace string // Kubernetes namespace for tenant instances
	Domain    string // Public domain suffix (e.g. "wareit.ai")
	Port      string // HTTP listen port
}

// Load reads configuration from environment variables, falling back to
// sensible defaults where a variable is unset or empty.
func Load() *Config {
	return &Config{
		Namespace: envOr("TENANT_NAMESPACE", "tenants"),
		Domain:    envOr("TENANT_DOMAIN", "wareit.ai"),
		Port:      envOr("PORT", "8080"),
	}
}

// envOr returns the value of the named environment variable or fallback if it
// is empty / unset.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
