package bnkforge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// Client is an HTTP client for interacting with the BNK Forge daemon.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// ClusterPayload represents the metadata registered with BNK Forge.
type ClusterPayload struct {
	Name         string            `json:"name"`
	Provider     string            `json:"provider"`
	Region       string            `json:"region"`
	Zone         string            `json:"zone"`
	Project      string            `json:"project"`
	Status       string            `json:"status"`
	ExternalCIDR string            `json:"externalCidr"`
	InternalCIDR string            `json:"internalCidr"`
	MainCIDR     string            `json:"mainCidr"`
	BNKVersion   string            `json:"bnkVersion"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// ClusterStatus represents the response from BNK Forge status query.
type ClusterStatus struct {
	Name         string            `json:"name"`
	Provider     string            `json:"provider"`
	Status       string            `json:"status"`
	RegisteredAt string            `json:"registeredAt"`
	UpdatedAt    string            `json:"updatedAt"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// NewClient creates a new BNK Forge client.
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// BuildPayload constructs the payload from an intent.Cluster.
func BuildPayload(cl *intent.Cluster, status string) ClusterPayload {
	if status == "" {
		status = "ACTIVE"
	}
	return ClusterPayload{
		Name:         cl.Metadata.Name,
		Provider:     "gke",
		Region:       cl.Region,
		Zone:         cl.Metadata.Zone,
		Project:      cl.Metadata.Project,
		Status:       status,
		ExternalCIDR: cl.Network.External.CIDR,
		InternalCIDR: cl.Network.Internal.CIDR,
		MainCIDR:     cl.Network.Main.CIDR,
		BNKVersion:   cl.Bnk.ManifestVersion,
		Labels:       cl.Metadata.Labels,
	}
}

// Register registers the cluster with the BNK Forge daemon.
func (c *Client) Register(ctx context.Context, payload ClusterPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/clusters", c.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect to bnkforge at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bnkforge register error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// Unregister removes the cluster from the BNK Forge daemon.
func (c *Client) Unregister(ctx context.Context, clusterName string) error {
	url := fmt.Sprintf("%s/api/v1/clusters/%s", c.BaseURL, clusterName)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect to bnkforge at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bnkforge unregister error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetStatus checks the cluster status in BNK Forge.
func (c *Client) GetStatus(ctx context.Context, clusterName string) (*ClusterStatus, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s", c.BaseURL, clusterName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to bnkforge at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("cluster %q not registered in bnkforge", clusterName)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bnkforge query error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var st ClusterStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, fmt.Errorf("decode bnkforge response: %w", err)
	}

	return &st, nil
}
