// Package fetch downloads https resources. It is the only code of the tool that uses the network.
package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Timeout is the time limit of one fetch.
const Timeout = 30 * time.Second

// MaxBytes is the largest body that Get accepts.
const MaxBytes = 32 << 20

// maxRedirects is the limit of net/http for redirects.
const maxRedirects = 10

// NewClient gives an HTTP client that follows only https redirects, at most 10.
func NewClient() *http.Client {
	return &http.Client{
		Timeout: Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return errors.New("redirect to a URL that is not https")
			}

			if len(via) >= maxRedirects {
				return errors.New("stopped after 10 redirects")
			}

			return nil
		},
	}
}

// Get downloads an https URL. It gives an error for other schemes, a status other than 200,
// or a body larger than MaxBytes. The error texts do not contain the URL.
// When ctx ends, the error wraps the error of ctx.
func Get(ctx context.Context, client *http.Client, uri string) ([]byte, error) {
	var body bytes.Buffer

	if err := Stream(ctx, client, uri, &body, Limits{MaxBytes: MaxBytes, Timeout: Timeout}); err != nil {
		return nil, err
	}

	return body.Bytes(), nil
}

// Limits are the size and time limits of one download.
type Limits struct {
	// MaxBytes is the largest body that is accepted.
	MaxBytes int64
	// Timeout is the time limit of the whole download.
	Timeout time.Duration
}

// Stream downloads an https URL into dst, for bodies that are too large to keep in memory. It gives the same
// errors as Get, with the limits of the call. limits.Timeout limits the whole download; zero adds no limit, so
// only ctx and the client limit the time.
func Stream(ctx context.Context, client *http.Client, uri string, dst io.Writer, limits Limits) error {
	if !strings.HasPrefix(uri, "https://") {
		return errors.New("only https URLs are fetched")
	}

	if client == nil {
		client = NewClient()
	}

	fetchCtx, cancel := ctx, context.CancelFunc(func() {})
	if limits.Timeout > 0 {
		fetchCtx, cancel = context.WithTimeout(ctx, limits.Timeout)
	}

	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, uri, http.NoBody)
	if err != nil {
		return errors.New("request is not valid")
	}

	resp, err := client.Do(req)
	if err != nil {
		return requestError(ctx, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	n, err := io.Copy(dst, io.LimitReader(resp.Body, limits.MaxBytes+1))
	if err != nil {
		return requestError(ctx, err)
	}

	if n > limits.MaxBytes {
		return fmt.Errorf("the body is larger than %d MiB", limits.MaxBytes>>20) //nolint:mnd // MiB shift.
	}

	return nil
}

// requestError gives the error of a failed request without the URL. When ctx ended, it wraps the error of ctx.
func requestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("fetch: %w", ctx.Err())
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}

	return err
}
