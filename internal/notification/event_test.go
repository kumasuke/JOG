package notification

import (
	"testing"
	"time"
)

func TestNewLifecycleEvent_KeyIsURLEncoded(t *testing.T) {
	// S3 delivers the object key URL-encoded (spaces become "+"), matching
	// url.QueryEscape; consumers decode it on receipt. We assert that contract so
	// a key like "a+b" stays distinguishable from "a b" after a decode round-trip.
	cases := []struct {
		name string
		key  string
		want string
	}{
		{"plain key is unchanged", "logs/2024/app.log", "logs%2F2024%2Fapp.log"},
		{"space becomes plus", "my photo.jpg", "my+photo.jpg"},
		{"literal plus is escaped", "a+b", "a%2Bb"},
		{"non-ascii is percent-encoded", "デ-タ.txt", "%E3%83%87-%E3%82%BF.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := NewLifecycleEvent(
				EventLifecycleExpirationDelete,
				"us-east-1", "bucket", "owner",
				tc.key, "etag", "", 7,
				time.Unix(0, 0).UTC(), "",
			)
			if got := ev.S3.Object.Key; got != tc.want {
				t.Fatalf("key = %q, want %q (raw %q)", got, tc.want, tc.key)
			}
		})
	}
}

func TestNewLifecycleEvent_StaticFields(t *testing.T) {
	ev := NewLifecycleEvent(
		EventLifecycleExpirationDeleteMarkerCreated,
		"ap-northeast-1", "b", "o",
		"k", "et", "v1", 0,
		time.Unix(0, 0).UTC(), "cfg-1",
	)
	if ev.EventVersion != "2.3" {
		t.Errorf("eventVersion = %q, want 2.3", ev.EventVersion)
	}
	if ev.EventSource != "aws:s3" {
		t.Errorf("eventSource = %q, want aws:s3", ev.EventSource)
	}
	if ev.EventName != EventLifecycleExpirationDeleteMarkerCreated {
		t.Errorf("eventName = %q, want %q", ev.EventName, EventLifecycleExpirationDeleteMarkerCreated)
	}
	if ev.AWSRegion != "ap-northeast-1" {
		t.Errorf("awsRegion = %q, want ap-northeast-1", ev.AWSRegion)
	}
	if ev.S3.Bucket.ARN != "arn:aws:s3:::b" {
		t.Errorf("bucket arn = %q, want arn:aws:s3:::b", ev.S3.Bucket.ARN)
	}
	if ev.S3.ConfigurationID != "cfg-1" {
		t.Errorf("configurationId = %q, want cfg-1", ev.S3.ConfigurationID)
	}
	if ev.S3.Object.VersionID != "v1" {
		t.Errorf("versionId = %q, want v1", ev.S3.Object.VersionID)
	}
}
