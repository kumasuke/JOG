package s3compat

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/kumasuke/jog/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPutGetBucketNotificationConfiguration(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	_, err := client.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String(bucketName),
		NotificationConfiguration: &types.NotificationConfiguration{
			TopicConfigurations: []types.TopicConfiguration{
				{
					Id:       aws.String("created-images"),
					TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:image-topic"),
					Events: []types.Event{
						types.EventS3ObjectCreatedPut,
					},
					Filter: &types.NotificationConfigurationFilter{
						Key: &types.S3KeyFilter{
							FilterRules: []types.FilterRule{
								{
									Name:  types.FilterRuleNamePrefix,
									Value: aws.String("images/"),
								},
								{
									Name:  types.FilterRuleNameSuffix,
									Value: aws.String(".jpg"),
								},
							},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	result, err := client.GetBucketNotificationConfiguration(ctx, &s3.GetBucketNotificationConfigurationInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	require.Len(t, result.TopicConfigurations, 1)
	topic := result.TopicConfigurations[0]
	assert.Equal(t, "created-images", *topic.Id)
	assert.Equal(t, "arn:aws:sns:us-east-1:123456789012:image-topic", *topic.TopicArn)
	assert.Equal(t, []types.Event{types.EventS3ObjectCreatedPut}, topic.Events)
	require.NotNil(t, topic.Filter)
	require.NotNil(t, topic.Filter.Key)
	require.Len(t, topic.Filter.Key.FilterRules, 2)
	assert.Equal(t, types.FilterRuleNamePrefix, topic.Filter.Key.FilterRules[0].Name)
	assert.Equal(t, "images/", *topic.Filter.Key.FilterRules[0].Value)
	assert.Equal(t, types.FilterRuleNameSuffix, topic.Filter.Key.FilterRules[1].Name)
	assert.Equal(t, ".jpg", *topic.Filter.Key.FilterRules[1].Value)
}

func TestGetBucketNotificationConfigurationEmpty(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	bucketName := testutil.RandomBucketName()
	cleanup := ts.CreateTestBucket(t, bucketName)
	defer cleanup()

	result, err := client.GetBucketNotificationConfiguration(ctx, &s3.GetBucketNotificationConfigurationInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)
	assert.Empty(t, result.TopicConfigurations)
	assert.Empty(t, result.QueueConfigurations)
	assert.Empty(t, result.LambdaFunctionConfigurations)
	assert.Nil(t, result.EventBridgeConfiguration)
}

func TestBucketNotificationConfigurationBucketNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	defer ts.Cleanup()

	client := ts.S3Client(t)
	ctx := context.Background()

	_, err := client.GetBucketNotificationConfiguration(ctx, &s3.GetBucketNotificationConfigurationInput{
		Bucket: aws.String("missing-bucket"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
	}
}
