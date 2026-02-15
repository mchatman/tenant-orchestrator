// Package k8s provides a thin wrapper around the Kubernetes dynamic client for
// managing OpenClaw tenant instances.
package k8s

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/mchatman/tenant-orchestrator/internal/config"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// uuidRe matches a standard UUID (v4 or otherwise).
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Manager provides high-level operations on OpenClaw tenant instances inside a
// single Kubernetes namespace.
type Manager struct {
	client dynamic.Interface
	cfg    *config.Config
}

var tenantGVR = schema.GroupVersionResource{
	Group:    "openclaw.rocks",
	Version:  "v1alpha1",
	Resource: "openclawinstances",
}

// NewManager creates a Manager that operates in the namespace specified by cfg.
func NewManager(cfg *config.Config) (*Manager, error) {
	restCfg, err := getConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get k8s config: %v", err)
	}

	client, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %v", err)
	}

	return &Manager{
		client: client,
		cfg:    cfg,
	}, nil
}

func getConfig() (*rest.Config, error) {
	// Try KUBECONFIG_BASE64 environment variable first (for App Platform)
	if kubeconfigBase64 := os.Getenv("KUBECONFIG_BASE64"); kubeconfigBase64 != "" {
		kubeconfigBytes, err := base64.StdEncoding.DecodeString(kubeconfigBase64)
		if err != nil {
			return nil, fmt.Errorf("failed to decode KUBECONFIG_BASE64: %v", err)
		}

		cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse kubeconfig: %v", err)
		}
		return cfg, nil
	}

	// Try in-cluster config (for when running in K8s)
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}

	// Fall back to kubeconfig file (for local development)
	kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

// ValidateTenantID returns an error if tenantID is not a valid UUID.
func ValidateTenantID(tenantID string) error {
	if !uuidRe.MatchString(tenantID) {
		return fmt.Errorf("invalid tenant ID: must be a valid UUID")
	}
	return nil
}

// buildEnvVars constructs the env var list for a new tenant instance.
// It injects shared API keys from the orchestrator's own environment.
func buildEnvVars(gatewayToken string) []map[string]interface{} {
	envs := []map[string]interface{}{
		{"name": "OPENCLAW_GATEWAY_TOKEN", "value": gatewayToken},
		{"name": "NODE_ENV", "value": "production"},
	}

	// Inject AI provider keys if configured
	for _, key := range []string{
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
	} {
		if val := os.Getenv(key); val != "" {
			envs = append(envs, map[string]interface{}{"name": key, "value": val})
		}
	}

	return envs
}

func generateTenantInstanceName() (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("tenant-%s", hex.EncodeToString(bytes)), nil
}

// buildInstanceSpec constructs the full OpenClawInstance CRD object ready for
// creation in the cluster.
func (m *Manager) buildInstanceSpec(instanceName, tenantID, gatewayToken string) *unstructured.Unstructured {
	domain := m.cfg.Domain
	internalDomain := m.cfg.InternalDomain

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "openclaw.rocks/v1alpha1",
			"kind":       "OpenClawInstance",
			"metadata": map[string]interface{}{
				"name":      instanceName,
				"namespace": m.cfg.Namespace,
				"labels": map[string]interface{}{
					"tenant": tenantID,
					"app":    "tenant-instance",
				},
			},
			"spec": map[string]interface{}{
				"image": map[string]interface{}{
					"repository": "ghcr.io/openclaw/openclaw",
					"tag":        "latest",
					"pullPolicy": "Always",
					"pullSecrets": []map[string]interface{}{
						{"name": "registry-wareit"},
					},
				},
				"config": map[string]interface{}{
					"raw": map[string]interface{}{
						"gateway": map[string]interface{}{
							"bind": "lan",
							"mode": "local",
							"trustedProxies": []string{
								"10.0.0.0/8",
								"172.16.0.0/12",
								"192.168.0.0/16",
							},
							"controlUi": map[string]interface{}{
								"allowInsecureAuth": true,
								"allowedOrigins":    []string{fmt.Sprintf("https://dashboard.%s", domain)},
							},
						},
					},
				},
				"env": buildEnvVars(gatewayToken),
				"networking": map[string]interface{}{
					"ingress": map[string]interface{}{
						"enabled":   true,
						"className": "nginx",
						"annotations": map[string]interface{}{
							"cert-manager.io/cluster-issuer":                 "letsencrypt-prod",
							"nginx.ingress.kubernetes.io/proxy-body-size":    "50m",
							"nginx.ingress.kubernetes.io/proxy-read-timeout": "3600",
							"nginx.ingress.kubernetes.io/proxy-send-timeout": "3600",
							"nginx.ingress.kubernetes.io/ssl-redirect":       "false",
							"nginx.ingress.kubernetes.io/force-ssl-redirect": "false",
						},
						"hosts": []map[string]interface{}{
							{
								"host": fmt.Sprintf("%s.%s", instanceName, domain),
								"paths": []map[string]interface{}{
									{"path": "/", "pathType": "Prefix"},
								},
							},
							{
								"host": fmt.Sprintf("%s.%s", instanceName, internalDomain),
								"paths": []map[string]interface{}{
									{"path": "/", "pathType": "Prefix"},
								},
							},
						},
						"tls": []map[string]interface{}{
							{
								"hosts":      []string{fmt.Sprintf("%s.%s", instanceName, domain)},
								"secretName": fmt.Sprintf("%s-tls", instanceName),
							},
						},
						"security": map[string]interface{}{
							"enableHSTS": false,
							"forceHTTPS": false,
						},
					},
				},
				"security": map[string]interface{}{
					"networkPolicy": map[string]interface{}{
						"allowedIngressNamespaces": []string{"ingress-nginx"},
					},
				},
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{
						"memory": "512Mi",
						"cpu":    "100m",
					},
					"limits": map[string]interface{}{
						"memory": "1536Mi",
						"cpu":    "1000m",
					},
				},
				"storage": map[string]interface{}{
					"persistence": map[string]interface{}{
						"enabled": true,
						"size":    "1Gi",
					},
				},
			},
		},
	}
}

// CreateInstance provisions a new OpenClaw instance for the given tenant and
// returns its public endpoint URL.
func (m *Manager) CreateInstance(ctx context.Context, tenantID, gatewayToken string) (string, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return "", err
	}

	instanceName, err := generateTenantInstanceName()
	if err != nil {
		return "", fmt.Errorf("generating instance name: %v", err)
	}

	instance := m.buildInstanceSpec(instanceName, tenantID, gatewayToken)

	_, err = m.client.Resource(tenantGVR).Namespace(m.cfg.Namespace).Create(ctx, instance, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create tenant instance: %v", err)
	}

	return m.InstanceURL(instanceName), nil
}

// InstanceInfo holds metadata about a running tenant instance.
type InstanceInfo struct {
	Name         string // Kubernetes resource name (e.g. "tenant-ab12cd34")
	Endpoint     string // Public URL (e.g. "https://tenant-ab12cd34.wareit.ai")
	Status       string // Simplified status: "starting", "running", or "error"
	GatewayToken string // The OPENCLAW_GATEWAY_TOKEN injected at creation time
}

// InstanceURL returns the public HTTPS URL for the given instance name.
func (m *Manager) InstanceURL(instanceName string) string {
	return fmt.Sprintf("https://%s.%s", instanceName, m.cfg.Domain)
}

// GetInstance finds a tenant's instance and returns its info, or nil if none
// exists.
func (m *Manager) GetInstance(ctx context.Context, tenantID string) (*InstanceInfo, error) {
	if err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}

	list, err := m.client.Resource(tenantGVR).Namespace(m.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("tenant=%s", tenantID),
	})
	if err != nil {
		return nil, fmt.Errorf("listing instances: %v", err)
	}

	if len(list.Items) == 0 {
		return nil, nil
	}

	item := list.Items[0]
	name := item.GetName()

	phase, found, _ := unstructured.NestedString(item.Object, "status", "phase")
	status := "starting"
	if found {
		switch phase {
		case "Running":
			status = "running"
		case "Failed":
			status = "error"
		}
	}

	// Extract gateway token from env vars
	var gatewayToken string
	envVars, _, _ := unstructured.NestedSlice(item.Object, "spec", "env")
	for _, e := range envVars {
		if envMap, ok := e.(map[string]interface{}); ok {
			if envMap["name"] == "OPENCLAW_GATEWAY_TOKEN" {
				gatewayToken, _ = envMap["value"].(string)
				break
			}
		}
	}

	return &InstanceInfo{
		Name:         name,
		Endpoint:     m.InstanceURL(name),
		Status:       status,
		GatewayToken: gatewayToken,
	}, nil
}

// DeleteInstance deletes all instances belonging to the given tenant.
func (m *Manager) DeleteInstance(ctx context.Context, tenantID string) error {
	if err := ValidateTenantID(tenantID); err != nil {
		return err
	}

	list, err := m.client.Resource(tenantGVR).Namespace(m.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("tenant=%s", tenantID),
	})
	if err != nil {
		return fmt.Errorf("listing instances for deletion: %v", err)
	}

	for _, instance := range list.Items {
		err = m.client.Resource(tenantGVR).Namespace(m.cfg.Namespace).Delete(
			ctx, instance.GetName(), metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete tenant instance %s: %v", instance.GetName(), err)
		}
	}

	return nil
}

// StopInstance tears down a tenant's instance. The OpenClaw operator does not
// support scaling to zero, so this is equivalent to deletion.
func (m *Manager) StopInstance(ctx context.Context, tenantID string) error {
	return m.DeleteInstance(ctx, tenantID)
}

// StartInstance is not currently supported — callers should use CreateInstance
// with a gateway token instead.
func (m *Manager) StartInstance(ctx context.Context, tenantID string) error {
	return fmt.Errorf("StartInstance not supported — use CreateInstance instead")
}
