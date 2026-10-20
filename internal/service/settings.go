package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/webitel/webitel-go-kit/pkg/cache"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/store"
)

// settingsTTL bounds how long a changed domain setting takes to apply.
const settingsTTL = 15 * time.Second

// TagMatchResolver settles the default match of a tag filter.
type TagMatchResolver interface {
	ResolveTagMatch(ctx context.Context, domainID int64, match model.TagMatch, tags []string) model.TagMatch
}

// VersionLimiter says how many latest versions an article history shows.
type VersionLimiter interface {
	VersionLimit(ctx context.Context, domainID int64) int
}

// ArticleSettings are the domain settings the article service applies.
type ArticleSettings interface {
	TagMatchResolver
	VersionLimiter
}

// settingKey names one setting of one domain.
type settingKey struct {
	domainID int64
	name     string
}

// DomainSettings reads the system settings of domains through a short cache;
// a domain without a setting gets the default.
type DomainSettings struct {
	values cache.Cache[settingKey, []byte]
	log    *slog.Logger
}

func NewDomainSettings(uow store.UnitOfWork, log *slog.Logger) (*DomainSettings, error) {
	values, err := cache.New[settingKey, []byte]().
		Name("kb_domain_settings").
		KeyFunc(func(k settingKey) string { return strconv.FormatInt(k.domainID, 10) + ":" + k.name }).
		L1(cache.RistrettoConfig{MaxCost: 10_000, TTL: settingsTTL}).
		WithLoader(func(ctx context.Context, k settingKey) ([]byte, error) {
			value, err := uow.SettingStore().Value(ctx, k.domainID, k.name)
			if err == nil && value == nil {
				return nil, cache.ErrNotFound
			}

			return value, err
		}, cache.LoadingConfig{NegativeTTL: settingsTTL}).
		Build()
	if err != nil {
		return nil, err
	}

	return &DomainSettings{values: values, log: log}, nil
}

// Close releases the cache.
func (s *DomainSettings) Close() error {
	return s.values.Close()
}

// ResolveTagMatch keeps an explicit match and settles the default one by the
// tags filtration mode of the domain, any tag when unset. Fewer than two tags
// match the same either way, so the setting is not read for them.
func (s *DomainSettings) ResolveTagMatch(
	ctx context.Context, domainID int64, match model.TagMatch, tags []string,
) model.TagMatch {
	if match != model.TagMatchDefault {
		return match
	}

	if len(tags) < 2 {
		return model.TagMatchAny
	}

	value, found := s.value(ctx, domainID, model.SettingTagsFiltrationMode)
	if !found {
		return model.TagMatchAny
	}

	mode, ok := model.ParseTagsFiltrationMode(value)
	if !ok {
		s.ignored(ctx, domainID, model.SettingTagsFiltrationMode, value)

		return model.TagMatchAny
	}

	return mode
}

// VersionLimit returns the history size of the domain, the default when unset.
func (s *DomainSettings) VersionLimit(ctx context.Context, domainID int64) int {
	value, found := s.value(ctx, domainID, model.SettingVersionLimit)
	if !found {
		return model.DefaultVersionLimit
	}

	limit, ok := model.ParseVersionLimit(value)
	if !ok {
		s.ignored(ctx, domainID, model.SettingVersionLimit, value)

		return model.DefaultVersionLimit
	}

	return limit
}

// ignored logs a stored value the knowledge base cannot apply.
func (s *DomainSettings) ignored(ctx context.Context, domainID int64, name string, value []byte) {
	s.log.WarnContext(ctx, "domain setting is ignored",
		slog.Int64("domain_id", domainID),
		slog.String("setting", name),
		slog.String("value", string(value)))
}

// value returns the stored value of a domain setting; a failed read counts as
// unset, so a request never fails over a setting.
func (s *DomainSettings) value(ctx context.Context, domainID int64, name string) ([]byte, bool) {
	value, _, err := s.values.Get(ctx, settingKey{domainID: domainID, name: name})
	if errors.Is(err, cache.ErrNotFound) {
		return nil, false
	}

	if err != nil {
		s.log.WarnContext(ctx, "domain setting is read as unset",
			slog.Int64("domain_id", domainID),
			slog.String("setting", name),
			slog.Any("error", err))

		return nil, false
	}

	return value, true
}
