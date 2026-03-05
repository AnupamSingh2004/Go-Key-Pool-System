package httpclient

import (
"bytes"
"context"
"fmt"
"io"
"net/http"
"time"

"key-pool-system/internal/worker"
)

// Client wraps Go's standard http.Client and implements worker.HTTPClient.
type Client struct {
httpClient *http.Client
}

// NewClient creates an HTTP client configured with the given timeouts.
func NewClient(timeout time.Duration, maxIdleConns int, idleConnTimeout time.Duration) *Client {
transport := &http.Transport{
MaxIdleConns:        maxIdleConns,
MaxIdleConnsPerHost: maxIdleConns,
IdleConnTimeout:     idleConnTimeout,
}

return &Client{
httpClient: &http.Client{
Timeout:   timeout,
Transport: transport,
},
}
}

// Do executes an HTTP request to the downstream API.
// It sets the API key in the Authorization header as a Bearer token.
func (c *Client) Do(ctx context.Context, req *worker.HTTPRequest) (*worker.HTTPResponse, error) {
var bodyReader io.Reader
if len(req.Body) > 0 {
bodyReader = bytes.NewReader(req.Body)
}

httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bodyReader)
if err != nil {
return nil, fmt.Errorf("failed to create request: %w", err)
}

// Set API key as Bearer token
httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)

// Apply custom headers from the request
for key, value := range req.Headers {
httpReq.Header.Set(key, value)
}

// Default content type if body is present and not already set
if len(req.Body) > 0 && httpReq.Header.Get("Content-Type") == "" {
httpReq.Header.Set("Content-Type", "application/json")
}

resp, err := c.httpClient.Do(httpReq)
if err != nil {
return nil, fmt.Errorf("request failed: %w", err)
}
defer resp.Body.Close()

body, err := io.ReadAll(resp.Body)
if err != nil {
return nil, fmt.Errorf("failed to read response body: %w", err)
}

return &worker.HTTPResponse{
StatusCode: resp.StatusCode,
Body:       body,
}, nil
}
