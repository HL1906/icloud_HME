package mail

import (
	"testing"
	"time"
)

func TestParseMessageDate(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 30, 0, 0, time.UTC)
	rfc3339 := now.Format(time.RFC3339)
	rfc1123z := now.Format(time.RFC1123Z)

	tests := []struct {
		name    string
		raw     string
		wantOK  bool
		wantUTC time.Time
	}{
		{"空白输入", "", false, time.Time{}},
		{"纯空格", "   ", false, time.Time{}},
		{"RFC3339", rfc3339, true, now},
		{"RFC1123Z", rfc1123z, true, now},
		{"RFC1123", now.Format(time.RFC1123), true, now},
		{"非日期文本", "not-a-date", false, time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMessageDate(tt.raw)
			if !tt.wantOK {
				if !got.IsZero() {
					t.Fatalf("parseMessageDate(%q) = %v, want zero", tt.raw, got)
				}
				return
			}
			if got.IsZero() {
				t.Fatalf("parseMessageDate(%q) = zero, want %v", tt.raw, tt.wantUTC)
			}
			if !got.Equal(tt.wantUTC) {
				t.Fatalf("parseMessageDate(%q) = %v, want %v", tt.raw, got, tt.wantUTC)
			}
		})
	}
}
