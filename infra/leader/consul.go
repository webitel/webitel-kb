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

func (c consulClient) Renew(ttl, sessionID string, done <-chan struct{}) error {
	return c.client.Session().RenewPeriodic(ttl, sessionID, nil, done)
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
