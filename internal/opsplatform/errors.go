// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// translate приводит ошибку клиента Kubernetes к ошибкам контракта. Исходная причина остаётся в цепочке (%w),
// поэтому errors.Is видит и ErrNotFound, и, например, context.DeadlineExceeded. Уже переведённая ошибка
// возвращается как есть. Неизвестные сбои — ErrUnavailable: источник не ответил, это не «пусто» и не «нет прав».
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrForbidden), errors.Is(err, ErrUnavailable),
		errors.Is(err, ErrTooLarge), errors.Is(err, ErrPartial):
		return err
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	case apierrors.IsNotFound(err):
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return fmt.Errorf("%w: %w", ErrForbidden, err)
	case apierrors.IsRequestEntityTooLargeError(err):
		return fmt.Errorf("%w: %w", ErrTooLarge, err)
	default:
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
}
