package filter

import (
	"testing"
	"time"
)

func TestMatchApp(t *testing.T) {
	tests := []struct {
		pattern string
		app     string
		want    bool
	}{
		{"web", "web", true},
		{"cc*", "cctrace", true},
		{"cc*", "claude-canvas", false},
		{"*", "anything", true},
		{"db*", "db", true},
		{"db*", "db.migrate", true},
		{"", "anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.app, func(t *testing.T) {
			got := MatchApp(tt.pattern, tt.app)
			if got != tt.want {
				t.Errorf("MatchApp(%q, %q) = %v, want %v", tt.pattern, tt.app, got, tt.want)
			}
		})
	}
}

func TestParseSince(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"10m", 10 * time.Minute, false},
		{"2h", 2 * time.Hour, false},
		{"60s", 60 * time.Second, false},
		{"abc", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseSince(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSince(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseSince(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMatchTime(t *testing.T) {
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	before := now.Add(-1 * time.Hour)
	after := now.Add(1 * time.Hour)

	t.Run("only After set, time in range", func(t *testing.T) {
		f := TimeFilter{After: before}
		if !MatchTime(f, now) {
			t.Error("expected match")
		}
	})

	t.Run("only After set, time out of range", func(t *testing.T) {
		f := TimeFilter{After: after}
		if MatchTime(f, now) {
			t.Error("expected no match")
		}
	})

	t.Run("only Before set, time in range", func(t *testing.T) {
		f := TimeFilter{Before: after}
		if !MatchTime(f, now) {
			t.Error("expected match")
		}
	})

	t.Run("only Before set, time out of range", func(t *testing.T) {
		f := TimeFilter{Before: before}
		if MatchTime(f, now) {
			t.Error("expected no match")
		}
	})

	t.Run("both set, time in range", func(t *testing.T) {
		f := TimeFilter{After: before, Before: after}
		if !MatchTime(f, now) {
			t.Error("expected match")
		}
	})

	t.Run("both set, time out of range", func(t *testing.T) {
		f := TimeFilter{After: after, Before: after.Add(time.Hour)}
		if MatchTime(f, now) {
			t.Error("expected no match")
		}
	})

	t.Run("neither set, always true", func(t *testing.T) {
		f := TimeFilter{}
		if !MatchTime(f, now) {
			t.Error("expected match with zero filter")
		}
	})
}

func TestNewTimeFilterAbsolute(t *testing.T) {
	t.Run("valid RFC3339 strings", func(t *testing.T) {
		afterStr := "2025-06-15T10:00:00Z"
		beforeStr := "2025-06-15T14:00:00Z"
		tf, err := NewTimeFilterAbsolute(afterStr, beforeStr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantAfter, _ := time.Parse(time.RFC3339, afterStr)
		wantBefore, _ := time.Parse(time.RFC3339, beforeStr)
		if !tf.After.Equal(wantAfter) {
			t.Errorf("After = %v, want %v", tf.After, wantAfter)
		}
		if !tf.Before.Equal(wantBefore) {
			t.Errorf("Before = %v, want %v", tf.Before, wantBefore)
		}
	})

	t.Run("empty strings", func(t *testing.T) {
		tf, err := NewTimeFilterAbsolute("", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !tf.After.IsZero() {
			t.Errorf("After should be zero, got %v", tf.After)
		}
		if !tf.Before.IsZero() {
			t.Errorf("Before should be zero, got %v", tf.Before)
		}
	})

	t.Run("invalid after string", func(t *testing.T) {
		_, err := NewTimeFilterAbsolute("not-a-date", "")
		if err == nil {
			t.Error("expected error for invalid after string")
		}
	})

	t.Run("invalid before string", func(t *testing.T) {
		_, err := NewTimeFilterAbsolute("", "not-a-date")
		if err == nil {
			t.Error("expected error for invalid before string")
		}
	})
}
