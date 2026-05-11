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

func TestBucketNotificationConfigurationAllTargetTypes(t *testing.T) {
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
					Id:       aws.String("topic-created"),
					TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:created-topic"),
					Events: []types.Event{
						types.EventS3ObjectCreatedPut,
					},
				},
				{
					Id:       aws.String("topic-removed"),
					TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:removed-topic"),
					Events: []types.Event{
						types.EventS3ObjectRemovedDelete,
					},
				},
			},
			QueueConfigurations: []types.QueueConfiguration{
				{
					Id:       aws.String("queue-created"),
					QueueArn: aws.String("arn:aws:sqs:us-east-1:123456789012:created-queue"),
					Events: []types.Event{
						types.EventS3ObjectCreated,
					},
				},
				{
					Id:       aws.String("queue-tagging"),
					QueueArn: aws.String("arn:aws:sqs:us-east-1:123456789012:tagging-queue"),
					Events: []types.Event{
						types.EventS3ObjectTaggingPut,
					},
					Filter: &types.NotificationConfigurationFilter{
						Key: &types.S3KeyFilter{
							FilterRules: []types.FilterRule{
								{
									Name:  types.FilterRuleNamePrefix,
									Value: aws.String("events/"),
								},
							},
						},
					},
				},
			},
			LambdaFunctionConfigurations: []types.LambdaFunctionConfiguration{
				{
					Id:                aws.String("lambda-created"),
					LambdaFunctionArn: aws.String("arn:aws:lambda:us-east-1:123456789012:function:created-handler"),
					Events: []types.Event{
						types.EventS3ObjectCreatedCompleteMultipartUpload,
					},
				},
			},
			EventBridgeConfiguration: &types.EventBridgeConfiguration{},
		},
	})
	require.NoError(t, err)

	result, err := client.GetBucketNotificationConfiguration(ctx, &s3.GetBucketNotificationConfigurationInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)

	require.Len(t, result.TopicConfigurations, 2)
	assert.Equal(t, "topic-created", *result.TopicConfigurations[0].Id)
	assert.Equal(t, "arn:aws:sns:us-east-1:123456789012:created-topic", *result.TopicConfigurations[0].TopicArn)
	assert.Equal(t, []types.Event{types.EventS3ObjectCreatedPut}, result.TopicConfigurations[0].Events)
	assert.Equal(t, "topic-removed", *result.TopicConfigurations[1].Id)
	assert.Equal(t, "arn:aws:sns:us-east-1:123456789012:removed-topic", *result.TopicConfigurations[1].TopicArn)
	assert.Equal(t, []types.Event{types.EventS3ObjectRemovedDelete}, result.TopicConfigurations[1].Events)

	require.Len(t, result.QueueConfigurations, 2)
	assert.Equal(t, "queue-created", *result.QueueConfigurations[0].Id)
	assert.Equal(t, "arn:aws:sqs:us-east-1:123456789012:created-queue", *result.QueueConfigurations[0].QueueArn)
	assert.Equal(t, []types.Event{types.EventS3ObjectCreated}, result.QueueConfigurations[0].Events)
	assert.Equal(t, "queue-tagging", *result.QueueConfigurations[1].Id)
	assert.Equal(t, "arn:aws:sqs:us-east-1:123456789012:tagging-queue", *result.QueueConfigurations[1].QueueArn)
	assert.Equal(t, []types.Event{types.EventS3ObjectTaggingPut}, result.QueueConfigurations[1].Events)
	require.NotNil(t, result.QueueConfigurations[1].Filter)
	require.NotNil(t, result.QueueConfigurations[1].Filter.Key)
	require.Len(t, result.QueueConfigurations[1].Filter.Key.FilterRules, 1)
	assert.Equal(t, types.FilterRuleNamePrefix, result.QueueConfigurations[1].Filter.Key.FilterRules[0].Name)
	assert.Equal(t, "events/", *result.QueueConfigurations[1].Filter.Key.FilterRules[0].Value)

	require.Len(t, result.LambdaFunctionConfigurations, 1)
	lambda := result.LambdaFunctionConfigurations[0]
	assert.Equal(t, "lambda-created", *lambda.Id)
	assert.Equal(t, "arn:aws:lambda:us-east-1:123456789012:function:created-handler", *lambda.LambdaFunctionArn)
	assert.Equal(t, []types.Event{types.EventS3ObjectCreatedCompleteMultipartUpload}, lambda.Events)
	assert.NotNil(t, result.EventBridgeConfiguration)
}

func TestBucketNotificationConfigurationOverwriteAndClear(t *testing.T) {
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
					Id:       aws.String("initial"),
					TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:initial-topic"),
					Events: []types.Event{
						types.EventS3ObjectCreatedPut,
					},
				},
			},
		},
	})
	require.NoError(t, err)

	_, err = client.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String(bucketName),
		NotificationConfiguration: &types.NotificationConfiguration{
			QueueConfigurations: []types.QueueConfiguration{
				{
					Id:       aws.String("replacement"),
					QueueArn: aws.String("arn:aws:sqs:us-east-1:123456789012:replacement-queue"),
					Events: []types.Event{
						types.EventS3ObjectRemovedDelete,
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
	assert.Empty(t, result.TopicConfigurations)
	require.Len(t, result.QueueConfigurations, 1)
	assert.Equal(t, "replacement", *result.QueueConfigurations[0].Id)
	assert.Equal(t, "arn:aws:sqs:us-east-1:123456789012:replacement-queue", *result.QueueConfigurations[0].QueueArn)

	_, err = client.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{
		Bucket:                    aws.String(bucketName),
		NotificationConfiguration: &types.NotificationConfiguration{},
	})
	require.NoError(t, err)

	result, err = client.GetBucketNotificationConfiguration(ctx, &s3.GetBucketNotificationConfigurationInput{
		Bucket: aws.String(bucketName),
	})
	require.NoError(t, err)
	assert.Empty(t, result.TopicConfigurations)
	assert.Empty(t, result.QueueConfigurations)
	assert.Empty(t, result.LambdaFunctionConfigurations)
	assert.Nil(t, result.EventBridgeConfiguration)
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

	_, err = client.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{
		Bucket: aws.String("missing-bucket"),
		NotificationConfiguration: &types.NotificationConfiguration{
			TopicConfigurations: []types.TopicConfiguration{
				{
					TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:topic"),
					Events: []types.Event{
						types.EventS3ObjectCreatedPut,
					},
				},
			},
		},
	})
	require.Error(t, err)

	if assert.ErrorAs(t, err, &apiErr) {
		assert.Equal(t, "NoSuchBucket", apiErr.ErrorCode())
	}
}
