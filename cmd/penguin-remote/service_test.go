package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
)

func TestStoredConfigServiceBinding(t *testing.T) {
	for _, tc := range []struct {
		name, saved, current string
		match                bool
	}{
		{"canonical origin", "https://go.example.com", "https://GO.example.com:443/", true},
		{"legacy unbound", "", "https://go.example.com", false},
		{"different environment", "https://staging.example.com", "https://go.example.com", false},
		{"different prefix", "https://go.example.com/test", "https://go.example.com", false},
		{"different escaped prefix", "https://go.example.com/a%2Fb", "https://go.example.com/a/b", false},
		{"IPv6 origin", "http://[::1]:80", "http://[::1]/", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := identity()
			c.PenguinGoURL = tc.saved
			configPath := filepath.Join(t.TempDir(), "remote.json")
			if err := remote.SaveConfig(configPath, c); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(configPath)
			a := &appServer{configPath: configPath, penguinGoURL: tc.current}
			if err := a.loadStoredConfig(); err != nil {
				t.Fatal(err)
			}
			if tc.match {
				if a.config == nil || a.config.DeviceAccessToken != c.DeviceAccessToken {
					t.Fatal("Matching identity was not restored")
				}
			} else {
				if a.config != nil || a.lastError != "protocol_upgrade_required" {
					t.Fatal("Foreign or unbound identity was restored")
				}
				after, _ := os.ReadFile(configPath)
				if !bytes.Equal(before, after) {
					t.Fatal("Rejected configuration was overwritten")
				}
				a.restoreIntent()
				if a.intent != nil {
					t.Fatal("Rejected identity started a connection")
				}
			}
		})
	}
}

func TestForeignCredentialRejectedBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer next.Close()
	a := &appServer{penguinGoURL: next.URL}
	c := identity()
	if _, err := a.slotClient(c); err == nil {
		t.Fatal("Foreign identity accepted")
	}
	c.PenguinGoURL = ""
	if _, err := a.slotClient(c); err == nil {
		t.Fatal("Legacy unbound identity accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("Rejected credential reached the network")
	}
}

func TestLoginDoesNotInheritForeignGrantWithSameIDs(t *testing.T) {
	old := identity()
	old.Grant = &remoteapi.SlotGrant{GrantID: "old-grant"}
	old.Desired = true
	a, _, _ := newTestApp(t, nil, "https://staging.example.com")
	a.config = &old
	a.loginState = "login"
	fresh := identity()
	if err := a.installIdentity("login", fresh); err != nil {
		t.Fatal(err)
	}
	if a.config.PenguinGoURL != "https://staging.example.com" || a.config.Grant != nil || a.config.Desired {
		t.Fatal("Foreign grant or connection intent was inherited")
	}
	if len(a.cancellations) != 0 {
		t.Fatal("Foreign cancellation was assigned to the new service")
	}
}

func TestCancellationRetainsOriginalService(t *testing.T) {
	var requests atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"protocolVersion":2,"cancelled":true}`))
	}))
	defer original.Close()
	a, _, _ := newTestApp(t, nil, "https://new.example.com")
	task := &cancelTask{BaseURL: original.URL, UserID: "1", DeviceID: "device", RequestID: "old-request", AccessToken: "original-test-token"}
	c := identity()
	c.PenguinGoURL = apiIdentity(original.URL)
	cli, err := a.slotClientAt(c, task.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.Cancel(context.Background(), task.AccessToken, remoteapi.CancelRequest{RequestID: task.RequestID}); err != nil {
		t.Fatal(err)
	}
	a.cancellations = []*cancelTask{task}
	current := identity()
	current.PenguinGoURL = apiIdentity(a.penguinGoURL)
	if a.hasIdentityCancellationLocked(current) {
		t.Fatal("Foreign cancellation blocked the current service")
	}
	if requests.Load() != 1 {
		t.Fatal("Original-service cleanup did not reach its own service")
	}
}
