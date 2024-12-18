package exceptions

import (
	"context"
	"encoding/json"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/constants"
	"net/http"
)

var (
	GenericError = BaseError{
		Code:    "GENERIC_ERROR",
		Message: "Try again later.",
	}
)

type WrappedError struct {
	Error     error
	Message   string
	Code      string
	BaseError BaseError
}

type BaseError struct {
	Code       string
	Message    string
	HttpStatus int
}

type ApiError struct {
	Message      string
	ResponseBody string
	Status       int
}

func (apiError ApiError) ToWrappedError(ctx *context.Context) *WrappedError {
	baseError := BaseError{
		Code:       constants.API_ERROR,
		Message:    "Error calling API.",
		HttpStatus: http.StatusInternalServerError,
	}

	err := json.Unmarshal([]byte(apiError.ResponseBody), &baseError)
	if err == nil {
		baseError.HttpStatus = apiError.Status
	}

	wrappedError := WrappedError{
		BaseError: baseError,
	}

	return &wrappedError
}

func (wrappedError WrappedError) GetMessage() string {
	if wrappedError.Error != nil {
		return wrappedError.Error.Error()
	}

	if wrappedError.Message != "" {
		return wrappedError.Message
	}

	if wrappedError.BaseError.Message != "" {
		return wrappedError.BaseError.Message
	}

	return ""
}

func (wrappedError WrappedError) GetCode() string {
	if wrappedError.Code != "" {
		return wrappedError.Code
	}

	if wrappedError.BaseError.Code != "" {
		return wrappedError.BaseError.Code
	}

	return ""
}
