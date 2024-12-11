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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/route53"
)

type HelperService struct {
	fetcherApi *api.FetcherApi
}

func NewHelperService() *HelperService {
	fetcherApi := api.NewFetcherApi()

	return &HelperService{
		fetcherApi: fetcherApi,
	}
}

func (service *HelperService) ScheduleDNSCheck(ctx context.Context) error {
	dnsUpdater := config.ApplicationConfig.Application.DNSUpdater
	recordName := dnsUpdater.Record.Name
	interval := dnsUpdater.Interval

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			log.Info(ctx).Msg("Checking DNS...")

			changed, publicIp, errw := service.isDnsChanged(ctx, recordName)
			if errw != nil {
				log.Error(ctx).Msg(fmt.Sprintf("Error on checking DNS: %v", errw.GetMessage()))
			}

			if changed || errw != nil {
				errw := service.updateDNS(ctx, recordName, publicIp)
				if errw != nil {
					log.Error(ctx).Msg(fmt.Sprintf("Error on updating DNS: %v", errw.GetMessage()))
				}
			} else {
				log.Info(ctx).Msg(fmt.Sprintf("DNS %s is up to date with public IP %s", recordName, publicIp))
			}
		}
	}()

	return nil
}

func (service *HelperService) isDnsChanged(ctx context.Context, domainName string) (bool, string, *exceptions.WrappedError) {
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

func (service *HelperService) resolveDNS(ctx context.Context, domainName string) (string, *exceptions.WrappedError) {
	log.Info(ctx).Msg(fmt.Sprintf("Resolving DNS for domain name: %s", domainName))

	ips, err := net.LookupIP(domainName)
	if err != nil {
		return "", &exceptions.WrappedError{
			Error: err,
		}
	}

	return ips[constants.ZERO].String(), nil
}

func (service *HelperService) updateDNS(ctx context.Context, recordName string, publicIp string) *exceptions.WrappedError {
	log.Info(ctx).Msg(fmt.Sprintf("Updating DNS %s with public IP: %s", recordName, publicIp))

	dnsUpdater := config.ApplicationConfig.Application.DNSUpdater
	record := dnsUpdater.Record
	hostedZoneId := record.HostedZoneId
	recordName = record.Name
	recordTTL := record.TTL

	awsConfig := config.ApplicationConfig.Aws
	accessKey := awsConfig.Credentials.AccessKey
	secretKey := awsConfig.Credentials.SecretKey
	region := awsConfig.Region

	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(region),
		Credentials: credentials.NewStaticCredentials(accessKey, secretKey, ""),
	})
	if err != nil {
		return &exceptions.WrappedError{
			Error: err,
		}
	}

	svc := route53.New(sess)
	recordType := "A"

	changeBatch := &route53.ChangeBatch{
		Changes: []*route53.Change{
			{
				Action: aws.String(route53.ChangeActionUpsert),
				ResourceRecordSet: &route53.ResourceRecordSet{
					Name: aws.String(recordName),
					Type: aws.String(recordType),
					TTL:  aws.Int64(recordTTL),
					ResourceRecords: []*route53.ResourceRecord{
						{
							Value: aws.String(publicIp),
						},
					},
				},
			},
		},
	}

	_, err = svc.ChangeResourceRecordSets(&route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(hostedZoneId),
		ChangeBatch:  changeBatch,
	})
	if err != nil {
		return &exceptions.WrappedError{
			Error: err,
		}
	}

	log.Info(ctx).Msg(fmt.Sprintf("DNS record for %s updated with public IP: %s", recordName, publicIp))
	return nil
}
