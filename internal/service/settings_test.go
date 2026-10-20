package service

import (
	"context"
	"errors"
	"testing"

	"github.com/webitel/webitel-kb/internal/model"
	"github.com/webitel/webitel-kb/internal/store"
)

// settingsStub applies the settings of a domain that has set none, except for
// a history size given in limit.
type settingsStub struct {
	limit int
}

func (s settingsStub) VersionLimit(context.Context, int64) int {
	if s.limit > 0 {
		return s.limit
	}

	return model.DefaultVersionLimit
}

func (settingsStub) ResolveTagMatch(_ context.Context, _ int64, match model.TagMatch, _ []string) model.TagMatch {
	if match == model.TagMatchDefault {
		return model.TagMatchAny
	}

	return match
}

// settingStoreFake plays back one stored value and counts the reads.
type settingStoreFake struct {
	value []byte
	err   error
	reads int
}

func (f *settingStoreFake) Value(_ context.Context, _ int64, _ string) ([]byte, error) {
	f.reads++

	return f.value, f.err
}

type settingsUow struct {
	store.UnitOfWork

	settings *settingStoreFake
}

func (u *settingsUow) SettingStore() store.SettingStore { return u.settings }

func TestResolveTagMatch(t *testing.T) {
	tests := []struct {
		name      string
		stored    []byte
		storeErr  error
		match     model.TagMatch
		tags      []string
		want      model.TagMatch
		wantReads int
	}{
		{name: "explicit all ignores the domain", stored: []byte(`"OR"`), match: model.TagMatchAll, tags: []string{"a", "b"}, want: model.TagMatchAll},
		{name: "explicit any ignores the domain", stored: []byte(`"AND"`), match: model.TagMatchAny, tags: []string{"a", "b"}, want: model.TagMatchAny},
		{name: "default takes and", stored: []byte(`"AND"`), tags: []string{"a", "b"}, want: model.TagMatchAll, wantReads: 1},
		{name: "default takes or", stored: []byte(`"OR"`), tags: []string{"a", "b"}, want: model.TagMatchAny, wantReads: 1},
		{name: "unset domain matches any", tags: []string{"a", "b"}, want: model.TagMatchAny, wantReads: 1},
		{name: "invalid value matches any", stored: []byte(`"and"`), tags: []string{"a", "b"}, want: model.TagMatchAny, wantReads: 1},
		{name: "failed read matches any", storeErr: errors.New("down"), tags: []string{"a", "b"}, want: model.TagMatchAny, wantReads: 1},
		{name: "one tag skips the read", stored: []byte(`"AND"`), tags: []string{"a"}, want: model.TagMatchAny},
		{name: "no tags skip the read", stored: []byte(`"AND"`), want: model.TagMatchAny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &settingStoreFake{value: tt.stored, err: tt.storeErr}

			settings, err := NewDomainSettings(&settingsUow{settings: fake}, discardLogger())
			if err != nil {
				t.Fatalf("NewDomainSettings: %v", err)
			}
			defer settings.Close()

			if got := settings.ResolveTagMatch(context.Background(), 5, tt.match, tt.tags); got != tt.want {
				t.Fatalf("ResolveTagMatch = %v, want %v", got, tt.want)
			}

			if fake.reads != tt.wantReads {
				t.Fatalf("reads = %d, want %d", fake.reads, tt.wantReads)
			}
		})
	}
}

func TestResolveTagMatchRetriesAFailedRead(t *testing.T) {
	fake := &settingStoreFake{err: errors.New("down")}

	settings, err := NewDomainSettings(&settingsUow{settings: fake}, discardLogger())
	if err != nil {
		t.Fatalf("NewDomainSettings: %v", err)
	}
	defer settings.Close()

	tags := []string{"a", "b"}
	settings.ResolveTagMatch(context.Background(), 5, model.TagMatchDefault, tags)

	// A failure is not remembered: the next request reads the value again.
	fake.value, fake.err = []byte(`"AND"`), nil

	if got := settings.ResolveTagMatch(context.Background(), 5, model.TagMatchDefault, tags); got != model.TagMatchAll {
		t.Fatalf("ResolveTagMatch after recovery = %v, want all", got)
	}

	if fake.reads != 2 {
		t.Fatalf("reads = %d, want 2", fake.reads)
	}
}

func TestVersionLimit(t *testing.T) {
	tests := []struct {
		name     string
		stored   []byte
		storeErr error
		want     int
	}{
		{name: "stored limit", stored: []byte(`3`), want: 3},
		{name: "unset domain", want: model.DefaultVersionLimit},
		{name: "invalid value", stored: []byte(`"3"`), want: model.DefaultVersionLimit},
		{name: "failed read", storeErr: errors.New("down"), want: model.DefaultVersionLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings, err := NewDomainSettings(&settingsUow{settings: &settingStoreFake{value: tt.stored, err: tt.storeErr}}, discardLogger())
			if err != nil {
				t.Fatalf("NewDomainSettings: %v", err)
			}
			defer settings.Close()

			if got := settings.VersionLimit(context.Background(), 5); got != tt.want {
				t.Fatalf("VersionLimit = %d, want %d", got, tt.want)
			}
		})
	}
}
