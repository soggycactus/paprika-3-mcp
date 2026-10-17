package paprika

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type flakyTransport struct {
	failures int
	calls    int
}

func (f *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, errors.New("wsarecv: An existing connection was forcibly closed by the remote host")
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

func TestRetryTransport_RetriesNetworkErrors(t *testing.T) {
	flaky := &flakyTransport{failures: 2}
	rt := &retryTransport{maxRetries: 3, baseDelay: time.Millisecond, transport: flaky}

	req, err := http.NewRequest(http.MethodPost, "https://example.test", strings.NewReader("body"))
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 3, flaky.calls)
}

func TestRetryTransport_GivesUpAfterMaxRetries(t *testing.T) {
	flaky := &flakyTransport{failures: 10}
	rt := &retryTransport{maxRetries: 2, baseDelay: time.Millisecond, transport: flaky}

	req, err := http.NewRequest(http.MethodGet, "https://example.test", nil)
	require.NoError(t, err)
	_, err = rt.RoundTrip(req)
	assert.Error(t, err)
	assert.Equal(t, 3, flaky.calls)
}
