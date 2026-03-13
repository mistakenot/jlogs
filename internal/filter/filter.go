package filter

import (
	"fmt"
	"path/filepath"
	"time"
)

// TimeFilter constrains log entries by timestamp.
// Zero values mean no bound on that side.
type TimeFilter struct {
	After  time.Time // zero value = no lower bound
	Before time.Time // zero value = no upper bound
}

// Config holds the full set of filter criteria.
type Config struct {
	AppPattern string     // glob pattern, e.g. "cc*", "web", "*"
	Time       TimeFilter
}

// MatchApp reports whether appName matches the glob pattern using
// filepath.Match semantics. An empty pattern matches everything.
func MatchApp(pattern, appName string) bool {
	if pattern == "" {
		return true
	}
	matched, err := filepath.Match(pattern, appName)
	if err != nil {
		return false
	}
	return matched
}

// MatchTime reports whether t falls within the bounds of the filter.
// A zero After means no lower bound; a zero Before means no upper bound.
// Both zero means always true.
func MatchTime(filter TimeFilter, t time.Time) bool {
	if !filter.After.IsZero() && t.Before(filter.After) {
		return false
	}
	if !filter.Before.IsZero() && t.After(filter.Before) {
		return false
	}
	return true
}

// ParseSince parses a duration string like "10m", "2h", or "60s".
// It delegates to time.ParseDuration.
func ParseSince(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return d, nil
}

// NewTimeFilterSince creates a TimeFilter covering the last d duration
// relative to the current time.
func NewTimeFilterSince(d time.Duration) TimeFilter {
	return TimeFilter{
		After: time.Now().Add(-d),
	}
}

// NewTimeFilterAbsolute creates a TimeFilter from RFC 3339 timestamp strings.
// Either string can be empty, meaning no bound on that side.
func NewTimeFilterAbsolute(after, before string) (TimeFilter, error) {
	var tf TimeFilter
	var err error

	if after != "" {
		tf.After, err = time.Parse(time.RFC3339, after)
		if err != nil {
			return TimeFilter{}, fmt.Errorf("parsing 'after' time: %w", err)
		}
	}

	if before != "" {
		tf.Before, err = time.Parse(time.RFC3339, before)
		if err != nil {
			return TimeFilter{}, fmt.Errorf("parsing 'before' time: %w", err)
		}
	}

	return tf, nil
}
