package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"gofr.dev/pkg/gofr"
	"gofr.dev/pkg/gofr/container"
)

func TestMain(m *testing.M) {
	// Save original env vars
	origUser := os.Getenv("BASIC_AUTH_USER")
	origPass := os.Getenv("BASIC_AUTH_PASSWORD")

	// Run tests
	code := m.Run()

	// Restore original env vars
	os.Setenv("BASIC_AUTH_USER", origUser)
	os.Setenv("BASIC_AUTH_PASSWORD", origPass)

	os.Exit(code)
}

func TestEnvironmentVariables(t *testing.T) {
	tests := []struct {
		name     string
		envSetup func()
		wantErr  bool
		errMsg   string
	}{
		{
			name: "Missing both env variables",
			envSetup: func() {
				os.Unsetenv("BASIC_AUTH_USER")
				os.Unsetenv("BASIC_AUTH_PASSWORD")
			},
			wantErr: true,
			errMsg:  "Error: BASIC_AUTH_USER and BASIC_AUTH_PASSWORD environment variables must be set.",
		},
		{
			name: "Missing BASIC_AUTH_USER",
			envSetup: func() {
				os.Unsetenv("BASIC_AUTH_USER")
				os.Setenv("BASIC_AUTH_PASSWORD", "test-pass")
			},
			wantErr: true,
			errMsg:  "Error: BASIC_AUTH_USER and BASIC_AUTH_PASSWORD environment variables must be set.",
		},
		{
			name: "Missing BASIC_AUTH_PASSWORD",
			envSetup: func() {
				os.Setenv("BASIC_AUTH_USER", "test-user")
				os.Unsetenv("BASIC_AUTH_PASSWORD")
			},
			wantErr: true,
			errMsg:  "Error: BASIC_AUTH_USER and BASIC_AUTH_PASSWORD environment variables must be set.",
		},
		{
			name: "Both env variables set correctly",
			envSetup: func() {
				os.Setenv("BASIC_AUTH_USER", "test-user")
				os.Setenv("BASIC_AUTH_PASSWORD", "test-pass")
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment for this test
			tt.envSetup()

			// Reset global variables to read new env values
			basicAuthUser = os.Getenv("BASIC_AUTH_USER")
			basicAuthPassword = os.Getenv("BASIC_AUTH_PASSWORD")

			err := checkEnvironmentVariables()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, basicAuthUser)
				assert.NotEmpty(t, basicAuthPassword)
			}
		})
	}
}

func TestWebhookHandler(t *testing.T) {
	tests := []struct {
		name        string
		requestBody string
		wantErr     bool
	}{
		{
			name:        "Valid empty events array",
			requestBody: `[]`,
			wantErr:     false,
		},
		{
			name:        "Invalid JSON",
			requestBody: `{invalid json}`,
			wantErr:     true,
		},
		{
			name:        "Valid event",
			requestBody: `[{"event_id":1,"event_level":"info","event_type":"update","item_name":"test-secret","item_id":123,"item_type":"secret"}]`,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock container
			mockContainer, _ := container.NewMockContainer(t)

			// Create test request using GoFr's HTTP package
			req := gofrHTTP.NewRequest(http.MethodPost, "/webhook", bytes.NewBuffer([]byte(tt.requestBody)))
			req.Header.Set("Content-Type", "application/json")

			// Create GoFr context
			ctx := gofr.NewContext(req, nil, app)
			ctx.Container = mockContainer

			// Call handler
			_, err := WebhookHandler(ctx)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
