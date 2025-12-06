package consul

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/SkynetNext/game-gateway/internal/logger"
	"go.uber.org/zap"
)

// ServiceEntry represents a service instance from Consul
type ServiceEntry struct {
	Address string
	Port    int
	Meta    map[string]string
}

// Discovery manages Consul service discovery
type Discovery struct {
	consulAddress   string
	httpClient      *http.Client
	refreshInterval time.Duration
}

// NewDiscovery creates a new Consul service discovery instance
func NewDiscovery(consulAddress string, refreshInterval time.Duration) *Discovery {
	return &Discovery{
		consulAddress: consulAddress,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		refreshInterval: refreshInterval,
	}
}

// DiscoverServices queries Consul for healthy services by service name and world_id
// Returns a map of world_id -> []ServiceEntry
func (d *Discovery) DiscoverServices(ctx context.Context, serviceName string) (map[int][]ServiceEntry, error) {
	// Query Consul health API: /v1/health/service/{service}?passing
	url := fmt.Sprintf("%s/v1/health/service/%s?passing=true", d.consulAddress, serviceName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query Consul: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("consul API returned status %d: %s", resp.StatusCode, string(body))
	}

	var entries []struct {
		Service struct {
			Address string            `json:"Address"`
			Port    int               `json:"Port"`
			Meta    map[string]string `json:"Meta"`
		} `json:"Service"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Group services by world_id
	result := make(map[int][]ServiceEntry)
	for _, entry := range entries {
		worldIDStr := entry.Service.Meta["world_id"]
		if worldIDStr == "" {
			// Skip services without world_id
			continue
		}

		worldID, err := strconv.Atoi(worldIDStr)
		if err != nil {
			logger.Warn("invalid world_id in service meta",
				zap.String("service", serviceName),
				zap.String("world_id", worldIDStr),
				zap.Error(err))
			continue
		}

		serviceEntry := ServiceEntry{
			Address: entry.Service.Address,
			Port:    entry.Service.Port,
			Meta:    entry.Service.Meta,
		}

		result[worldID] = append(result[worldID], serviceEntry)
	}

	return result, nil
}

// StartRefreshLoop starts a background goroutine that periodically refreshes service discovery
// and calls the callback with updated services
func (d *Discovery) StartRefreshLoop(ctx context.Context, serviceName string, callback func(map[int][]ServiceEntry)) {
	go func() {
		ticker := time.NewTicker(d.refreshInterval)
		defer ticker.Stop()

		// Initial discovery
		services, err := d.DiscoverServices(ctx, serviceName)
		if err != nil {
			logger.Error("initial Consul service discovery failed",
				zap.String("service", serviceName),
				zap.Error(err))
		} else {
			callback(services)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				services, err := d.DiscoverServices(ctx, serviceName)
				if err != nil {
					logger.Error("Consul service discovery failed",
						zap.String("service", serviceName),
						zap.Error(err))
					continue
				}

				callback(services)
			}
		}
	}()
}
