package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bitbomdev/minefield/pkg/graph"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestOptions_AddFlags(t *testing.T) {
	tests := []struct {
		name           string
		initialOptions *options
		expectedAddr   string
		expectedConc   int32
		flagValues     map[string]string
		shouldSetFlags bool
	}{
		{
			name:           "default values",
			initialOptions: &options{},
			expectedAddr:   "localhost:8089",
			expectedConc:   10,
			shouldSetFlags: false,
		},
		{
			name:           "custom values",
			initialOptions: &options{},
			expectedAddr:   "0.0.0.0:9000",
			expectedConc:   20,
			flagValues: map[string]string{
				"addr":        "0.0.0.0:9000",
				"concurrency": "20",
			},
			shouldSetFlags: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			tt.initialOptions.AddFlags(cmd)

			// If we should set flags, do so
			if tt.shouldSetFlags {
				for flag, value := range tt.flagValues {
					err := cmd.Flags().Set(flag, value)
					assert.NoError(t, err)
				}
			}

			// Get the flags and verify their values
			addr, err := cmd.Flags().GetString("addr")
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedAddr, addr)

			conc, err := cmd.Flags().GetInt32("concurrency")
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedConc, conc)
		})
	}
}

type mockStorage struct {
	graph.Storage
}

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		storage graph.Storage
		want    struct {
			use   string
			short string
		}
	}{
		{
			name:    "creates server command with correct properties",
			storage: &mockStorage{},
			want: struct {
				use   string
				short string
			}{
				use:   "server",
				short: "Start the minefield server for graph operations and queries",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := New()

			assert.NotNil(t, cmd)
			assert.Equal(t, tt.want.use, cmd.Use)
			assert.Equal(t, tt.want.short, cmd.Short)
			assert.True(t, cmd.DisableAutoGenTag)

			// Verify flags are added
			flags := cmd.Flags()
			concurrencyFlag := flags.Lookup("concurrency")
			assert.NotNil(t, concurrencyFlag)
			assert.Equal(t, "10", concurrencyFlag.DefValue)

			addrFlag := flags.Lookup("addr")
			assert.NotNil(t, addrFlag)
			assert.Equal(t, "localhost:8089", addrFlag.DefValue)
		})
	}
}
func TestSetupServer(t *testing.T) {
	o := &options{
		storage:     &mockStorage{},
		concurrency: 10,
		addr:        "localhost:8089",
	}

	srv, err := o.setupServer()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if srv.Addr != "localhost:8089" {
		t.Errorf("Expected address 'localhost:8089', got '%s'", srv.Addr)
	}

	if srv.Handler == nil {
		t.Error("Expected handler to be set, got nil")
	}
}

func TestOptions_PersistentPreRunE(t *testing.T) {
	tests := []struct {
		name         string
		options      *options
		wantErr      bool
		errorMessage string
	}{
		{
			name: "SQLite with empty StoragePath",
			options: &options{
				StorageType: sqliteStorageType,
				StoragePath: "",
			},
			wantErr:      true,
			errorMessage: "storage-path is required when using SQLite with file-based storage",
		},
		{
			name: "Redis with empty StorageAddr",
			options: &options{
				StorageType: redisStorageType,
				StorageAddr: "",
			},
			wantErr:      true,
			errorMessage: "storage-addr is required when using Redis (format: host:port)",
		},
		{
			name: "SQLite with valid StoragePath",
			options: &options{
				StorageType: sqliteStorageType,
				StoragePath: "/path/to/sqlite.db",
			},
			wantErr: false,
		},
		{
			name: "Redis with valid StorageAddr and UseInMemory disabled",
			options: &options{
				StorageType: redisStorageType,
				StorageAddr: "localhost:6379",
				UseInMemory: false,
			},
			wantErr: false,
		},
		{
			name: "Unsupported StorageType",
			options: &options{
				StorageType: "unsupported",
			},
			wantErr:      true,
			errorMessage: `invalid storage-type "unsupported": must be one of [redis, sqlite]`,
		},
	}

	cmd := &cobra.Command{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.options.PersistentPreRunE(cmd, []string{})
			if (err != nil) != tt.wantErr {
				t.Errorf("PersistentPreRunE() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if err.Error() != tt.errorMessage {
					t.Errorf("PersistentPreRunE() error message = %v, want %v", err.Error(), tt.errorMessage)
				}
			}
		})
	}
}

func TestWithCORS(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name           string
		options        options
		requestOrigin  string
		expectedOrigin string
	}{
		{
			name: "Allowed Origin",
			options: options{
				CORS: []string{"http://localhost:3000", "https://example.com"},
			},
			requestOrigin:  "http://localhost:3000",
			expectedOrigin: "http://localhost:3000",
		},
		{
			name: "Disallowed Origin",
			options: options{
				CORS: []string{"http://localhost:3000", "https://example.com"},
			},
			requestOrigin:  "http://malicious.com",
			expectedOrigin: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a dummy handler that writes a 200 OK status
			dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			// Wrap the dummy handler with CORS middleware
			handler := withCORS(dummyHandler, &tc.options)

			// Create a new HTTP request with the specified Origin header
			req := httptest.NewRequest("GET", "http://localhost:8089/test", nil)
			req.Header.Set("Origin", tc.requestOrigin)

			// Create a ResponseRecorder to capture the response
			rr := httptest.NewRecorder()

			// Serve the HTTP request
			handler.ServeHTTP(rr, req)

			// Check the CORS headers
			if tc.expectedOrigin != "" {
				assert.Equal(t, tc.expectedOrigin, rr.Header().Get("Access-Control-Allow-Origin"), "Access-Control-Allow-Origin should match the allowed origin")
			} else {
				assert.Empty(t, rr.Header().Get("Access-Control-Allow-Origin"), "Access-Control-Allow-Origin should be empty for disallowed origins")
			}

			// Optionally, check other CORS headers if needed
			if rr.Header().Get("Access-Control-Allow-Credentials") != "" {
				assert.Equal(t, "true", rr.Header().Get("Access-Control-Allow-Credentials"), "Access-Control-Allow-Credentials should be true")
			}
		})
	}
}

func TestNewServerCommand(t *testing.T) {
	tests := []struct {
		name        string
		storage     graph.Storage
		options     *options
		wantErr     bool
		wantCommand struct {
			use   string
			short string
		}
	}{
		{
			name:    "creates server command with valid storage and options",
			storage: &mockStorage{},
			options: &options{
				concurrency: 10,
				addr:        "localhost:8089",
			},
			wantErr: false,
			wantCommand: struct {
				use   string
				short string
			}{
				use:   "server",
				short: "Start the minefield server for graph operations and queries",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := NewServerCommand(tt.storage, tt.options)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, cmd)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, cmd)
			assert.Equal(t, tt.wantCommand.use, cmd.Use)
			assert.Equal(t, tt.wantCommand.short, cmd.Short)
			assert.True(t, cmd.DisableAutoGenTag)

			// Verify storage is set correctly in options
			assert.Equal(t, tt.storage, tt.options.storage)

			// Verify flags are added
			flags := cmd.Flags()

			concurrencyFlag := flags.Lookup("concurrency")
			assert.NotNil(t, concurrencyFlag)
			assert.Equal(t, "10", concurrencyFlag.DefValue)

			addrFlag := flags.Lookup("addr")
			assert.NotNil(t, addrFlag)
			assert.Equal(t, "localhost:8089", addrFlag.DefValue)
		})
	}
}
