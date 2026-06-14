package api

import (
	"encoding/xml"
	"errors"
	"net/http"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// NotificationConfiguration is the XML structure for bucket notifications.
type NotificationConfiguration struct {
	XMLName                      xml.Name                         `xml:"NotificationConfiguration"`
	Xmlns                        string                           `xml:"xmlns,attr,omitempty"`
	TopicConfigurations          []TopicNotificationConfiguration `xml:"TopicConfiguration,omitempty"`
	QueueConfigurations          []QueueNotificationConfiguration `xml:"QueueConfiguration,omitempty"`
	LambdaFunctionConfigurations []LambdaFunctionConfiguration    `xml:"CloudFunctionConfiguration,omitempty"`
	EventBridgeConfiguration     *EventBridgeConfiguration        `xml:"EventBridgeConfiguration,omitempty"`
}

// TopicNotificationConfiguration is an SNS notification target.
type TopicNotificationConfiguration struct {
	ID     string              `xml:"Id,omitempty"`
	Topic  string              `xml:"Topic"`
	Events []string            `xml:"Event"`
	Filter *NotificationFilter `xml:"Filter,omitempty"`
}

// QueueNotificationConfiguration is an SQS notification target.
type QueueNotificationConfiguration struct {
	ID     string              `xml:"Id,omitempty"`
	Queue  string              `xml:"Queue"`
	Events []string            `xml:"Event"`
	Filter *NotificationFilter `xml:"Filter,omitempty"`
}

// LambdaFunctionConfiguration is a Lambda notification target.
type LambdaFunctionConfiguration struct {
	ID             string              `xml:"Id,omitempty"`
	LambdaFunction string              `xml:"CloudFunction"`
	Events         []string            `xml:"Event"`
	Filter         *NotificationFilter `xml:"Filter,omitempty"`
}

// EventBridgeConfiguration enables EventBridge notifications.
type EventBridgeConfiguration struct{}

// NotificationFilter stores S3 key filtering rules.
type NotificationFilter struct {
	Key *S3KeyFilter `xml:"S3Key,omitempty"`
}

// S3KeyFilter stores key filter rules.
type S3KeyFilter struct {
	FilterRules []FilterRule `xml:"FilterRule,omitempty"`
}

// FilterRule stores a single notification filter rule.
type FilterRule struct {
	Name  string `xml:"Name"`
	Value string `xml:"Value"`
}

// PutBucketNotification handles PUT /{bucket}?notification.
func (h *Handler) PutBucketNotification(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse request body (size-capped; see readXMLBody / MaxNotificationBodySize).
	body, s3err := readXMLBody(w, r, MaxNotificationBodySize)
	if s3err != nil {
		WriteErrorWithResource(w, s3err, "/"+bucket)
		return
	}

	var config NotificationConfiguration
	if len(body) > 0 {
		if err := xml.Unmarshal(body, &config); err != nil {
			WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket)
			return
		}
	}

	if err := h.storage.PutBucketNotification(r.Context(), bucket, toStorageNotification(&config)); err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

// GetBucketNotification handles GET /{bucket}?notification.
func (h *Handler) GetBucketNotification(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	config, err := h.storage.GetBucketNotification(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	response := fromStorageNotification(config)
	response.Xmlns = "http://s3.amazonaws.com/doc/2006-03-01/"

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetBucketNotification response")
	}
}

func toStorageNotification(config *NotificationConfiguration) *storage.NotificationConfiguration {
	if config == nil {
		return &storage.NotificationConfiguration{}
	}

	out := &storage.NotificationConfiguration{}
	if config.EventBridgeConfiguration != nil {
		out.EventBridgeConfiguration = &storage.EventBridgeNotificationConfiguration{}
	}
	for _, c := range config.TopicConfigurations {
		out.TopicConfigurations = append(out.TopicConfigurations, storage.TopicNotificationConfiguration{
			ID:       c.ID,
			TopicArn: c.Topic,
			Events:   append([]string(nil), c.Events...),
			Filter:   toStorageNotificationFilter(c.Filter),
		})
	}
	for _, c := range config.QueueConfigurations {
		out.QueueConfigurations = append(out.QueueConfigurations, storage.QueueNotificationConfiguration{
			ID:       c.ID,
			QueueArn: c.Queue,
			Events:   append([]string(nil), c.Events...),
			Filter:   toStorageNotificationFilter(c.Filter),
		})
	}
	for _, c := range config.LambdaFunctionConfigurations {
		out.LambdaFunctionConfigurations = append(out.LambdaFunctionConfigurations, storage.LambdaFunctionNotificationConfiguration{
			ID:                c.ID,
			LambdaFunctionArn: c.LambdaFunction,
			Events:            append([]string(nil), c.Events...),
			Filter:            toStorageNotificationFilter(c.Filter),
		})
	}
	return out
}

func fromStorageNotification(config *storage.NotificationConfiguration) NotificationConfiguration {
	out := NotificationConfiguration{}
	if config == nil {
		return out
	}
	if config.EventBridgeConfiguration != nil {
		out.EventBridgeConfiguration = &EventBridgeConfiguration{}
	}
	for _, c := range config.TopicConfigurations {
		out.TopicConfigurations = append(out.TopicConfigurations, TopicNotificationConfiguration{
			ID:     c.ID,
			Topic:  c.TopicArn,
			Events: append([]string(nil), c.Events...),
			Filter: fromStorageNotificationFilter(c.Filter),
		})
	}
	for _, c := range config.QueueConfigurations {
		out.QueueConfigurations = append(out.QueueConfigurations, QueueNotificationConfiguration{
			ID:     c.ID,
			Queue:  c.QueueArn,
			Events: append([]string(nil), c.Events...),
			Filter: fromStorageNotificationFilter(c.Filter),
		})
	}
	for _, c := range config.LambdaFunctionConfigurations {
		out.LambdaFunctionConfigurations = append(out.LambdaFunctionConfigurations, LambdaFunctionConfiguration{
			ID:             c.ID,
			LambdaFunction: c.LambdaFunctionArn,
			Events:         append([]string(nil), c.Events...),
			Filter:         fromStorageNotificationFilter(c.Filter),
		})
	}
	return out
}

func toStorageNotificationFilter(filter *NotificationFilter) *storage.NotificationFilter {
	if filter == nil || filter.Key == nil {
		return nil
	}
	out := &storage.NotificationFilter{Key: &storage.S3KeyFilter{}}
	for _, rule := range filter.Key.FilterRules {
		out.Key.FilterRules = append(out.Key.FilterRules, storage.FilterRule{
			Name:  rule.Name,
			Value: rule.Value,
		})
	}
	return out
}

func fromStorageNotificationFilter(filter *storage.NotificationFilter) *NotificationFilter {
	if filter == nil || filter.Key == nil {
		return nil
	}
	out := &NotificationFilter{Key: &S3KeyFilter{}}
	for _, rule := range filter.Key.FilterRules {
		out.Key.FilterRules = append(out.Key.FilterRules, FilterRule{
			Name:  rule.Name,
			Value: rule.Value,
		})
	}
	return out
}
