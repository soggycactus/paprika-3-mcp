package paprika

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogin(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
	}{
		{
			name:     "simple credentials",
			username: "user@example.com",
			password: "password123",
		},
		{
			name:     "password with special characters",
			username: "user@example.com",
			password: "p@ss&word=1+2%3",
		},
		{
			name:     "email with plus addressing",
			username: "user+tag@example.com",
			password: "secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				capturedBody = string(body)

				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(loginResponse{
					Result: struct {
						Token string `json:"token"`
					}{Token: "test-token"},
				})
			}))
			defer server.Close()

			// Override the login URL by using a custom transport that redirects to our test server
			client := http.Client{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					req.URL, _ = url.Parse(server.URL + req.URL.Path)
					return http.DefaultTransport.RoundTrip(req)
				}),
			}

			token, err := login(context.Background(), client, tt.username, tt.password)
			require.NoError(t, err)
			assert.Equal(t, "test-token", token)

			// Parse the captured body and verify credentials were properly encoded
			values, err := url.ParseQuery(capturedBody)
			require.NoError(t, err)
			assert.Equal(t, tt.username, values.Get("email"))
			assert.Equal(t, tt.password, values.Get("password"))
		})
	}
}

func TestLoginError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.URL, _ = url.Parse(server.URL + req.URL.Path)
			return http.DefaultTransport.RoundTrip(req)
		}),
	}

	_, err := login(context.Background(), client, "user@example.com", "wrong")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to login")
}

// roundTripFunc allows using a function as an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
