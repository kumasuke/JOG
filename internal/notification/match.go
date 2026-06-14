package notification

import (
	"strings"

	"github.com/kumasuke/jog/internal/storage"
)

// matchedTarget is a notification configuration that subscribed to an event and
// whose key filter matched: the destination ARN to resolve plus the
// configuration ID echoed into the event's s3.configurationId.
type matchedTarget struct {
	arn             string
	configurationID string
}

// matchTargets returns every notification target in cfg that subscribes to
// eventName AND whose S3Key filter matches key. eventName is the on-the-wire form
// without the "s3:" prefix (e.g. "LifecycleExpiration:Delete"); the stored
// configuration subscribes with the "s3:"-prefixed form and may use a ":*"
// wildcard (e.g. "s3:LifecycleExpiration:*"). EventBridge is ignored here: it has
// no ARN/webhook destination in this model.
func matchTargets(cfg *storage.NotificationConfiguration, eventName, key string) []matchedTarget {
	if cfg == nil {
		return nil
	}
	var out []matchedTarget
	add := func(id, arn string, events []string, filter *storage.NotificationFilter) {
		if !eventSubscribed(events, eventName) {
			return
		}
		if !keyFilterMatches(filter, key) {
			return
		}
		out = append(out, matchedTarget{arn: arn, configurationID: id})
	}
	for _, c := range cfg.TopicConfigurations {
		add(c.ID, c.TopicArn, c.Events, c.Filter)
	}
	for _, c := range cfg.QueueConfigurations {
		add(c.ID, c.QueueArn, c.Events, c.Filter)
	}
	for _, c := range cfg.LambdaFunctionConfigurations {
		add(c.ID, c.LambdaFunctionArn, c.Events, c.Filter)
	}
	return out
}

// eventSubscribed reports whether any configured event matches the on-the-wire
// eventName. A configured event "s3:Foo:Bar" matches eventName "Foo:Bar"; a
// configured wildcard ending in "*" ("s3:Foo:*", or the catch-all "s3:*")
// matches any eventName sharing the literal prefix before the "*".
func eventSubscribed(configured []string, eventName string) bool {
	for _, ev := range configured {
		want := strings.TrimPrefix(ev, "s3:")
		if want == eventName {
			return true
		}
		if strings.HasSuffix(want, "*") {
			prefix := strings.TrimSuffix(want, "*")
			if strings.HasPrefix(eventName, prefix) {
				return true
			}
		}
	}
	return false
}

// keyFilterMatches applies an S3Key prefix/suffix filter to a key. A nil filter
// (or one with no rules) matches every key. Multiple rules are AND-combined, as
// in S3 (at most one prefix and one suffix are meaningful). Rule names are
// matched case-insensitively ("prefix"/"suffix").
func keyFilterMatches(filter *storage.NotificationFilter, key string) bool {
	if filter == nil || filter.Key == nil {
		return true
	}
	for _, rule := range filter.Key.FilterRules {
		switch strings.ToLower(rule.Name) {
		case "prefix":
			if !strings.HasPrefix(key, rule.Value) {
				return false
			}
		case "suffix":
			if !strings.HasSuffix(key, rule.Value) {
				return false
			}
		}
	}
	return true
}
