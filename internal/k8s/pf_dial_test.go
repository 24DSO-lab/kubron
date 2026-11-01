// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/httpstream" //nolint:staticcheck // client-go spdy.NewDialer still returns this type
)

type fakeStream struct {
	headers http.Header
	r       *io.PipeReader
	w       *io.PipeWriter // сюда тест «присылает» данные от пода
	mu      sync.Mutex
	written bytes.Buffer
	closed  bool
	reset   bool
}

func newFakeStream(h http.Header) *fakeStream {
	r, w := io.Pipe()
	return &fakeStream{headers: h, r: r, w: w}
}

func (s *fakeStream) Read(p []byte) (int, error) { return s.r.Read(p) }
func (s *fakeStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written.Write(p)
}
func (s *fakeStream) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}
func (s *fakeStream) Reset() error {
	s.mu.Lock()
	s.reset = true
	s.mu.Unlock()
	_ = s.r.CloseWithError(io.EOF) // разбудить читателя
	return nil
}
func (s *fakeStream) Headers() http.Header { return s.headers }
func (s *fakeStream) Identifier() uint32   { return 1 }

type fakeSPDY struct {
	mu        sync.Mutex
	streams   []*fakeStream
	removed   []httpstream.Stream
	closeCh   chan bool
	closed    bool
	createErr error
}

func newFakeSPDY() *fakeSPDY { return &fakeSPDY{closeCh: make(chan bool)} }

func (c *fakeSPDY) CreateStream(h http.Header) (httpstream.Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.createErr != nil {
		return nil, c.createErr
	}
	s := newFakeStream(h.Clone())
	c.streams = append(c.streams, s)
	return s, nil
}
func (c *fakeSPDY) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.closeCh)
	}
	return nil
}
func (c *fakeSPDY) CloseChan() <-chan bool { return c.closeCh }
func (c *fakeSPDY) RemoveStreams(s ...httpstream.Stream) {
	c.mu.Lock()
	c.removed = append(c.removed, s...)
	c.mu.Unlock()
}

func (c *fakeSPDY) stream(i int) *fakeStream {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.streams[i]
}

func dialerWith(conns ...*fakeSPDY) (*PodDialer, *int) {
	calls := 0
	d := newPodDialer("ns", "web-0", 8080, func(context.Context) (spdyConn, error) {
		if calls >= len(conns) {
			return nil, errors.New("no more connections")
		}
		c := conns[calls]
		calls++
		return c, nil
	})
	return d, &calls
}

func TestPodDialerCreatesStreamPairsAndReusesTheConnection(t *testing.T) {
	c := newFakeSPDY()
	d, calls := dialerWith(c)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		conn, err := d.DialContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
	}
	if *calls != 1 {
		t.Fatalf("the SPDY connection must be reused, connect called %d times", *calls)
	}
	want := []struct{ kind, id string }{{"error", "0"}, {"data", "0"}, {"error", "1"}, {"data", "1"}}
	if len(c.streams) != len(want) {
		t.Fatalf("streams: %d", len(c.streams))
	}
	for i, w := range want {
		h := c.stream(i).Headers()
		if h.Get("streamType") != w.kind || h.Get("requestID") != w.id || h.Get("port") != "8080" {
			t.Errorf("stream %d headers: %v, want %v", i, h, w)
		}
	}
	if !c.stream(0).closed {
		t.Error("the error stream must be half-closed: nothing is written to it")
	}
}

func TestPodConnTransfersDataBothWays(t *testing.T) {
	c := newFakeSPDY()
	d, _ := dialerWith(c)
	conn, err := d.DialContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	go func() { _, _ = c.stream(1).w.Write([]byte("hello")) }()
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("read: %q %v", buf[:n], err)
	}
	if _, err := conn.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	if got := c.stream(1).written.String(); got != "request" {
		t.Fatalf("written: %q", got)
	}
	if conn.RemoteAddr().String() != "ns/web-0:8080" || conn.RemoteAddr().Network() != "pod" {
		t.Errorf("addr: %v", conn.RemoteAddr())
	}
}

func TestPodConnSurfacesTheKubeletError(t *testing.T) {
	c := newFakeSPDY()
	d, _ := dialerWith(c)
	conn, err := d.DialContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	go func() {
		_, _ = c.stream(0).w.Write([]byte("connection refused"))
		_ = c.stream(0).w.Close()
	}()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.Read(make([]byte, 8))
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("the kubelet message must reach the reader, got %v", err)
	}
}

func TestPodConnCloseReleasesStreamsOnce(t *testing.T) {
	c := newFakeSPDY()
	d, _ := dialerWith(c)
	conn, _ := d.DialContext(context.Background())
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if !c.stream(1).reset || len(c.removed) != 2 || !c.stream(0).reset {
		t.Fatalf("data reset=%v removed=%d error reset=%v", c.stream(1).reset, len(c.removed), c.stream(0).reset)
	}
}

func TestPodDialerReconnectsAfterTheConnectionDrops(t *testing.T) {
	first, second := newFakeSPDY(), newFakeSPDY()
	d, calls := dialerWith(first, second)
	if conn, err := d.DialContext(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		conn.Close()
	}
	_ = first.Close() // apiserver оборвал соединение
	conn, err := d.DialContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if *calls != 2 || len(second.streams) != 2 {
		t.Fatalf("calls=%d streams on the new connection=%d", *calls, len(second.streams))
	}
}

func TestPodDialerErrors(t *testing.T) {
	d, _ := dialerWith() // connect всегда отказывает
	if _, err := d.DialContext(context.Background()); err == nil || !strings.Contains(err.Error(), "ns/web-0:8080") {
		t.Fatalf("connect error: %v", err)
	}

	c, next := newFakeSPDY(), newFakeSPDY()
	d, calls := dialerWith(c, next)
	c.createErr = errors.New("boom")
	if _, err := d.DialContext(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("stream error: %v", err)
	}
	if !c.closed {
		t.Error("a connection that cannot create streams must be closed")
	}
	conn, err := d.DialContext(context.Background())
	if err != nil {
		t.Fatalf("the next dial must open a fresh connection: %v", err)
	}
	defer conn.Close()
	if *calls != 2 {
		t.Fatalf("calls=%d", *calls)
	}
}

func TestPodConnExpiredDeadlineClosesTheConnection(t *testing.T) {
	c := newFakeSPDY()
	d, _ := dialerWith(c)
	conn, _ := d.DialContext(context.Background())
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	done := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read must fail after the deadline")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the deadline did not unblock the read")
	}
}
