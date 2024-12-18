package service

import (
	"context"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/constants"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/exceptions"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/log"
	"fernandoglatz/aws-infrastructure-helper/internal/infrastructure/api"
	"fernandoglatz/aws-infrastructure-helper/internal/infrastructure/config"
	"fmt"
	"net"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

type HelperService struct {
	fetcherApi                   *api.FetcherApi
	ispFallback                  *bool
	autoScalingGroupShutdownTime *time.Time
}

func NewHelperService() *HelperService {
	fetcherApi := api.NewFetcherApi()

	return &HelperService{
		fetcherApi: fetcherApi,
	}
}

func (service *HelperService) ScheduleDNSUpdater(ctx *context.Context) error {
	dnsUpdater := config.ApplicationConfig.Application.DNSUpdater
	hostedZoneIds := dnsUpdater.Record.HostedZoneIds
	recordTTL := dnsUpdater.Record.TTL
	recordName := dnsUpdater.Record.Name
	checkInterval := dnsUpdater.CheckInterval

	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for range ticker.C {
			log.Info(ctx).Msg("Checking DNS...")

			changed, publicIp, errw := service.isDnsChanged(ctx, recordName)
			if errw != nil {
				log.Error(ctx).Msg(fmt.Sprintf("Error on checking DNS: %v", errw.GetMessage()))
			}

			if changed || errw != nil {
				awsConfig, errw := service.getAWSConfig(ctx)
				if errw != nil {
					log.Error(ctx).Msg(fmt.Sprintf("Error on getting AWS config: %v", errw.GetMessage()))
					return
				}

				client := route53.NewFromConfig(*awsConfig)
				rrType := route53types.RRTypeA

				for _, hostedZoneId := range hostedZoneIds {
					errw := service.updateDNS(ctx, client, hostedZoneId, recordName, publicIp, rrType, recordTTL)
					if errw != nil {
						log.Error(ctx).Msg(fmt.Sprintf("Error on updating DNS: %v", errw.GetMessage()))
					}
				}

			} else {
				log.Info(ctx).Msg(fmt.Sprintf("DNS %s is up to date with public IP %s", recordName, publicIp))
			}
		}
	}()

	return nil
}

func (service *HelperService) ScheduleISPFallback(ctx *context.Context) error {
	ispFallbackUpdater := config.ApplicationConfig.Application.ISPFallbackUpdater
	checkInterval := ispFallbackUpdater.CheckInterval
	autoscalingGroupName := ispFallbackUpdater.EC2.AutoScalingGroup.Name

	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for range ticker.C {
			log.Info(ctx).Msg("Checking ISP ports...")

			closed := service.isPortClosed(ctx)

			if (service.ispFallback == nil || *service.ispFallback) && !closed {
				errw := service.disableISPFallback(ctx)
				if errw != nil {
					log.Error(ctx).Msg(fmt.Sprintf("Error on disabling ISP fallback: %v", errw.GetMessage()))
					service.ispFallback = nil
				} else {
					pointer := false
					service.ispFallback = &pointer
				}

			} else if (service.ispFallback == nil || !*service.ispFallback) && closed {
				errw := service.enableISPFallback(ctx)
				if errw != nil {
					log.Error(ctx).Msg(fmt.Sprintf("Error on enabling ISP fallback: %v", errw.GetMessage()))
					service.ispFallback = nil
				} else {
					pointer := true
					service.ispFallback = &pointer
				}
			} else if closed {
				log.Info(ctx).Msg("ISP ports are closed")
			} else {
				log.Info(ctx).Msg("ISP ports are open")
			}

			if service.autoScalingGroupShutdownTime != nil && time.Now().After(*service.autoScalingGroupShutdownTime) {
				awsConfig, errw := service.getAWSConfig(ctx)
				if errw != nil {
					log.Error(ctx).Msg(fmt.Sprintf("Error on getting AWS config: %v", errw.GetMessage()))
					return
				}

				errw = service.updateAutoScallingGroup(ctx, awsConfig, autoscalingGroupName, constants.ZERO)
				if errw != nil {
					log.Error(ctx).Msg(fmt.Sprintf("Error on shutting down Auto Scaling Group: %v", errw.GetMessage()))
					return
				}

				service.autoScalingGroupShutdownTime = nil
			}
		}
	}()

	return nil
}

func (service *HelperService) isDnsChanged(ctx *context.Context, domainName string) (bool, string, *exceptions.WrappedError) {
	publicIp, erra := service.fetcherApi.GetPublicIp(ctx)
	if erra != nil {
		return false, publicIp, erra.ToWrappedError(ctx)
	}

	resolvedIp, errw := service.resolveDNS(ctx, domainName)
	if errw != nil {
		return false, publicIp, errw
	}

	if publicIp != resolvedIp {
		log.Info(ctx).Msg(fmt.Sprintf("Public IP changed from %s to %s", resolvedIp, publicIp))
		return true, publicIp, nil
	}

	return false, publicIp, nil
}

func (service *HelperService) resolveDNS(ctx *context.Context, domainName string) (string, *exceptions.WrappedError) {
	log.Info(ctx).Msg(fmt.Sprintf("Resolving DNS for domain name: %s", domainName))

	ips, err := net.LookupIP(domainName)
	if err != nil {
		return "", &exceptions.WrappedError{
			Error: err,
		}
	}

	return ips[constants.ZERO].String(), nil
}

func (service *HelperService) updateDNS(ctx *context.Context, client *route53.Client, hostedZoneId string, recordName string, value string, rrtype route53types.RRType, ttl int64) *exceptions.WrappedError {
	log.Info(ctx).Msg(fmt.Sprintf("Updating DNS %s with value %s for hosted zone %s", recordName, value, hostedZoneId))

	changeBatch := &route53types.ChangeBatch{
		Changes: []route53types.Change{
			{
				Action: route53types.ChangeActionUpsert,
				ResourceRecordSet: &route53types.ResourceRecordSet{
					Name: &recordName,
					Type: rrtype,
					TTL:  aws.Int64(ttl),
					ResourceRecords: []route53types.ResourceRecord{
						{
							Value: &value,
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

	log.Info(ctx).Msg(fmt.Sprintf("DNS record for %s updated with value: %s", recordName, value))
	return nil
}

func (service *HelperService) getAWSConfig(ctx *context.Context) (*aws.Config, *exceptions.WrappedError) {
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

func (service *HelperService) isPortClosed(ctx *context.Context) bool {
	erra := service.fetcherApi.Fetch(ctx)
	return erra != nil && erra.Status == constants.ZERO
}

func (service *HelperService) enableISPFallback(ctx *context.Context) *exceptions.WrappedError {
	log.Info(ctx).Msg("Enabling ISP fallback")

	errw := service.changeISPFallback(ctx, true)
	if errw == nil {
		log.Info(ctx).Msg("ISP fallback enabled")
	}

	return errw
}

func (service *HelperService) disableISPFallback(ctx *context.Context) *exceptions.WrappedError {
	log.Info(ctx).Msg("Disabling ISP fallback")

	errw := service.changeISPFallback(ctx, false)
	if errw == nil {
		log.Info(ctx).Msg("ISP fallback disabled")
	}

	return errw
}

func (service *HelperService) changeISPFallback(ctx *context.Context, fallback bool) *exceptions.WrappedError {
	ispFallbackUpdater := config.ApplicationConfig.Application.ISPFallbackUpdater
	hostedZoneIds := ispFallbackUpdater.Record.HostedZoneIds
	recordName := ispFallbackUpdater.Record.Name
	recordValue := ispFallbackUpdater.Record.Value.Normal
	recordTTL := ispFallbackUpdater.Record.TTL
	rrType := route53types.RRTypeCname
	autoscalingGroupName := ispFallbackUpdater.EC2.AutoScalingGroup.Name
	autoScalingGroupShutdownTime := ispFallbackUpdater.EC2.AutoScalingGroup.ShutdownTime
	distributionId := ispFallbackUpdater.Cloudfront.DistributionId
	distributionOrigin := ispFallbackUpdater.Cloudfront.Origin.Normal

	awsConfig, errw := service.getAWSConfig(ctx)
	if errw != nil {
		return errw
	}

	if fallback {
		service.autoScalingGroupShutdownTime = nil
		recordValue = ispFallbackUpdater.Record.Value.Fallback
		distributionOrigin = ispFallbackUpdater.Cloudfront.Origin.Fallback

		errw = service.updateAutoScallingGroup(ctx, awsConfig, autoscalingGroupName, constants.ONE)
		if errw != nil {
			return errw
		}

		errw = service.updateCloudfrontDistribution(ctx, awsConfig, distributionId, distributionOrigin)
		if errw != nil {
			return errw
		}

	} else {
		futureTime := time.Now().Add(autoScalingGroupShutdownTime)
		service.autoScalingGroupShutdownTime = &futureTime

		errw = service.updateCloudfrontDistribution(ctx, awsConfig, distributionId, distributionOrigin)
		if errw != nil {
			return errw
		}
	}

	client := route53.NewFromConfig(*awsConfig)
	for _, hostedZoneId := range hostedZoneIds {
		errw := service.updateDNS(ctx, client, hostedZoneId, recordName, recordValue, rrType, recordTTL)
		return errw
	}

	return nil
}

func (service *HelperService) updateAutoScallingGroup(ctx *context.Context, awsConfig *aws.Config, autoscalingGroupName string, desired int32) *exceptions.WrappedError {
	log.Info(ctx).Msg(fmt.Sprintf("Updating auto scaling group %s to desired capacity %d", autoscalingGroupName, desired))

	client := autoscaling.NewFromConfig(*awsConfig)
	input := &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String(autoscalingGroupName),
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

	log.Info(ctx).Msg(fmt.Sprintf("Updated auto scaling group %s to desired capacity %d", autoscalingGroupName, desired))

	return nil
}

func (service *HelperService) updateCloudfrontDistribution(ctx *context.Context, awsConfig *aws.Config, distributionId string, origin string) *exceptions.WrappedError {
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
