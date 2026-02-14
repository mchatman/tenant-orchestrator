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
			"apiVersion": "openclaw.openclaw.io/v1alpha1",
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
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{
						"memory": "1Gi",
						"cpu":    "250m",
					},
					"limits": map[string]interface{}{
						"memory": "2Gi",
						"cpu":    "500m",
					},
				},
				"probes": map[string]interface{}{
					"startup": map[string]interface{}{
						"initialDelaySeconds": 30,
						"timeoutSeconds":      10,
						"periodSeconds":       10,
						"failureThreshold":    60, // 10 minutes total
					},
					"readiness": map[string]interface{}{
						"initialDelaySeconds": 10,
						"timeoutSeconds":      5,
						"periodSeconds":       10,
						"failureThreshold":    3,
					},
					"liveness": map[string]interface{}{
						"initialDelaySeconds": 60,
						"timeoutSeconds":      10,
						"periodSeconds":       30,
						"failureThreshold":    3,
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

	// Create Ingress entry for this tenant
	err = m.createIngress(ctx, instanceName)
	if err != nil {
		// Log but don't fail - the instance is created, ingress can be added later
		fmt.Printf("Warning: failed to create ingress for %s: %v\n", instanceName, err)
	}

	// Return the instance name (which will be used as the endpoint)
	return instanceName, nil
}

// GetInstanceEndpoint returns the external endpoint for a tenant's instance
func (m *Manager) GetInstanceEndpoint(instanceName string) string {
	return fmt.Sprintf("http://%s.wareit.ai", instanceName)
}

// GetInstanceStatus checks if an instance is running by tenant ID
func (m *Manager) GetInstanceStatus(ctx context.Context, tenantID string) (string, error) {
	// Find instance by tenant label
	list, err := m.client.Resource(tenantGVR).Namespace(m.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("tenant=%s", tenantID),
	})
	if err != nil {
		return "", fmt.Errorf("listing instances: %v", err)
	}

	if len(list.Items) == 0 {
		return "not_found", nil
	}

	// Check the status of the first matching instance
	instance := list.Items[0]
	status, found, _ := unstructured.NestedString(instance.Object, "status", "phase")
	if !found {
		return "starting", nil // No status yet means it's starting
	}

	switch status {
	case "Ready":
		return "running", nil
	case "Failed":
		return "error", nil
	default:
		return "starting", nil
	}
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

var ingressGVR = schema.GroupVersionResource{
	Group:    "networking.k8s.io",
	Version:  "v1",
	Resource: "ingresses",
}

// createIngress creates an ingress entry for a tenant instance
func (m *Manager) createIngress(ctx context.Context, instanceName string) error {
	// For now, just log that we would create the ingress
	// The actual implementation can be done later or manually
	fmt.Printf("Would create ingress for %s at %s.wareit.ai\n", instanceName, instanceName)
	return nil
}