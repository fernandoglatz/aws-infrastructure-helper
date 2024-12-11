package service

import (
	"context"
	"fernandoglatz/aws-infrastructure-helper/internal/core/common/utils/exceptions"
)

type IHelper interface {
	ScheduleDNSCheck(ctx context.Context) *exceptions.WrappedError
}
