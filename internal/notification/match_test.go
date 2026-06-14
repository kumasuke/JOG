package notification

import (
	"testing"

	"github.com/kumasuke/jog/internal/storage"
)

func TestEventSubscribed(t *testing.T) {
	cases := []struct {
		name       string
		configured []string
		eventName  string
		want       bool
	}{
		{"exact-prefixed", []string{"s3:LifecycleExpiration:Delete"}, "LifecycleExpiration:Delete", true},
		{"wildcard-matches-delete", []string{"s3:LifecycleExpiration:*"}, "LifecycleExpiration:Delete", true},
		{"wildcard-matches-dm", []string{"s3:LifecycleExpiration:*"}, "LifecycleExpiration:DeleteMarkerCreated", true},
		{"wildcard-different-family", []string{"s3:ObjectRemoved:*"}, "LifecycleExpiration:Delete", false},
		{"exact-miss", []string{"s3:LifecycleExpiration:Delete"}, "LifecycleExpiration:DeleteMarkerCreated", false},
		{"unprefixed-config", []string{"LifecycleExpiration:Delete"}, "LifecycleExpiration:Delete", true},
		{"empty-config", nil, "LifecycleExpiration:Delete", false},
		{"multiple-one-matches", []string{"s3:ObjectCreated:*", "s3:LifecycleExpiration:Delete"}, "LifecycleExpiration:Delete", true},
		// A bare "s3:*"-style wildcard is not standard for these configs, but the
		// suffix logic should still behave: "s3:*" → want "*" → prefix "" matches all.
		{"top-wildcard", []string{"s3:*"}, "LifecycleExpiration:Delete", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := eventSubscribed(c.configured, c.eventName); got != c.want {
				t.Errorf("eventSubscribed(%v, %q) = %v, want %v", c.configured, c.eventName, got, c.want)
			}
		})
	}
}

func TestKeyFilterMatches(t *testing.T) {
	mk := func(rules ...storage.FilterRule) *storage.NotificationFilter {
		return &storage.NotificationFilter{Key: &storage.S3KeyFilter{FilterRules: rules}}
	}
	cases := []struct {
		name   string
		filter *storage.NotificationFilter
		key    string
		want   bool
	}{
		{"nil-filter", nil, "any/key", true},
		{"empty-rules", mk(), "any/key", true},
		{"prefix-match", mk(storage.FilterRule{Name: "prefix", Value: "logs/"}), "logs/a", true},
		{"prefix-miss", mk(storage.FilterRule{Name: "prefix", Value: "logs/"}), "data/a", false},
		{"suffix-match", mk(storage.FilterRule{Name: "suffix", Value: ".jpg"}), "a/b.jpg", true},
		{"suffix-miss", mk(storage.FilterRule{Name: "suffix", Value: ".jpg"}), "a/b.png", false},
		{"prefix-and-suffix-both", mk(
			storage.FilterRule{Name: "prefix", Value: "img/"},
			storage.FilterRule{Name: "suffix", Value: ".jpg"},
		), "img/cat.jpg", true},
		{"prefix-and-suffix-prefix-fails", mk(
			storage.FilterRule{Name: "prefix", Value: "img/"},
			storage.FilterRule{Name: "suffix", Value: ".jpg"},
		), "vid/cat.jpg", false},
		{"case-insensitive-name", mk(storage.FilterRule{Name: "Prefix", Value: "logs/"}), "logs/a", true},
		{"unknown-rule-name-ignored", mk(storage.FilterRule{Name: "weird", Value: "x"}), "logs/a", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := keyFilterMatches(c.filter, c.key); got != c.want {
				t.Errorf("keyFilterMatches = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMatchTargets(t *testing.T) {
	cfg := &storage.NotificationConfiguration{
		TopicConfigurations: []storage.TopicNotificationConfiguration{
			{ID: "topic-all-lifecycle", TopicArn: "arn:topic:1", Events: []string{"s3:LifecycleExpiration:*"}},
			{ID: "topic-logs-only", TopicArn: "arn:topic:2", Events: []string{"s3:LifecycleExpiration:Delete"},
				Filter: &storage.NotificationFilter{Key: &storage.S3KeyFilter{FilterRules: []storage.FilterRule{{Name: "prefix", Value: "logs/"}}}}},
		},
		QueueConfigurations: []storage.QueueNotificationConfiguration{
			{ID: "queue-created", QueueArn: "arn:queue:1", Events: []string{"s3:ObjectCreated:*"}},
		},
		LambdaFunctionConfigurations: []storage.LambdaFunctionNotificationConfiguration{
			{ID: "lambda-dm", LambdaFunctionArn: "arn:lambda:1", Events: []string{"s3:LifecycleExpiration:DeleteMarkerCreated"}},
		},
	}

	t.Run("delete-on-logs-key", func(t *testing.T) {
		got := matchTargets(cfg, "LifecycleExpiration:Delete", "logs/a")
		// topic-all-lifecycle (wildcard, no filter) + topic-logs-only (prefix matches).
		// queue-created subscribes to a different family; lambda-dm to a different subtype.
		ids := targetIDs(got)
		assertSet(t, ids, []string{"topic-all-lifecycle", "topic-logs-only"})
	})

	t.Run("delete-on-data-key", func(t *testing.T) {
		got := matchTargets(cfg, "LifecycleExpiration:Delete", "data/a")
		// topic-logs-only filtered out (prefix miss); only the wildcard topic matches.
		ids := targetIDs(got)
		assertSet(t, ids, []string{"topic-all-lifecycle"})
	})

	t.Run("delete-marker-created", func(t *testing.T) {
		got := matchTargets(cfg, "LifecycleExpiration:DeleteMarkerCreated", "any")
		// wildcard topic + the DM-specific lambda.
		ids := targetIDs(got)
		assertSet(t, ids, []string{"topic-all-lifecycle", "lambda-dm"})
	})

	t.Run("nil-config", func(t *testing.T) {
		if got := matchTargets(nil, "LifecycleExpiration:Delete", "k"); got != nil {
			t.Errorf("matchTargets(nil) = %v, want nil", got)
		}
	})

	t.Run("no-subscription", func(t *testing.T) {
		got := matchTargets(cfg, "LifecycleExpiration:Delete", "")
		// empty key: prefix "logs/" fails for topic-logs-only, wildcard topic matches.
		ids := targetIDs(got)
		assertSet(t, ids, []string{"topic-all-lifecycle"})
	})
}

func targetIDs(ts []matchedTarget) []string {
	var ids []string
	for _, t := range ts {
		ids = append(ids, t.configurationID)
	}
	return ids
}

func assertSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for _, g := range got {
		if !wantSet[g] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}
