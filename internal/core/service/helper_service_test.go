package service

import (
	"context"
	"errors"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/exceptions"
	"fernandoglatz/aws-infrastructure-helper/internal/infrastructure/config"
	"net"
	"net/url"
	"testing"
	"time"

	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// --- fakes ---

type fakeFetcher struct {
	fetchResults []*exceptions.ApiError
	fetchCalls   int

	publicIp      string
	publicIpError *exceptions.ApiError
}

func (fetcher *fakeFetcher) Fetch(ctx *context.Context) *exceptions.ApiError {
	index := fetcher.fetchCalls
	fetcher.fetchCalls++

	if index < len(fetcher.fetchResults) {
		return fetcher.fetchResults[index]
	}

	return nil
}

func (fetcher *fakeFetcher) GetPublicIp(ctx *context.Context) (string, *exceptions.ApiError) {
	return fetcher.publicIp, fetcher.publicIpError
}

type recordUpsert struct {
	hostedZoneId string
	name         string
	value        string
	rrType       route53types.RRType
}

type fakeCloud struct {
	asgUpdates        []int32
	cloudfrontOrigins []string
	recordUpserts     []recordUpsert

	asgErrors        []error
	failRecordZoneId string
}

func (cloud *fakeCloud) UpdateAutoScalingGroup(ctx *context.Context, name string, desired int32) *exceptions.WrappedError {
	cloud.asgUpdates = append(cloud.asgUpdates, desired)

	if len(cloud.asgErrors) > 0 {
		err := cloud.asgErrors[0]
		cloud.asgErrors = cloud.asgErrors[1:]
		if err != nil {
			return &exceptions.WrappedError{Error: err}
		}
	}

	return nil
}

func (cloud *fakeCloud) UpdateCloudfrontOrigin(ctx *context.Context, distributionId string, origin string) *exceptions.WrappedError {
	cloud.cloudfrontOrigins = append(cloud.cloudfrontOrigins, origin)
	return nil
}

func (cloud *fakeCloud) UpsertRecord(ctx *context.Context, hostedZoneId string, name string, value string, rrType route53types.RRType, ttl int64) *exceptions.WrappedError {
	cloud.recordUpserts = append(cloud.recordUpserts, recordUpsert{hostedZoneId, name, value, rrType})

	if hostedZoneId == cloud.failRecordZoneId {
		return &exceptions.WrappedError{Error: errors.New("access denied")}
	}

	return nil
}

type fakeClock struct {
	current time.Time
}

func (clock *fakeClock) now() time.Time {
	return clock.current
}

func (clock *fakeClock) advance(duration time.Duration) {
	clock.current = clock.current.Add(duration)
}

// --- helpers ---

var (
	connectionError = &exceptions.ApiError{
		Message: "connection timed out",
		Err:     &url.Error{Op: "Get", URL: "https://example.com", Err: &net.OpError{Op: "dial", Err: errors.New("i/o timeout")}},
	}
	dnsError = &exceptions.ApiError{
		Message: "lookup failed",
		Err:     &url.Error{Op: "Get", URL: "https://example.com", Err: &net.OpError{Op: "dial", Err: &net.DNSError{Err: "server misbehaving", Name: "example.com"}}},
	}
	httpError = &exceptions.ApiError{Status: 500}
)

func setupConfig(t *testing.T) {
	t.Helper()

	previous := config.ApplicationConfig
	t.Cleanup(func() { config.ApplicationConfig = previous })

	config.ApplicationConfig = config.Config{}

	ispFallback := &config.ApplicationConfig.Application.ISPFallbackUpdater
	ispFallback.FailureThreshold = 3
	ispFallback.DNSFailureThreshold = 6
	ispFallback.Record.HostedZoneIds = []string{"ZONE1", "ZONE2"}
	ispFallback.Record.Name = "home.example.com"
	ispFallback.Record.TTL = 60
	ispFallback.Record.Value.Normal = "normal.example.com"
	ispFallback.Record.Value.Fallback = "fallback.example.com"
	ispFallback.Cloudfront.DistributionId = "DIST"
	ispFallback.Cloudfront.Origin.Normal = "origin-normal"
	ispFallback.Cloudfront.Origin.Fallback = "origin-fallback"
	ispFallback.EC2.AutoScalingGroup.Name = "asg"
	ispFallback.EC2.AutoScalingGroup.ShutdownTime = 5 * time.Minute

	dnsUpdater := &config.ApplicationConfig.Application.DNSUpdater
	dnsUpdater.Record.HostedZoneIds = []string{"ZONE1", "ZONE2"}
	dnsUpdater.Record.Name = "public.example.com"
	dnsUpdater.Record.TTL = 60
}

func newTestService(fetcher *fakeFetcher, cloud *fakeCloud, clock *fakeClock) *HelperService {
	return &HelperService{
		fetcher:  fetcher,
		cloud:    cloud,
		now:      clock.now,
		lookupIP: func(host string) ([]net.IP, error) { return nil, errors.New("lookup not configured") },
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func runChecks(service *HelperService, ctx *context.Context, count int) {
	for i := 0; i < count; i++ {
		service.checkISP(ctx)
	}
}

// --- ISP fallback ---

func TestCheckISPDoesNotEnableFallbackBelowFailureThreshold(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{fetchResults: []*exceptions.ApiError{connectionError, connectionError}}
	cloud := &fakeCloud{}
	service := newTestService(fetcher, cloud, &fakeClock{current: time.Now()})
	service.ispFallback = boolPtr(false)

	runChecks(service, &ctx, 2)

	if len(cloud.asgUpdates) != 0 || len(cloud.cloudfrontOrigins) != 0 || len(cloud.recordUpserts) != 0 {
		t.Fatalf("expected no AWS changes, got asg=%v cloudfront=%v records=%v", cloud.asgUpdates, cloud.cloudfrontOrigins, cloud.recordUpserts)
	}
	if *service.ispFallback {
		t.Fatal("expected fallback to stay disabled")
	}
}

func TestCheckISPEnablesFallbackAfterConsecutiveFailures(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{fetchResults: []*exceptions.ApiError{connectionError, connectionError, connectionError}}
	cloud := &fakeCloud{}
	service := newTestService(fetcher, cloud, &fakeClock{current: time.Now()})
	service.ispFallback = boolPtr(false)

	runChecks(service, &ctx, 3)

	if len(cloud.asgUpdates) != 1 || cloud.asgUpdates[0] != 1 {
		t.Fatalf("expected one scale-up to 1, got %v", cloud.asgUpdates)
	}
	if len(cloud.cloudfrontOrigins) != 1 || cloud.cloudfrontOrigins[0] != "origin-fallback" {
		t.Fatalf("expected cloudfront fallback origin, got %v", cloud.cloudfrontOrigins)
	}
	if service.ispFallback == nil || !*service.ispFallback {
		t.Fatal("expected fallback to be enabled")
	}
}

func TestCheckISPSuccessResetsFailureStreak(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{fetchResults: []*exceptions.ApiError{connectionError, connectionError, nil, connectionError, connectionError}}
	cloud := &fakeCloud{}
	service := newTestService(fetcher, cloud, &fakeClock{current: time.Now()})
	service.ispFallback = boolPtr(false)

	runChecks(service, &ctx, 5)

	if len(cloud.asgUpdates) != 0 {
		t.Fatalf("expected no scale-up when failures are not consecutive, got %v", cloud.asgUpdates)
	}
}

func TestCheckISPTreatsHTTPErrorStatusAsReachable(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{fetchResults: []*exceptions.ApiError{httpError, httpError, httpError, httpError}}
	cloud := &fakeCloud{}
	service := newTestService(fetcher, cloud, &fakeClock{current: time.Now()})
	service.ispFallback = boolPtr(false)

	runChecks(service, &ctx, 4)

	if len(cloud.asgUpdates) != 0 {
		t.Fatalf("expected an HTTP response to count as reachable, got asg=%v", cloud.asgUpdates)
	}
}

func TestCheckISPDNSFailuresUseHigherThreshold(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	results := []*exceptions.ApiError{}
	for i := 0; i < 6; i++ {
		results = append(results, dnsError)
	}
	fetcher := &fakeFetcher{fetchResults: results}
	cloud := &fakeCloud{}
	service := newTestService(fetcher, cloud, &fakeClock{current: time.Now()})
	service.ispFallback = boolPtr(false)

	runChecks(service, &ctx, 5)
	if len(cloud.asgUpdates) != 0 {
		t.Fatalf("expected DNS-only failures below dns threshold to be ignored, got %v", cloud.asgUpdates)
	}

	runChecks(service, &ctx, 1)
	if len(cloud.asgUpdates) != 1 || cloud.asgUpdates[0] != 1 {
		t.Fatalf("expected scale-up once dns threshold is reached, got %v", cloud.asgUpdates)
	}
}

func TestCheckISPMixedFailuresCountConnectionErrorsOnly(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{fetchResults: []*exceptions.ApiError{connectionError, dnsError, connectionError, dnsError}}
	cloud := &fakeCloud{}
	service := newTestService(fetcher, cloud, &fakeClock{current: time.Now()})
	service.ispFallback = boolPtr(false)

	runChecks(service, &ctx, 4)
	if len(cloud.asgUpdates) != 0 {
		t.Fatalf("expected 2 connection failures to stay below threshold 3, got %v", cloud.asgUpdates)
	}
}

func TestCheckISPUpdatesEveryHostedZone(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{fetchResults: []*exceptions.ApiError{connectionError, connectionError, connectionError}}
	cloud := &fakeCloud{}
	service := newTestService(fetcher, cloud, &fakeClock{current: time.Now()})
	service.ispFallback = boolPtr(false)

	runChecks(service, &ctx, 3)

	if len(cloud.recordUpserts) != 2 {
		t.Fatalf("expected both hosted zones updated, got %v", cloud.recordUpserts)
	}
	for i, zone := range []string{"ZONE1", "ZONE2"} {
		upsert := cloud.recordUpserts[i]
		if upsert.hostedZoneId != zone || upsert.value != "fallback.example.com" || upsert.rrType != route53types.RRTypeCname {
			t.Fatalf("unexpected upsert %d: %+v", i, upsert)
		}
	}
}

func TestCheckISPContinuesWithRemainingZonesWhenOneFails(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{fetchResults: []*exceptions.ApiError{connectionError, connectionError, connectionError}}
	cloud := &fakeCloud{failRecordZoneId: "ZONE1"}
	service := newTestService(fetcher, cloud, &fakeClock{current: time.Now()})
	service.ispFallback = boolPtr(false)

	runChecks(service, &ctx, 3)

	if len(cloud.recordUpserts) != 2 || cloud.recordUpserts[1].hostedZoneId != "ZONE2" {
		t.Fatalf("expected ZONE2 to be updated after ZONE1 failed, got %v", cloud.recordUpserts)
	}
	if service.ispFallback != nil {
		t.Fatal("expected fallback state to be unknown after a partial failure so it is retried")
	}
}

func TestCheckISPRetriesEnableAfterFailure(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{fetchResults: []*exceptions.ApiError{connectionError, connectionError, connectionError, connectionError}}
	cloud := &fakeCloud{asgErrors: []error{errors.New("throttled")}}
	service := newTestService(fetcher, cloud, &fakeClock{current: time.Now()})
	service.ispFallback = boolPtr(false)

	runChecks(service, &ctx, 3)
	if service.ispFallback != nil {
		t.Fatal("expected unknown fallback state after enable failed")
	}

	runChecks(service, &ctx, 1)
	if len(cloud.asgUpdates) != 2 || service.ispFallback == nil || !*service.ispFallback {
		t.Fatalf("expected enable to be retried and succeed, asg=%v", cloud.asgUpdates)
	}
}

func TestCheckISPRecoverySchedulesShutdownAfterDelay(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{}
	cloud := &fakeCloud{}
	clock := &fakeClock{current: time.Now()}
	service := newTestService(fetcher, cloud, clock)
	service.ispFallback = boolPtr(true)

	service.checkISP(&ctx)
	if len(cloud.cloudfrontOrigins) != 1 || cloud.cloudfrontOrigins[0] != "origin-normal" {
		t.Fatalf("expected cloudfront normal origin, got %v", cloud.cloudfrontOrigins)
	}
	if len(cloud.asgUpdates) != 0 {
		t.Fatalf("expected no immediate scale-down, got %v", cloud.asgUpdates)
	}

	clock.advance(4 * time.Minute)
	service.checkISP(&ctx)
	if len(cloud.asgUpdates) != 0 {
		t.Fatalf("expected no scale-down before shutdown time, got %v", cloud.asgUpdates)
	}

	clock.advance(2 * time.Minute)
	service.checkISP(&ctx)
	if len(cloud.asgUpdates) != 1 || cloud.asgUpdates[0] != 0 {
		t.Fatalf("expected scale-down to 0 after shutdown time, got %v", cloud.asgUpdates)
	}
	if service.autoScalingGroupShutdownTime != nil {
		t.Fatal("expected shutdown time to be cleared")
	}
}

func TestCheckISPReenablingFallbackCancelsPendingShutdown(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{fetchResults: []*exceptions.ApiError{nil, connectionError, connectionError, connectionError, connectionError}}
	cloud := &fakeCloud{}
	clock := &fakeClock{current: time.Now()}
	service := newTestService(fetcher, cloud, clock)
	service.ispFallback = boolPtr(true)

	service.checkISP(&ctx) // recovery: shutdown scheduled
	runChecks(service, &ctx, 3)
	if len(cloud.asgUpdates) != 1 || cloud.asgUpdates[0] != 1 {
		t.Fatalf("expected fallback re-enabled with scale-up, got %v", cloud.asgUpdates)
	}

	clock.advance(10 * time.Minute)
	service.checkISP(&ctx)
	if len(cloud.asgUpdates) != 1 {
		t.Fatalf("expected pending shutdown to be cancelled, got %v", cloud.asgUpdates)
	}
}

func TestCheckISPRetriesShutdownAfterFailure(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{}
	cloud := &fakeCloud{asgErrors: []error{errors.New("dns failure")}}
	clock := &fakeClock{current: time.Now()}
	service := newTestService(fetcher, cloud, clock)
	service.ispFallback = boolPtr(true)

	service.checkISP(&ctx)
	clock.advance(6 * time.Minute)

	service.checkISP(&ctx)
	if service.autoScalingGroupShutdownTime == nil {
		t.Fatal("expected shutdown to stay pending after a failed scale-down")
	}

	service.checkISP(&ctx)
	if len(cloud.asgUpdates) != 2 || service.autoScalingGroupShutdownTime != nil {
		t.Fatalf("expected scale-down to be retried and succeed, got %v", cloud.asgUpdates)
	}
}

func TestCheckISPReconcilesUnknownStateOnFirstSuccess(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	cloud := &fakeCloud{}
	service := newTestService(&fakeFetcher{}, cloud, &fakeClock{current: time.Now()})

	service.checkISP(&ctx)

	if service.ispFallback == nil || *service.ispFallback {
		t.Fatal("expected fallback disabled after first successful check")
	}
	if len(cloud.cloudfrontOrigins) != 1 || cloud.cloudfrontOrigins[0] != "origin-normal" {
		t.Fatalf("expected normal origin reconciled on startup, got %v", cloud.cloudfrontOrigins)
	}
}

// --- DNS updater ---

func TestCheckDNSSkipsUpdateWhenPublicIPFetchFails(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	fetcher := &fakeFetcher{publicIpError: connectionError}
	cloud := &fakeCloud{}
	service := newTestService(fetcher, cloud, &fakeClock{current: time.Now()})

	service.checkDNS(&ctx)

	if len(cloud.recordUpserts) != 0 {
		t.Fatalf("expected no DNS update without a public IP, got %v", cloud.recordUpserts)
	}
}

func TestCheckDNSSkipsUpdateWhenPublicIPInvalid(t *testing.T) {
	for _, publicIp := range []string{"", "not-an-ip", "2001:db8::1"} {
		t.Run(publicIp, func(t *testing.T) {
			setupConfig(t)
			ctx := context.Background()
			cloud := &fakeCloud{}
			service := newTestService(&fakeFetcher{publicIp: publicIp}, cloud, &fakeClock{current: time.Now()})

			service.checkDNS(&ctx)

			if len(cloud.recordUpserts) != 0 {
				t.Fatalf("expected no DNS update for %q, got %v", publicIp, cloud.recordUpserts)
			}
		})
	}
}

func TestCheckDNSNoUpdateWhenRecordMatches(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	cloud := &fakeCloud{}
	service := newTestService(&fakeFetcher{publicIp: "203.0.113.10\n"}, cloud, &fakeClock{current: time.Now()})
	service.lookupIP = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP("203.0.113.10")}, nil
	}

	service.checkDNS(&ctx)

	if len(cloud.recordUpserts) != 0 {
		t.Fatalf("expected no update when an IPv4 record matches, got %v", cloud.recordUpserts)
	}
}

func TestCheckDNSUpdatesAllZonesWhenIPChanged(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	cloud := &fakeCloud{failRecordZoneId: "ZONE1"}
	service := newTestService(&fakeFetcher{publicIp: "203.0.113.10"}, cloud, &fakeClock{current: time.Now()})
	service.lookupIP = func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("198.51.100.1")}, nil
	}

	service.checkDNS(&ctx)

	if len(cloud.recordUpserts) != 2 {
		t.Fatalf("expected both zones updated, got %v", cloud.recordUpserts)
	}
	for _, upsert := range cloud.recordUpserts {
		if upsert.value != "203.0.113.10" || upsert.rrType != route53types.RRTypeA || upsert.name != "public.example.com" {
			t.Fatalf("unexpected upsert %+v", upsert)
		}
	}
}

func TestCheckDNSUpdatesWhenResolutionFails(t *testing.T) {
	setupConfig(t)
	ctx := context.Background()
	cloud := &fakeCloud{}
	service := newTestService(&fakeFetcher{publicIp: "203.0.113.10"}, cloud, &fakeClock{current: time.Now()})

	service.checkDNS(&ctx)

	if len(cloud.recordUpserts) != 2 {
		t.Fatalf("expected update when the record cannot be resolved, got %v", cloud.recordUpserts)
	}
}

// --- error classification ---

func TestIsDNSError(t *testing.T) {
	if !isDNSError(dnsError) {
		t.Fatal("expected wrapped net.DNSError to be detected")
	}
	if isDNSError(connectionError) {
		t.Fatal("expected connection error not to be a DNS error")
	}
	if isDNSError(&exceptions.ApiError{Message: "no cause"}) {
		t.Fatal("expected error without cause not to be a DNS error")
	}
	if isDNSError(nil) {
		t.Fatal("expected nil not to be a DNS error")
	}
}

func TestThresholdDefaults(t *testing.T) {
	setupConfig(t)
	ispFallback := &config.ApplicationConfig.Application.ISPFallbackUpdater
	ispFallback.FailureThreshold = 0
	ispFallback.DNSFailureThreshold = 0

	if got := failureThreshold(); got != DEFAULT_FAILURE_THRESHOLD {
		t.Fatalf("expected default failure threshold %d, got %d", DEFAULT_FAILURE_THRESHOLD, got)
	}
	if got := dnsFailureThreshold(); got != DEFAULT_DNS_FAILURE_THRESHOLD {
		t.Fatalf("expected default dns failure threshold %d, got %d", DEFAULT_DNS_FAILURE_THRESHOLD, got)
	}
}
