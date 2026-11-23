package service

import (
	"context"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/exceptions"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/log"
	"fernandoglatz/aws-infrastructure-helper/internal/infrastructure/config"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// cloudGateway is the set of AWS operations the helper performs.
type cloudGateway interface {
	UpdateAutoScalingGroup(ctx *context.Context, name string, desired int32) *exceptions.WrappedError
	UpdateCloudfrontOrigin(ctx *context.Context, distributionId string, origin string) *exceptions.WrappedError
	UpsertRecord(ctx *context.Context, hostedZoneId string, name string, value string, rrType route53types.RRType, ttl int64) *exceptions.WrappedError
}

type awsGateway struct{}

func (gateway *awsGateway) getAWSConfig(ctx *context.Context) (*aws.Config, *exceptions.WrappedError) {
	awsConfig := config.ApplicationConfig.Aws
	accessKey := awsConfig.Credentials.AccessKey
	secretKey := awsConfig.Credentials.SecretKey
	region := awsConfig.Region

	cfg, err := awsconfig.LoadDefaultConfig(*ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, &exceptions.WrappedError{
			Error: err,
		}
	}

	return &cfg, nil
}

func (gateway *awsGateway) UpdateAutoScalingGroup(ctx *context.Context, name string, desired int32) *exceptions.WrappedError {
	log.Info(ctx).Msg(fmt.Sprintf("Updating auto scaling group %s to desired capacity %d", name, desired))

	awsConfig, errw := gateway.getAWSConfig(ctx)
	if errw != nil {
		return errw
	}

	client := autoscaling.NewFromConfig(*awsConfig)
	input := &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String(name),
		MinSize:              aws.Int32(desired),
		MaxSize:              aws.Int32(desired),
		DesiredCapacity:      aws.Int32(desired),
	}

	_, err := client.UpdateAutoScalingGroup(*ctx, input)
	if err != nil {
		return &exceptions.WrappedError{
			Error: err,
		}
	}

	log.Info(ctx).Msg(fmt.Sprintf("Updated auto scaling group %s to desired capacity %d", name, desired))

	return nil
}

func (gateway *awsGateway) UpdateCloudfrontOrigin(ctx *context.Context, distributionId string, origin string) *exceptions.WrappedError {
	awsConfig, errw := gateway.getAWSConfig(ctx)
	if errw != nil {
		return errw
	}

	client := cloudfront.NewFromConfig(*awsConfig)

	getInput := &cloudfront.GetDistributionConfigInput{
		Id: aws.String(distributionId),
	}

	getDistributionConfigOutput, err := client.GetDistributionConfig(*ctx, getInput)
	if err != nil {
		return &exceptions.WrappedError{
			Error: err,
		}
	}

	distributionConfig := getDistributionConfigOutput.DistributionConfig
	distributionConfig.DefaultCacheBehavior.TargetOriginId = aws.String(origin)

	input := &cloudfront.UpdateDistributionInput{
		Id:                 aws.String(distributionId),
		DistributionConfig: distributionConfig,
		IfMatch:            getDistributionConfigOutput.ETag,
	}

	_, err = client.UpdateDistribution(*ctx, input)
	if err != nil {
		return &exceptions.WrappedError{
			Error: err,
		}
	}

	return nil
}

func (gateway *awsGateway) UpsertRecord(ctx *context.Context, hostedZoneId string, name string, value string, rrType route53types.RRType, ttl int64) *exceptions.WrappedError {
	log.Info(ctx).Msg(fmt.Sprintf("Updating DNS %s with value %s for hosted zone %s", name, value, hostedZoneId))

	awsConfig, errw := gateway.getAWSConfig(ctx)
	if errw != nil {
		return errw
	}

	client := route53.NewFromConfig(*awsConfig)

	changeBatch := &route53types.ChangeBatch{
		Changes: []route53types.Change{
			{
				Action: route53types.ChangeActionUpsert,
				ResourceRecordSet: &route53types.ResourceRecordSet{
					Name: aws.String(name),
					Type: rrType,
					TTL:  aws.Int64(ttl),
					ResourceRecords: []route53types.ResourceRecord{
						{
							Value: aws.String(value),
						},
					},
				},
			},
		},
	}

	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(hostedZoneId),
		ChangeBatch:  changeBatch,
	}

	_, err := client.ChangeResourceRecordSets(*ctx, input)
	if err != nil {
		return &exceptions.WrappedError{
			Error: err,
		}
	}

	log.Info(ctx).Msg(fmt.Sprintf("DNS record for %s updated with value: %s", name, value))
	return nil
}
