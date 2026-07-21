package queryobject

import "testing"

func TestCompactSQL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already compact", "SELECT 1", "SELECT 1"},
		{"spaces folded", "SELECT   id ,  name", "SELECT id,name"},
		{"newlines and tabs folded", "SELECT\n\tid,\n\tname\nFROM t", "SELECT id,name FROM t"},
		{"line comment stripped", "SELECT id -- the id\nFROM t", "SELECT id FROM t"},
		{"block comment stripped", "SELECT /* pick */ id FROM t", "SELECT id FROM t"},
		{"literal spaces preserved", "SELECT 'a  b' FROM t", "SELECT'a  b'FROM t"},
		{"literal with comment marker preserved", "SELECT '--x' FROM t", "SELECT'--x'FROM t"},
		{"trailing dash kept", "a-", "a-"},
		{"trailing slash kept", "x /", "x/"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompactSQL(tt.in); got != tt.want {
				t.Fatalf("CompactSQL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
