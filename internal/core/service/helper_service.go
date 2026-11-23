package service

import (
	"context"
	"errors"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/constants"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/exceptions"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/log"
	"fernandoglatz/aws-infrastructure-helper/internal/infrastructure/api"
	"fernandoglatz/aws-infrastructure-helper/internal/infrastructure/config"
	"fmt"
	"net"
	"strings"
	"time"

	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

const (
	DEFAULT_FAILURE_THRESHOLD     = 3
	DEFAULT_DNS_FAILURE_THRESHOLD = 30
)

type fetcher interface {
	GetPublicIp(ctx *context.Context) (string, *exceptions.ApiError)
	Fetch(ctx *context.Context) *exceptions.ApiError
}

type HelperService struct {
	fetcher  fetcher
	cloud    cloudGateway
	lookupIP func(host string) ([]net.IP, error)
	now      func() time.Time

	ispFallback                  *bool
	autoScalingGroupShutdownTime *time.Time

	// Consecutive failed ISP checks of any kind, and how many of them were not DNS failures.
	failureStreak           int
	connectionFailureStreak int
}

func NewHelperService() *HelperService {
	return &HelperService{
		fetcher:  api.NewFetcherApi(),
		cloud:    &awsGateway{},
		lookupIP: net.LookupIP,
		now:      time.Now,
	}
}

func failureThreshold() int {
	threshold := config.ApplicationConfig.Application.ISPFallbackUpdater.FailureThreshold
	if threshold <= constants.ZERO {
		return DEFAULT_FAILURE_THRESHOLD
	}
	return threshold
}

func dnsFailureThreshold() int {
	threshold := config.ApplicationConfig.Application.ISPFallbackUpdater.DNSFailureThreshold
	if threshold <= constants.ZERO {
		return DEFAULT_DNS_FAILURE_THRESHOLD
	}
	return threshold
}

// isDNSError reports whether the request failed while resolving the host name.
func isDNSError(apiError *exceptions.ApiError) bool {
	if apiError == nil || apiError.Err == nil {
		return false
	}

	var dnsError *net.DNSError
	return errors.As(apiError.Err, &dnsError)
}

func (service *HelperService) ScheduleDNSUpdater(ctx *context.Context) error {
	checkInterval := config.ApplicationConfig.Application.DNSUpdater.CheckInterval

	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for range ticker.C {
			service.checkDNS(ctx)
		}
	}()

	return nil
}

func (service *HelperService) ScheduleISPFallback(ctx *context.Context) error {
	checkInterval := config.ApplicationConfig.Application.ISPFallbackUpdater.CheckInterval

	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for range ticker.C {
			service.checkISP(ctx)
		}
	}()

	return nil
}

// checkDNS keeps the public record pointing to the current public IP.
func (service *HelperService) checkDNS(ctx *context.Context) {
	dnsUpdater := config.ApplicationConfig.Application.DNSUpdater
	recordName := dnsUpdater.Record.Name

	log.Info(ctx).Msg("Checking DNS...")

	publicIp, erra := service.fetcher.GetPublicIp(ctx)
	if erra != nil {
		log.Error(ctx).Msg(fmt.Sprintf("Error on fetching public IP, skipping DNS update: %s (status %d)", erra.Message, erra.Status))
		return
	}

	publicIp = strings.TrimSpace(publicIp)
	parsedIp := net.ParseIP(publicIp)
	if parsedIp == nil || parsedIp.To4() == nil {
		log.Error(ctx).Msg(fmt.Sprintf("Invalid public IPv4 %q, skipping DNS update", publicIp))
		return
	}

	resolvedIp, errw := service.resolveDNS(recordName)
	if errw != nil {
		log.Warn(ctx).Msg(fmt.Sprintf("Error on resolving %s, updating it anyway: %v", recordName, errw.GetMessage()))
	} else if resolvedIp == publicIp {
		log.Info(ctx).Msg(fmt.Sprintf("DNS %s is up to date with public IP %s", recordName, publicIp))
		return
	} else {
		log.Info(ctx).Msg(fmt.Sprintf("Public IP changed from %s to %s", resolvedIp, publicIp))
	}

	errw = service.upsertRecords(ctx, dnsUpdater.Record.HostedZoneIds, recordName, publicIp, route53types.RRTypeA, dnsUpdater.Record.TTL)
	if errw != nil {
		log.Error(ctx).Msg(fmt.Sprintf("Error on updating DNS: %v", errw.GetMessage()))
	}
}

// resolveDNS returns the first IPv4 address of the domain name.
func (service *HelperService) resolveDNS(domainName string) (string, *exceptions.WrappedError) {
	ips, err := service.lookupIP(domainName)
	if err != nil {
		return constants.EMPTY, &exceptions.WrappedError{
			Error: err,
		}
	}

	for _, ip := range ips {
		if ip.To4() != nil {
			return ip.String(), nil
		}
	}

	return constants.EMPTY, &exceptions.WrappedError{
		Message: fmt.Sprintf("no IPv4 address found for %s", domainName),
	}
}

// checkISP probes the home ISP and toggles the AWS fallback. A single failed probe is
// not enough: the fallback is enabled only after consecutive failures.
func (service *HelperService) checkISP(ctx *context.Context) {
	log.Info(ctx).Msg("Checking ISP ports...")

	erra := service.fetcher.Fetch(ctx)
	// Any HTTP response, even an error status, means the ISP link is up.
	reachable := erra == nil || erra.Status != constants.ZERO

	if reachable {
		service.failureStreak = constants.ZERO
		service.connectionFailureStreak = constants.ZERO

		if service.ispFallback == nil || *service.ispFallback {
			service.setISPFallback(ctx, false)
		} else {
			log.Info(ctx).Msg("ISP ports are open")
		}

	} else {
		service.failureStreak++
		if !isDNSError(erra) {
			service.connectionFailureStreak++
		}

		if service.ispFallback != nil && *service.ispFallback {
			log.Info(ctx).Msg("ISP ports are closed")

		} else if service.connectionFailureStreak >= failureThreshold() || service.failureStreak >= dnsFailureThreshold() {
			service.setISPFallback(ctx, true)

		} else {
			log.Warn(ctx).Msg(fmt.Sprintf("ISP check failed (%d consecutive, %d connection failures, thresholds %d/%d), not enabling fallback yet",
				service.failureStreak, service.connectionFailureStreak, failureThreshold(), dnsFailureThreshold()))
		}
	}

	service.shutdownAutoScalingGroupIfDue(ctx)
}

func (service *HelperService) setISPFallback(ctx *context.Context, fallback bool) {
	action, state := "disabling", "disabled"
	if fallback {
		action, state = "enabling", "enabled"
	}

	log.Info(ctx).Msg(fmt.Sprintf("ISP fallback: %s", action))

	errw := service.changeISPFallback(ctx, fallback)
	if errw != nil {
		log.Error(ctx).Msg(fmt.Sprintf("Error on %s ISP fallback: %v", action, errw.GetMessage()))
		// Unknown state: retried on the next check.
		service.ispFallback = nil
		return
	}

	service.ispFallback = &fallback
	log.Info(ctx).Msg(fmt.Sprintf("ISP fallback %s", state))
}

func (service *HelperService) changeISPFallback(ctx *context.Context, fallback bool) *exceptions.WrappedError {
	ispFallbackUpdater := config.ApplicationConfig.Application.ISPFallbackUpdater
	record := ispFallbackUpdater.Record
	autoScalingGroup := ispFallbackUpdater.EC2.AutoScalingGroup
	cloudfront := ispFallbackUpdater.Cloudfront

	recordValue := record.Value.Normal
	distributionOrigin := cloudfront.Origin.Normal

	if fallback {
		recordValue = record.Value.Fallback
		distributionOrigin = cloudfront.Origin.Fallback

		// A shutdown left over from a previous recovery must not stop the instance we need now.
		service.autoScalingGroupShutdownTime = nil

		errw := service.cloud.UpdateAutoScalingGroup(ctx, autoScalingGroup.Name, constants.ONE)
		if errw != nil {
			return errw
		}

	} else {
		shutdownTime := service.now().Add(autoScalingGroup.ShutdownTime)
		service.autoScalingGroupShutdownTime = &shutdownTime

		log.Info(ctx).Msg(fmt.Sprintf("Shutting down auto scaling group %s at %s", autoScalingGroup.Name, shutdownTime))
	}

	errw := service.cloud.UpdateCloudfrontOrigin(ctx, cloudfront.DistributionId, distributionOrigin)
	if errw != nil {
		return errw
	}

	return service.upsertRecords(ctx, record.HostedZoneIds, record.Name, recordValue, route53types.RRTypeCname, record.TTL)
}

// upsertRecords updates the record in every hosted zone, returning the first error.
func (service *HelperService) upsertRecords(ctx *context.Context, hostedZoneIds []string, name string, value string, rrType route53types.RRType, ttl int64) *exceptions.WrappedError {
	var firstError *exceptions.WrappedError

	for _, hostedZoneId := range hostedZoneIds {
		errw := service.cloud.UpsertRecord(ctx, hostedZoneId, name, value, rrType, ttl)
		if errw != nil {
			log.Error(ctx).Msg(fmt.Sprintf("Error on updating DNS %s for hosted zone %s: %v", name, hostedZoneId, errw.GetMessage()))
			if firstError == nil {
				firstError = errw
			}
		}
	}

	return firstError
}

// shutdownAutoScalingGroupIfDue scales the group down once the shutdown time has passed.
// On failure the shutdown stays pending and is retried on the next check.
func (service *HelperService) shutdownAutoScalingGroupIfDue(ctx *context.Context) {
	if service.autoScalingGroupShutdownTime == nil || !service.now().After(*service.autoScalingGroupShutdownTime) {
		return
	}

	autoScalingGroupName := config.ApplicationConfig.Application.ISPFallbackUpdater.EC2.AutoScalingGroup.Name

	errw := service.cloud.UpdateAutoScalingGroup(ctx, autoScalingGroupName, constants.ZERO)
	if errw != nil {
		log.Error(ctx).Msg(fmt.Sprintf("Error on shutting down Auto Scaling Group, retrying on next check: %v", errw.GetMessage()))
		return
	}

	service.autoScalingGroupShutdownTime = nil
}
