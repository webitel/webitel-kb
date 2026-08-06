package grpc

import (
	"time"

	"github.com/webitel/webitel-kb/api/kb"
	"github.com/webitel/webitel-kb/internal/model"
)

// unixMilli renders a timestamp the way the contract carries it: epoch
// milliseconds, zero when unset.
func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

func lookupToProto(l *model.Lookup) *kb.Lookup {
	if l == nil {
		return nil
	}

	return &kb.Lookup{Id: l.ID, Name: l.Name}
}

func lookupsToProto(ls []model.Lookup) []*kb.Lookup {
	if len(ls) == 0 {
		return nil
	}

	out := make([]*kb.Lookup, 0, len(ls))
	for _, l := range ls {
		out = append(out, &kb.Lookup{Id: l.ID, Name: l.Name})
	}

	return out
}
