package postgres

import (
	"bytes"
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/webitel/webitel-go-kit/pkg/errors"
)

func TestSettingValue(t *testing.T) {
	tests := []struct {
		name     string
		row      fakeRow
		want     []byte
		wantCode codes.Code
	}{
		{name: "stored value", row: fakeRow{vals: []any{[]byte(`"AND"`)}}, want: []byte(`"AND"`)},
		{name: "unset setting", row: fakeRow{err: pgx.ErrNoRows}},
		{name: "null value", row: fakeRow{vals: []any{nil}}},
		{name: "failed read", row: fakeRow{err: stderrors.New("boom")}, wantCode: codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeQuerier{row: tt.row}
			s := &settingStore{db: f}

			got, err := s.Value(context.Background(), 5, "tags_filtration_mode")
			if tt.wantCode != codes.OK {
				if errors.Code(err) != tt.wantCode {
					t.Fatalf("error = %v, want %v", err, tt.wantCode)
				}

				return
			}

			if err != nil || !bytes.Equal(got, tt.want) {
				t.Fatalf("Value = %q, %v, want %q", got, err, tt.want)
			}

			if f.gotSQL != settingValueSQL || !reflect.DeepEqual(f.gotArgs, []any{int64(5), "tags_filtration_mode"}) {
				t.Fatalf("sql = %q, args = %v", f.gotSQL, f.gotArgs)
			}
		})
	}
}
