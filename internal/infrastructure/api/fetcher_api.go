package api

import (
	"context"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/exceptions"
	"fernandoglatz/aws-infrastructure-helper/internal/infrastructure/config"
	"net/http"
)

type FetcherApi struct {
}

func NewFetcherApi() *FetcherApi {
	return &FetcherApi{}
}

func (api *FetcherApi) GetPublicIp(ctx *context.Context) (string, *exceptions.ApiError) {
	method := http.MethodGet
	fetcherConfig := config.ApplicationConfig.Application.DNSUpdater.PublicIPFetcher
	requestUrl := fetcherConfig.Url
	timeout := fetcherConfig.Timeout
	responseStr := ""

	headers := make(map[string]string)
	headers["Accept"] = "plain/text"

	erra := executeRequest(ctx, method, requestUrl, timeout, &headers, nil, &responseStr)
	return responseStr, erra
}

func (api *FetcherApi) Fetch(ctx *context.Context) *exceptions.ApiError {
	method := http.MethodGet
	fetcherConfig := config.ApplicationConfig.Application.ISPFallbackUpdater.PortFetcher
	requestUrl := fetcherConfig.Url
	host := fetcherConfig.Host
	timeout := fetcherConfig.Timeout

	headers := make(map[string]string)
	headers["Host"] = host

	return executeRequest(ctx, method, requestUrl, timeout, &headers, nil, nil)
}
