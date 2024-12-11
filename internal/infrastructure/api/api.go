package api

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/constants"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/exceptions"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/log"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func logRequest(ctx context.Context, request http.Request, requestBody []byte) {
	if log.IsLevelEnabled(log.DEBUG) {
		requestBodyLength := len(requestBody)

		log.Debug(ctx).Msg(fmt.Sprintf("---> %s %s %s", request.Method, request.URL, request.Proto))
		for headerName, headerValues := range request.Header {
			headerValue := headerValues[constants.ZERO]
			log.Debug(ctx).Msg(fmt.Sprintf("%s: %s", headerName, headerValue))
		}

		if requestBodyLength > 0 {
			log.Debug(ctx).Msg(string(requestBody))
		}

		log.Debug(ctx).Msg(fmt.Sprintf("---> END HTTP (%d-byte body)", requestBodyLength))
	}
}

func logResponse(ctx context.Context, start time.Time, end time.Time, response *http.Response, responseBody []byte) {
	if log.IsLevelEnabled(log.DEBUG) {
		diff := end.Sub(start)

		log.Debug(ctx).Msg(fmt.Sprintf("<--- %s %s (%s)", response.Proto, response.Status, diff))
		for headerName, headerValues := range response.Header {
			headerValue := headerValues[constants.ZERO]
			log.Debug(ctx).Msg(fmt.Sprintf("%s: %s", headerName, headerValue))
		}

		log.Debug(ctx).Msg(string(responseBody))
		log.Debug(ctx).Msg(fmt.Sprintf("<--- END HTTP (%d-byte body)", len(responseBody)))
	}
}

func executeRequest(ctx context.Context, method string, requestUrl string, timeout time.Duration, headers map[string]string, requestDTO any, responseDTO any) *exceptions.ApiError {
	client := &http.Client{
		Timeout: timeout,
	}

	var requestBody []byte
	var reader io.Reader
	var err error

	if requestDTO != nil {
		contentType := headers["Content-Type"]

		if strings.Contains(contentType, "/xml") {
			requestBody, err = xml.Marshal(requestDTO)

		} else if strings.Contains(contentType, "/x-www-form-urlencoded") {
			requestBody = []byte(requestDTO.(string))

		} else {
			requestBody, err = json.Marshal(requestDTO)
		}

		if err != nil {
			message := fmt.Sprintf("Error during marshal body request: %s", err.Error())
			log.Error(ctx).Msg(message)

			return &exceptions.ApiError{
				Message: message,
			}
		}

		reader = bytes.NewReader(requestBody)
	}

	request, err := http.NewRequest(method, requestUrl, reader)
	if err != nil {
		message := fmt.Sprintf("Error on creating request: %s", err.Error())
		log.Error(ctx).Msg(message)

		return &exceptions.ApiError{
			Message: message,
		}
	}

	if requestDTO != nil && utils.IsEmptyStr(headers["Content-Type"]) {
		request.Header.Set("Content-Type", "application/json")
	}

	if utils.IsEmptyStr(headers["Accept"]) {
		request.Header.Set("Accept", "application/json")
	}

	for key, value := range headers {
		request.Header.Set(key, value)
	}

	logRequest(ctx, *request, requestBody)

	start := time.Now()
	response, err := client.Do(request)
	if err != nil {
		message := fmt.Sprintf("Error on sending request: %s", err.Error())
		log.Error(ctx).Msg(message)

		return &exceptions.ApiError{
			Message: message,
		}
	}

	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		message := fmt.Sprintf("Error on reading response body: %s", err.Error())
		log.Error(ctx).Msg(message)

		return &exceptions.ApiError{
			Message: message,
			Status:  response.StatusCode,
		}
	}

	end := time.Now()
	logResponse(ctx, start, end, response, responseBody)

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		apiError := &exceptions.ApiError{
			ResponseBody: string(responseBody),
			Status:       response.StatusCode,
		}

		return apiError
	}

	if responseDTO != nil {
		responseContentType := response.Header.Get("Content-Type")

		if strings.Contains(responseContentType, "/xml") {
			err = xml.Unmarshal(responseBody, &responseDTO)
		} else if strings.Contains(responseContentType, "text/") {
			if str, ok := responseDTO.(*string); ok {
				*str = string(responseBody)
			} else {
				err = errors.New("responseDTO is not a pointer to a string")
			}
		} else {
			err = json.Unmarshal(responseBody, &responseDTO)
		}

		if err != nil {
			message := fmt.Sprintf("Error on unmarshalling response body: %s", err.Error())
			log.Error(ctx).Msg(message)
			return &exceptions.ApiError{
				ResponseBody: string(responseBody),
				Message:      message,
				Status:       response.StatusCode,
			}
		}
	}

	return nil
}
