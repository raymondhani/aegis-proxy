package infrastructure

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"strconv"

	"github.com/redis/go-redis/v9"
)

type PolicyConfig struct {
	RateLimitRPM   int  `json:"rate_limit_rpm"`
	BlockMalicious bool `json:"block_malicious"`
	SQLGuillotine  bool `json:"sql_guillotine"`
}

type PolicyManager struct {
	client *redis.Client
	ctx    context.Context
	mu     sync.RWMutex
	cache  map[string]*PolicyConfig
}

func NewPolicyManager(client *redis.Client) *PolicyManager {
	pm := &PolicyManager{
		client: client,
		ctx:    context.Background(),
		cache:  make(map[string]*PolicyConfig),
	}
	if client != nil {
		go pm.listenForUpdates()
	}
	return pm
}

func (pm *PolicyManager) GetPolicy(tenantID string) *PolicyConfig {
	if tenantID == "" {
		return &PolicyConfig{RateLimitRPM: 1000, BlockMalicious: true}
	}

	pm.mu.RLock()
	config, ok := pm.cache[tenantID]
	pm.mu.RUnlock()

	if ok {
		return config
	}

	if pm.client != nil {
		res, err := pm.client.HGetAll(pm.ctx, "aegis:proxy:config:"+tenantID).Result()
		if err == nil && len(res) > 0 {
			rpm, _ := strconv.Atoi(res["rate_limit_rpm"])
			if rpm < 0 { rpm = 1000 }
			config = &PolicyConfig{
				RateLimitRPM:   rpm,
				BlockMalicious: res["block_malicious"] == "true",
				SQLGuillotine:  res["sql_guillotine"] == "true",
			}
			pm.mu.Lock()
			pm.cache[tenantID] = config
			pm.mu.Unlock()
			return config
		}
	}

	return &PolicyConfig{
		RateLimitRPM:   1000,
		BlockMalicious: true,
	}
}

func (pm *PolicyManager) listenForUpdates() {
	pubsub := pm.client.PSubscribe(pm.ctx, "policy:updates:*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for msg := range ch {
		tenantID := msg.Channel[len("policy:updates:"):]
		var config PolicyConfig
		if err := json.Unmarshal([]byte(msg.Payload), &config); err != nil {
			slog.Error("Failed to parse policy update payload", slog.Any("error", err))
			continue
		}
		
		if config.RateLimitRPM < 0 {
			config.RateLimitRPM = 1000
		}

		pm.mu.Lock()
		pm.cache[tenantID] = &config
		pm.mu.Unlock()
		
		slog.Info("Policy updated for tenant", slog.String("tenant_id", tenantID), slog.Int("rate_limit_rpm", config.RateLimitRPM))
	}
}
