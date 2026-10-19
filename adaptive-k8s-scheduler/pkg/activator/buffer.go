package activator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// BufferedRequest captures an in-flight HTTP request for replaying after workload restoration.
type BufferedRequest struct {
	Method        string
	URL           string
	Header        http.Header
	Body          []byte
	ContentLength int64
	Host          string
}

// BufferHTTPRequest reads and stores an HTTP request up to maxBytes.
func BufferHTTPRequest(r *http.Request, maxBytes int64) (*BufferedRequest, error) {
	var bodyBytes []byte
	if r.Body != nil {
		limitedReader := io.LimitReader(r.Body, maxBytes)
		var err error
		bodyBytes, err = io.ReadAll(limitedReader)
		if err != nil {
			return nil, fmt.Errorf("failed buffering request body: %w", err)
		}
		_ = r.Body.Close()
	}

	headerCopy := make(http.Header)
	for k, vv := range r.Header {
		vvCopy := make([]string, len(vv))
		copy(vvCopy, vv)
		headerCopy[k] = vvCopy
	}

	return &BufferedRequest{
		Method:        r.Method,
		URL:           r.URL.RequestURI(),
		Header:        headerCopy,
		Body:          bodyBytes,
		ContentLength: int64(len(bodyBytes)),
		Host:          r.Host,
	}, nil
}

// ToHTTPRequest reconstitutes a standard *http.Request targeting the provided base URL.
func (b *BufferedRequest) ToHTTPRequest(ctx context.Context, targetBaseURL string) (*http.Request, error) {
	targetURL := targetBaseURL + b.URL
	var bodyReader io.Reader
	if len(b.Body) > 0 {
		bodyReader = bytes.NewReader(b.Body)
	}

	req, err := http.NewRequestWithContext(ctx, b.Method, targetURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed creating target request: %w", err)
	}

	req.Header = make(http.Header)
	for k, vv := range b.Header {
		vvCopy := make([]string, len(vv))
		copy(vvCopy, vv)
		req.Header[k] = vvCopy
	}

	req.Host = b.Host
	req.ContentLength = b.ContentLength

	return req, nil
}

// SingleFlightGroup ensures that multiple concurrent requests for the same workload
// only trigger one restoration process while other requests await its completion.
type SingleFlightGroup struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

type flightCall struct {
	wg  sync.WaitGroup
	err error
}

func NewSingleFlightGroup() *SingleFlightGroup {
	return &SingleFlightGroup{
		calls: make(map[string]*flightCall),
	}
}

// Do executes fn once for a given key. Concurrent callers with the same key block
// until fn completes, and all callers receive the return error.
func (g *SingleFlightGroup) Do(key string, fn func() error) error {
	g.mu.Lock()
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.err
	}

	c := &flightCall{}
	c.wg.Add(1)
	g.calls[key] = c
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		c.wg.Done()
	}()

	c.err = fn()
	return c.err
}

// DoWithTimeout executes Do with a bounded context timeout.
func (g *SingleFlightGroup) DoWithTimeout(ctx context.Context, key string, timeout time.Duration, fn func() error) error {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- g.Do(key, fn)
	}()

	select {
	case <-ctxWithTimeout.Done():
		return fmt.Errorf("single-flight restoration timed out for %s: %w", key, ctxWithTimeout.Err())
	case err := <-errChan:
		return err
	}
}
