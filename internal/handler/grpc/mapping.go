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
