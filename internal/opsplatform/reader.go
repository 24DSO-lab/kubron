// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package opsplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Clients — клиенты подключения одной сессии к одному кластеру. Их даёт существующий Manager (ForSession, ConfigFor):
// пакет не строит конфигурации и не создаёт клиентов сам (ADR-0018, ADR-0026 §1).
type Clients struct {
	Dynamic   dynamic.Interface
	Core      kubernetes.Interface
	Discovery discovery.DiscoveryInterface
}

// ReaderLimits — ограничения одного вызова Reader. Нулевые поля заменяются значениями по умолчанию.
type ReaderLimits struct {
	Timeout         time.Duration // deadline одного вызова
	PageSize        int64         // размер страницы List
	DefaultItems    int64         // число объектов List при ListOptions.Limit == 0
	MaxItems        int64         // верхняя граница числа объектов List
	MaxListBytes    int           // верхняя граница сериализованного размера результата List
	DefaultTail     int64         // строк лога по умолчанию
	MaxTail         int64         // верхняя граница строк лога
	DefaultLogBytes int64         // байт лога по умолчанию
	MaxLogBytes     int64         // верхняя граница байт лога
}

// DefaultReaderLimits — значения платформы.
func DefaultReaderLimits() ReaderLimits {
	return ReaderLimits{
		Timeout:         20 * time.Second,
		PageSize:        200,
		DefaultItems:    500,
		MaxItems:        1000,
		MaxListBytes:    8 << 20,
		DefaultTail:     200,
		MaxTail:         2000,
		DefaultLogBytes: 256 << 10,
		MaxLogBytes:     1 << 20,
	}
}

func (l ReaderLimits) withDefaults() ReaderLimits {
	d := DefaultReaderLimits()
	pick := func(v, def int64) int64 {
		if v > 0 {
			return v
		}
		return def
	}
	l.Timeout = time.Duration(pick(int64(l.Timeout), int64(d.Timeout)))
	l.PageSize = pick(l.PageSize, d.PageSize)
	l.MaxItems = pick(l.MaxItems, d.MaxItems)
	l.DefaultItems = min(pick(l.DefaultItems, d.DefaultItems), l.MaxItems)
	l.MaxListBytes = int(pick(int64(l.MaxListBytes), int64(d.MaxListBytes)))
	l.MaxTail = pick(l.MaxTail, d.MaxTail)
	l.DefaultTail = min(pick(l.DefaultTail, d.DefaultTail), l.MaxTail)
	l.MaxLogBytes = pick(l.MaxLogBytes, d.MaxLogBytes)
	l.DefaultLogBytes = min(pick(l.DefaultLogBytes, d.DefaultLogBytes), l.MaxLogBytes)
	return l
}

// reader — реализация Reader поверх клиентов подключения.
type reader struct {
	clusterID string
	c         Clients
	lim       *ClusterLimiter
	l         ReaderLimits
	now       func() time.Time
}

// NewReader создаёт Reader кластера clusterID. lim — общий лимитер процесса (nil — SharedLimiter()).
func NewReader(clusterID string, c Clients, lim *ClusterLimiter, l ReaderLimits) Reader {
	if lim == nil {
		lim = SharedLimiter()
	}
	return &reader{clusterID: clusterID, c: c, lim: lim, l: l.withDefaults(), now: time.Now}
}

// begin занимает слот лимитера и ограничивает ctx deadline'ом вызова. done обязателен (defer).
func (r *reader) begin(ctx context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, r.l.Timeout)
	release, err := r.lim.Acquire(ctx, r.clusterID)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return ctx, func() { release(); cancel() }, nil
}

func (r *reader) Get(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	gvr, info, err := resolve(gvk)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errors.New("resource name is required")
	}
	if info.namespaced && namespace == "" {
		return nil, errors.New("namespace is required for a namespaced resource")
	}
	ctx, done, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	var res dynamic.ResourceInterface = r.c.Dynamic.Resource(gvr)
	if info.namespaced {
		res = r.c.Dynamic.Resource(gvr).Namespace(namespace)
	}
	obj, err := res.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, translate(err)
	}
	return obj, nil
}

func (r *reader) List(ctx context.Context, gvk schema.GroupVersionKind, opts ListOptions) (*ListResult, error) {
	gvr, info, err := resolve(gvk)
	if err != nil {
		return nil, err
	}
	if _, err := labels.Parse(opts.LabelSelector); err != nil {
		return nil, fmt.Errorf("invalid label selector: %w", err)
	}
	if _, err := fields.ParseSelector(opts.FieldSelector); err != nil {
		return nil, fmt.Errorf("invalid field selector: %w", err)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = r.l.DefaultItems
	}
	limit = min(limit, r.l.MaxItems)

	ctx, done, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	var res dynamic.ResourceInterface = r.c.Dynamic.Resource(gvr)
	if info.namespaced && opts.Namespace != "" {
		res = r.c.Dynamic.Resource(gvr).Namespace(opts.Namespace)
	}

	out := &ListResult{}
	size := 0
	cont := ""
pages:
	for {
		list, err := res.List(ctx, metav1.ListOptions{
			LabelSelector: opts.LabelSelector,
			FieldSelector: opts.FieldSelector,
			Limit:         min(r.l.PageSize, limit),
			Continue:      cont,
		})
		if err != nil {
			if cont != "" && apierrors.IsResourceExpired(err) {
				out.Truncated = true // токен продолжения истёк: отдаём прочитанное и честно говорим об усечении
				break
			}
			return nil, translate(err)
		}
		for i := range list.Items {
			raw, err := json.Marshal(list.Items[i].Object)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
			}
			if int64(len(out.Items)) >= limit || size+len(raw) > r.l.MaxListBytes {
				if len(out.Items) == 0 {
					return nil, ErrTooLarge // один объект больше лимита ответа
				}
				out.Truncated = true
				break pages
			}
			out.Items = append(out.Items, list.Items[i])
			size += len(raw)
		}
		cont = list.GetContinue()
		if cont == "" {
			break
		}
		if int64(len(out.Items)) >= limit {
			out.Truncated = true
			break
		}
	}
	out.ObservedAt = r.now().UTC()
	return out, nil
}

func (r *reader) PodLogs(ctx context.Context, req LogRequest) ([]byte, error) {
	if req.Namespace == "" || req.Pod == "" {
		return nil, errors.New("namespace and pod are required")
	}
	tail := req.TailLines
	if tail <= 0 {
		tail = r.l.DefaultTail
	}
	tail = min(tail, r.l.MaxTail)
	limit := req.LimitBytes
	if limit <= 0 {
		limit = r.l.DefaultLogBytes
	}
	limit = min(limit, r.l.MaxLogBytes)

	ctx, done, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	// Просим на байт больше лимита: так видно, что лог не поместился (ErrTooLarge), а не молча обрезан сервером.
	probe := limit + 1
	stream, err := r.c.Core.CoreV1().Pods(req.Namespace).GetLogs(req.Pod, &corev1.PodLogOptions{
		Container:  req.Container,
		Previous:   req.Previous,
		TailLines:  &tail,
		LimitBytes: &probe,
	}).Stream(ctx)
	if err != nil {
		if apierrors.IsBadRequest(err) { // контейнер ещё не запускался / предыдущего экземпляра нет: логов нет
			return nil, fmt.Errorf("%w: %w", ErrNotFound, err)
		}
		return nil, translate(err)
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, probe))
	if err != nil {
		return nil, translate(err)
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}

// callCtx выполняет вызов клиента discovery, который не принимает контекст, так, чтобы отмена ctx освобождала
// вызывающего сразу. Результат отменённого вызова отбрасывается; горутина завершается вместе с самим запросом
// (у соединения кластера есть собственные таймауты клиента).
func callCtx[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1) // буфер: горутина не зависнет на отправке после отмены
	go func() {
		v, err := fn()
		ch <- result{v, err}
	}()
	select {
	case res := <-ch:
		return res.v, res.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func (r *reader) ServerVersion(ctx context.Context) (version.Info, error) {
	ctx, done, err := r.begin(ctx)
	if err != nil {
		return version.Info{}, err
	}
	defer done()
	info, err := callCtx(ctx, func() (*version.Info, error) { return r.c.Discovery.ServerVersion() })
	if err != nil {
		return version.Info{}, translate(err)
	}
	return *info, nil
}

func (r *reader) ServerResources(ctx context.Context) ([]*metav1.APIResourceList, error) {
	ctx, done, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	type groupsAndResources struct{ lists []*metav1.APIResourceList }
	res, err := callCtx(ctx, func() (groupsAndResources, error) {
		_, lists, err := r.c.Discovery.ServerGroupsAndResources()
		return groupsAndResources{lists}, err
	})
	if err == nil {
		return res.lists, nil
	}
	var failed *discovery.ErrGroupDiscoveryFailed
	if errors.As(err, &failed) && len(res.lists) > 0 {
		// Часть групп не ответила: отдаём остальное и сообщаем о неполноте отдельной ошибкой, а не пустым успехом.
		names := make([]string, 0, len(failed.Groups))
		for gv := range failed.Groups {
			names = append(names, gv.String())
		}
		sort.Strings(names)
		return res.lists, &PartialError{Failed: names}
	}
	return nil, translate(err)
}
