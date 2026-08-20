package leader

import (
	"context"
	"time"

	"github.com/hashicorp/consul/api"
)

// consulClient adapts the Consul API to the narrow surface of the elector.
type consulClient struct {
	client *api.Client
}

var _ sessionAPI = consulClient{}

func (c consulClient) Create(ctx context.Context, entry *api.SessionEntry) (string, error) {
	id, _, err := c.client.Session().Create(entry, (&api.WriteOptions{}).WithContext(ctx))

	return id, err
}

// Renew refreshes the session until done closes, bounding every request with a
// deadline. Unlike RenewPeriodic, a stalled request cannot hang teardown.
func (c consulClient) Renew(period, sessionID string, done <-chan struct{}) error {
	every, err := time.ParseDuration(period)
	if err != nil {
		return err
	}

	// Refresh twice per period; both stay under the session TTL.
	ticker := time.NewTicker(every / 2)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return nil
		case <-ticker.C:
			if err := c.renewOnce(sessionID, every); err != nil {
				return err
			}
		}
	}
}

func (c consulClient) renewOnce(sessionID string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	_, _, err := c.client.Session().Renew(sessionID, (&api.WriteOptions{}).WithContext(ctx))

	return err
}

func (c consulClient) Acquire(ctx context.Context, pair *api.KVPair) (bool, error) {
	acquired, _, err := c.client.KV().Acquire(pair, (&api.WriteOptions{}).WithContext(ctx))

	return acquired, err
}

// Watch blocks until the key changes past index or wait elapses, so a lost
// key is noticed at once instead of on the next poll.
func (c consulClient) Watch(
	ctx context.Context, key string, index uint64, wait time.Duration,
) (string, uint64, error) {
	opts := (&api.QueryOptions{WaitIndex: index, WaitTime: wait}).WithContext(ctx)

	pair, meta, err := c.client.KV().Get(key, opts)
	if err != nil {
		return "", 0, err
	}

	if pair == nil {
		return "", meta.LastIndex, nil
	}

	return pair.Session, meta.LastIndex, nil
}

func (c consulClient) Destroy(ctx context.Context, sessionID string) error {
	_, err := c.client.Session().Destroy(sessionID, (&api.WriteOptions{}).WithContext(ctx))

	return err
}
