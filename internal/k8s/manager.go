package k8s

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type Manager struct {
	client    dynamic.Interface
	namespace string
}

var tenantGVR = schema.GroupVersionResource{
	Group:    "openclaw.rocks",
	Version:  "v1alpha1",
	Resource: "openclawinstances",
}

func NewManager(namespace string) (*Manager, error) {
	config, err := getConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get k8s config: %v", err)
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %v", err)
	}

	return &Manager{
		client:    client,
		namespace: namespace,
	}, nil
}

func getConfig() (*rest.Config, error) {
	// Try KUBECONFIG_BASE64 environment variable first (for App Platform)
	if kubeconfigBase64 := os.Getenv("KUBECONFIG_BASE64"); kubeconfigBase64 != "" {
		kubeconfigBytes, err := base64.StdEncoding.DecodeString(kubeconfigBase64)
		if err != nil {
			return nil, fmt.Errorf("failed to decode KUBECONFIG_BASE64: %v", err)
		}

		config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse kubeconfig: %v", err)
		}
		return config, nil
	}

	// Try in-cluster config (for when running in K8s)
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	// Fall back to kubeconfig file (for local development)
	kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func (m *Manager) generateTenantInstanceName() (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("tenant-%s", hex.EncodeToString(bytes)), nil
}

// CreateInstance creates a new instance for a tenant
func (m *Manager) CreateInstance(ctx context.Context, tenantID, gatewayToken string) (string, error) {
	// Generate a unique instance name
	instanceName, err := m.generateTenantInstanceName()
	if err != nil {
		return "", fmt.Errorf("generating instance name: %v", err)
	}

	// Create OpenClawInstance CRD
	instance := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "openclaw.rocks/v1alpha1",
			"kind":       "OpenClawInstance",
			"metadata": map[string]interface{}{
				"name":      instanceName,
				"namespace": m.namespace,
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
							"controlUi": map[string]interface{}{
								"allowInsecureAuth":              true,
								"dangerouslyDisableDeviceAuth":   true,
							},
						},
					},
				},
				"env": []map[string]interface{}{
					{
						"name":  "OPENCLAW_GATEWAY_TOKEN",
						"value": gatewayToken,
					},
					{
						"name":  "NODE_ENV",
						"value": "production",
					},
				},
				"networking": map[string]interface{}{
					"ingress": map[string]interface{}{
						"enabled":   true,
						"className": "nginx",
						"annotations": map[string]interface{}{
							"cert-manager.io/cluster-issuer":                    "letsencrypt-prod",
							"nginx.ingress.kubernetes.io/proxy-body-size":       "50m",
							"nginx.ingress.kubernetes.io/proxy-read-timeout":    "3600",
							"nginx.ingress.kubernetes.io/proxy-send-timeout":    "3600",
						},
						"hosts": []map[string]interface{}{
							{
								"host": fmt.Sprintf("%s.wareit.ai", instanceName),
								"paths": []map[string]interface{}{
									{"path": "/", "pathType": "Prefix"},
								},
							},
						},
						"tls": []map[string]interface{}{
							{
								"hosts":      []string{fmt.Sprintf("%s.wareit.ai", instanceName)},
								"secretName": fmt.Sprintf("%s-tls", instanceName),
							},
						},
						"security": map[string]interface{}{
							"enableHSTS":  false,
							"forceHTTPS": true,
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

	_, err = m.client.Resource(tenantGVR).Namespace(m.namespace).Create(ctx, instance, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create tenant instance: %v", err)
	}

	// The operator creates Ingress + TLS automatically via the CRD.
	return instanceName, nil
}

// InstanceInfo holds the name and status of a tenant's instance.
type InstanceInfo struct {
	Name   string
	Status string
}

// GetInstanceEndpoint returns the external endpoint for a tenant's instance
func (m *Manager) GetInstanceEndpoint(instanceName string) string {
	return fmt.Sprintf("https://%s.wareit.ai", instanceName)
}

// GetInstance finds a tenant's instance and returns its name and status.
func (m *Manager) GetInstance(ctx context.Context, tenantID string) (*InstanceInfo, error) {
	list, err := m.client.Resource(tenantGVR).Namespace(m.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("tenant=%s", tenantID),
	})
	if err != nil {
		return nil, fmt.Errorf("listing instances: %v", err)
	}

	if len(list.Items) == 0 {
		return nil, nil
	}

	instance := list.Items[0]
	name := instance.GetName()

	phase, found, _ := unstructured.NestedString(instance.Object, "status", "phase")
	status := "starting"
	if found {
		switch phase {
		case "Running":
			status = "running"
		case "Failed":
			status = "error"
		}
	}

	return &InstanceInfo{Name: name, Status: status}, nil
}

// GetInstanceStatus checks if an instance is running by tenant ID (deprecated: use GetInstance)
func (m *Manager) GetInstanceStatus(ctx context.Context, tenantID string) (string, error) {
	info, err := m.GetInstance(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "not_found", nil
	}
	return info.Status, nil
}

// DeleteInstance deletes a tenant's instance
func (m *Manager) DeleteInstance(ctx context.Context, tenantID string) error {
	// Find instance by tenant label
	list, err := m.client.Resource(tenantGVR).Namespace(m.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("tenant=%s", tenantID),
	})
	if err != nil {
		return fmt.Errorf("listing instances for deletion: %v", err)
	}

	// Delete all instances for this tenant
	for _, instance := range list.Items {
		err = m.client.Resource(tenantGVR).Namespace(m.namespace).Delete(
			ctx, instance.GetName(), metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete tenant instance %s: %v", instance.GetName(), err)
		}
	}

	return nil
}

// StopInstance removes the instance (OpenClaw operator doesn't support scaling to 0)
func (m *Manager) StopInstance(ctx context.Context, tenantID string) error {
	return m.DeleteInstance(ctx, tenantID)
}

// StartInstance creates a new instance (OpenClaw operator doesn't support scaling from 0)
func (m *Manager) StartInstance(ctx context.Context, tenantID string) error {
	// For simplicity, this would need a gateway token - this method may not be needed
	// since we typically create instances with a token during provisioning
	return fmt.Errorf("StartInstance not supported - use CreateInstance instead")
}

