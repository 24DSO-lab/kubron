// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/httpstream" //nolint:staticcheck // client-go spdy.NewDialer still returns this type
	"k8s.io/client-go/transport/spdy"
)

// portForwardProtocolV1 — подпротокол port-forward kubelet (тот же, что использует kubectl).
const portForwardProtocolV1 = "portforward.k8s.io"

// Заголовки потоков протокола port-forward.
const (
	pfHeaderStreamType = "streamType"
	pfHeaderPort       = "port"
	pfHeaderRequestID  = "requestID"
	pfStreamData       = "data"
	pfStreamError      = "error"
)

// spdyConn — то, что PodDialer требует от соединения с apiserver (httpstream.Connection его реализует).
type spdyConn interface {
	CreateStream(headers http.Header) (httpstream.Stream, error)
	Close() error
	CloseChan() <-chan bool
	RemoveStreams(streams ...httpstream.Stream)
}

// PodDialer открывает соединения с портом пода через port-forward API без прослушивания порта на хосте:
// каждое соединение — пара потоков (data + error) внутри одного SPDY-соединения с kube-apiserver. Соединение
// переиспользуется и открывается заново, если оборвалось. Потокобезопасен.
type PodDialer struct {
	port    int
	target  string
	connect func(ctx context.Context) (spdyConn, error)

	mu    sync.Mutex
	conn  spdyConn
	reqID int
}

// NewPodDialer создаёт dialer к порту пода активного подключения.
func NewPodDialer(conn *ClusterConnection, namespace, pod string, port int) (*PodDialer, error) {
	reqURL := conn.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(pod).
		SubResource("portforward").
		URL()
	transport, upgrader, err := spdy.RoundTripperFor(conn.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, &url.URL{
		Scheme: reqURL.Scheme,
		Host:   reqURL.Host,
		Path:   reqURL.Path,
	})
	connect := func(ctx context.Context) (spdyConn, error) {
		type result struct {
			c   httpstream.Connection
			err error
		}
		ch := make(chan result, 1)
		go func() {
			c, _, err := dialer.Dial(portForwardProtocolV1)
			ch <- result{c, err}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				return nil, r.err
			}
			return r.c, nil
		case <-ctx.Done():
			go func() { // соединение, установленное после отмены, не должно остаться открытым
				if r := <-ch; r.err == nil && r.c != nil {
					_ = r.c.Close()
				}
			}()
			return nil, ctx.Err()
		}
	}
	return newPodDialer(namespace, pod, port, connect), nil
}

func newPodDialer(namespace, pod string, port int, connect func(context.Context) (spdyConn, error)) *PodDialer {
	return &PodDialer{port: port, target: fmt.Sprintf("%s/%s:%d", namespace, pod, port), connect: connect}
}

// connection возвращает живое SPDY-соединение, открывая новое при необходимости.
func (d *PodDialer) connection(ctx context.Context) (spdyConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		select {
		case <-d.conn.CloseChan():
			d.conn = nil
		default:
			return d.conn, nil
		}
	}
	c, err := d.connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to pod %s: %w", d.target, err)
	}
	d.conn = c
	return c, nil
}

// invalidate забывает неисправное соединение (если оно ещё текущее) и закрывает его.
func (d *PodDialer) invalidate(c spdyConn) {
	d.mu.Lock()
	if d.conn == c {
		d.conn = nil
	}
	d.mu.Unlock()
	_ = c.Close()
}

// Close закрывает SPDY-соединение с apiserver вместе с открытыми через него соединениями.
func (d *PodDialer) Close() {
	d.mu.Lock()
	c := d.conn
	d.conn = nil
	d.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (d *PodDialer) nextRequestID() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := d.reqID
	d.reqID++
	return id
}

func streamHeaders(kind string, port, requestID int) http.Header {
	h := http.Header{}
	h.Set(pfHeaderStreamType, kind)
	h.Set(pfHeaderPort, strconv.Itoa(port))
	h.Set(pfHeaderRequestID, strconv.Itoa(requestID))
	return h
}

// DialContext открывает новое соединение с портом пода. Подходит как pfproxy.DialFunc после привязки к ctx.
func (d *PodDialer) DialContext(ctx context.Context) (net.Conn, error) {
	conn, err := d.connection(ctx)
	if err != nil {
		return nil, err
	}
	id := d.nextRequestID()
	errStream, err := conn.CreateStream(streamHeaders(pfStreamError, d.port, id))
	if err != nil {
		d.invalidate(conn)
		return nil, fmt.Errorf("error creating error stream for pod %s: %w", d.target, err)
	}
	_ = errStream.Close() // в error-поток мы не пишем, только читаем
	dataStream, err := conn.CreateStream(streamHeaders(pfStreamData, d.port, id))
	if err != nil {
		conn.RemoveStreams(errStream)
		d.invalidate(conn)
		return nil, fmt.Errorf("error creating data stream for pod %s: %w", d.target, err)
	}
	pc := &podConn{data: dataStream, errs: errStream, spdy: conn, addr: podAddr(d.target)}
	go pc.watchErrors()
	return pc, nil
}

type podAddr string

func (podAddr) Network() string  { return "pod" }
func (a podAddr) String() string { return string(a) }

// podConn — net.Conn поверх пары потоков port-forward.
type podConn struct {
	data, errs httpstream.Stream
	spdy       spdyConn
	addr       podAddr

	closeOnce sync.Once
	mu        sync.Mutex
	remoteErr error
	timers    [2]*time.Timer // чтение, запись
}

// watchErrors читает error-поток: kubelet пишет туда причину, если порт пода недоступен.
func (c *podConn) watchErrors() {
	msg, _ := io.ReadAll(c.errs)
	if len(msg) == 0 {
		return
	}
	c.mu.Lock()
	c.remoteErr = fmt.Errorf("error forwarding to pod %s: %s", string(c.addr), string(msg))
	c.mu.Unlock()
	_ = c.Close()
}

func (c *podConn) wrap(err error) error {
	if err == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remoteErr != nil {
		return c.remoteErr
	}
	return err
}

func (c *podConn) Read(p []byte) (int, error) {
	n, err := c.data.Read(p)
	return n, c.wrap(err)
}

func (c *podConn) Write(p []byte) (int, error) {
	n, err := c.data.Write(p)
	return n, c.wrap(err)
}

func (c *podConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		for _, t := range c.timers {
			if t != nil {
				t.Stop()
			}
		}
		c.mu.Unlock()
		// Reset, а не Close: Close у SPDY-потока только закрывает запись, а net.Conn.Close обязан
		// прервать и блокирующее чтение.
		err = c.data.Reset()
		c.spdy.RemoveStreams(c.data, c.errs)
		_ = c.errs.Reset() // разбудить watchErrors
	})
	return err
}

func (c *podConn) LocalAddr() net.Addr  { return c.addr }
func (c *podConn) RemoteAddr() net.Addr { return c.addr }

// SetDeadline и родственные: потоки SPDY не поддерживают дедлайнов, поэтому просроченный дедлайн закрывает
// соединение. Этого достаточно для http.Transport и ReverseProxy, которые используют дедлайн, чтобы
// разблокировать чтение или запись.
func (c *podConn) SetDeadline(t time.Time) error {
	c.setTimer(0, t)
	c.setTimer(1, t)
	return nil
}
func (c *podConn) SetReadDeadline(t time.Time) error  { c.setTimer(0, t); return nil }
func (c *podConn) SetWriteDeadline(t time.Time) error { c.setTimer(1, t); return nil }

func (c *podConn) setTimer(i int, t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timers[i] != nil {
		c.timers[i].Stop()
		c.timers[i] = nil
	}
	if t.IsZero() {
		return
	}
	c.timers[i] = time.AfterFunc(time.Until(t), func() { _ = c.Close() })
}
