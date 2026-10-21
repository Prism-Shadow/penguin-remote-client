package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
)

//go:embed assets/penguin-logo.svg
var penguinLogoSVG []byte

//go:embed assets/wechat-support-qr.png
var supportWechatQR []byte

//go:embed assets/app.html
var appPageHTML string

//go:embed assets/guides/*.png assets/guides/*.svg
var guideImages embed.FS

type appServer struct {
	mu sync.Mutex

	baseURL      string
	configPath   string
	penguinGoURL string
	deviceName   string
	platform     string
	localURL     string
	dataRoot     string
	language     string
	theme        string
	discovered   []remote.HarnessInstance

	loginState string
	lastError  string
	config     *remote.Config
	// clientInstanceID identifies this installation to Penguin Go. It is read
	// once at start-up and reused for every login.
	clientInstanceID string

	runnerStatus string
	authStatus   string

	// tunnel is the running tunnel, if any. It is installed and cleared under mu.
	tunnel *runningTunnel
	// intent owns a user-requested connection across acquire and network recovery.
	intent            *connectionIntent
	systemProxyURL    string
	systemProxySecret string
	newSlotClient     func(remote.Config) (slotClient, error)
	// newRuntime builds the tunnel runtime. The product always uses the real frp
	// client; lifecycle tests can replace it while real protocol tests use frps.
	newRuntime          func(ctx context.Context, cfg remote.Config) (tunnelRuntime, error)
	cancellations       []*cancelTask
	cancelWake          chan struct{}
	cancelContext       context.Context
	cancelShutdown      context.CancelFunc
	cancelJournalDirty  bool
	cancelConfigDirty   bool
	cancelJournalBroken bool
	saveConfig          func(string, remote.Config) error
	saveJournal         func(string, any) error
}

// runningTunnel bundles everything needed to stop a started tunnel exactly once,
// across stop, account replacement and asynchronous runtime completion.
type runningTunnel struct {
	runtime tunnelRuntime
	// everConnected distinguishes the first registration from a later reconnect.
	// It is updated while appServer.mu is held.
	everConnected bool
	// cancel ends the frp client when this tunnel loses its owner.
	cancel context.CancelFunc
}

func runApp(args []string) error {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	systemProxy := fs.String("system-proxy", "", "desktop loopback network bridge")
	listenAddr := fs.String("listen", "127.0.0.1:18973", "local client UI listen address")
	configPath := fs.String("config", defaultConfigPath, "config path")
	penguinGoURL := fs.String("penguin-go-url", remoteapi.DefaultBaseURL, "Penguin Go base URL")
	deviceName := fs.String("device-name", hostnameFallback(), "Penguin Remote device name")
	platform := fs.String("platform", runtime.GOOS, "device platform")
	localURL := fs.String("local", remote.DefaultHarnessURL, "local Penguin Harness URL")
	dataRoot := fs.String("data-root", "", "local Penguin Harness data root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := remoteapi.NewClient(remoteapi.ClientConfig{BaseURL: *penguinGoURL}); err != nil {
		return err
	}
	uiHost, _, err := net.SplitHostPort(*listenAddr)
	if err != nil || (uiHost != "localhost" && !net.ParseIP(uiHost).IsLoopback()) {
		return errors.New("client UI must use loopback")
	}
	if _, err := (remote.Config{Harness: remote.HarnessConfig{LocalURL: *localURL}}).LocalBackend(); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen for local client UI: %w", err)
	}
	defer listener.Close()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("local client UI listener returned %T, want TCP address", listener.Addr())
	}
	host := tcpAddr.IP.String()
	if host == "" || tcpAddr.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	baseURL := (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, fmt.Sprintf("%d", tcpAddr.Port)),
	}).String()

	// The installation id is generated on first use and kept next to the config
	// file, so every login from this install is recognisable as the same one
	// across restarts.
	clientInstanceID, err := remote.LoadClientInstanceID(*configPath)
	if err != nil {
		return err
	}

	app := &appServer{
		baseURL:           baseURL,
		systemProxyURL:    *systemProxy,
		systemProxySecret: os.Getenv("PENGUIN_REMOTE_SYSTEM_PROXY_SECRET"),
		configPath:        *configPath,
		penguinGoURL:      *penguinGoURL,
		deviceName:        *deviceName,
		platform:          *platform,
		localURL:          *localURL,
		dataRoot:          *dataRoot,
		language:          loadLanguage(*configPath),
		theme:             loadTheme(*configPath),
		clientInstanceID:  clientInstanceID,
		runnerStatus:      "stopped",
		authStatus:        "unchecked",
	}
	if err := app.loadStoredConfig(); err != nil {
		return err
	}
	_ = app.refreshHarnessDiscovery(context.Background())
	app.loadCancellations()
	defer app.closeCancellations()
	app.restoreIntent()

	server := &http.Server{
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Printf("Penguin Remote client is ready: %s\n", baseURL)
	fmt.Println("Open this local client page and click Login yourself.")
	return server.Serve(listener)
}

func (a *appServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/assets/penguin-logo.svg", a.handlePenguinLogo)
	mux.HandleFunc("/assets/wechat-support-qr.png", a.handleSupportWechatQR)
	mux.Handle("/assets/guides/", http.FileServer(http.FS(guideImages)))
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/api/subscription/refresh", a.handleSubscriptionRefresh)
	mux.HandleFunc("/api/wake", a.handleWake)
	mux.HandleFunc("/api/cancellation/retry", a.handleCancelRetry)
	mux.HandleFunc("/api/preferences/language", a.handleLanguage)
	mux.HandleFunc("/api/preferences/theme", a.handleTheme)
	mux.HandleFunc("/api/harness/rescan", a.handleHarnessRescan)
	mux.HandleFunc("/api/login/start", a.handleLoginStart)
	mux.HandleFunc("/callback", a.handleCallback)
	mux.HandleFunc("/api/tunnel/start", a.handleTunnelStart)
	mux.HandleFunc("/api/tunnel/stop", a.handleTunnelStop)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "invalid origin", 403)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != "http://"+r.Host {
				http.Error(w, "invalid origin", 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *appServer) handlePenguinLogo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(penguinLogoSVG)
}

func (a *appServer) handleSupportWechatQR(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(supportWechatQR)
}

func (a *appServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, appPageHTML)
}

func (a *appServer) handleStatus(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()

	subscription := remote.Subscription{Status: remote.EntitlementStateUnknown}
	if a.config != nil {
		subscription = a.config.SubscriptionAt(now)
	}
	// expiresAt is null rather than an empty string so a client can tell "no
	// expiry" from "expired at the zero time".
	var expiresAt any
	if subscription.ExpiresAt != nil {
		expiresAt = subscription.ExpiresAt.UTC().Format(time.RFC3339)
	}

	var leaseExpiresAt any
	if subscription.LeaseExpiresAt != nil {
		leaseExpiresAt = subscription.LeaseExpiresAt.UTC().Format(time.RFC3339)
	}
	connectionStatus := "idle"
	if a.tunnel != nil {
		if a.tunnel.runtime.ProxyConnected() {
			a.tunnel.everConnected = true
			connectionStatus = "connected"
		} else if a.tunnel.everConnected {
			connectionStatus = "reconnecting"
		} else {
			connectionStatus = "connecting"
		}
	} else if a.intent != nil {
		connectionStatus = a.runnerStatus
	}

	if a.tunnel != nil {
		if runtime, ok := a.tunnel.runtime.(interface{ ConnectionPhase() string }); ok {
			if phase := runtime.ConnectionPhase(); phase != "" {
				connectionStatus = phase
			}
		}
	}
	cancelPending := a.cancelJournalBroken || a.cancelJournalDirty || a.cancelConfigDirty
	if a.config != nil {
		for _, task := range a.cancellations {
			if taskDeviceMatches(task, *a.config) && (a.intent == nil || a.intent.requestID != task.RequestID || !a.sameTaskIdentity(task, *a.config)) {
				cancelPending = true
			}
		}
	}
	lastError := a.lastError
	harnessLoginMode := ""
	if a.intent != nil {
		harnessLoginMode = string(a.intent.loginMode)
	}
	if cancelPending {
		lastError = a.cancellationMessageLocked()
	}
	writeJSON(w, map[string]any{
		"language":           a.language,
		"theme":              a.theme,
		"penguinGoUrl":       a.penguinGoURL,
		"localUrl":           a.localURL,
		"dataRoot":           a.dataRoot,
		"harnessFound":       len(a.discovered) > 0,
		"harnesses":          a.discovered,
		"configured":         a.config != nil,
		"loggedIn":           a.config != nil && a.config.DeviceAccessToken != "",
		"desired":            a.intent != nil,
		"entitlementStatus":  string(subscription.Status),
		"expiresAt":          expiresAt,
		"subscriptionActive": subscription.Status == remote.EntitlementStateActive,
		"renewalUrl":         subscription.RenewalURL,
		"tunnelReady":        subscription.TunnelReady,
		// The slot the client occupies and how many the account may run at once.
		// maxConcurrentTunnels is 0 when Penguin Go did not say.
		"leaseState":           string(subscription.LeaseState),
		"leaseExpiresAt":       leaseExpiresAt,
		"slotIndex":            subscription.SlotIndex,
		"maxConcurrentTunnels": subscription.MaxConcurrentTunnels,
		"publicUrl":            configPublicURL(a.config, subscription),
		"deviceId":             configDeviceID(a.config),
		"userName":             configUserName(a.config),
		"runnerStatus":         a.runnerStatus,
		"connectionStatus":     connectionStatus,
		"authStatus":           a.authStatus,
		"harnessLoginMode":     harnessLoginMode,
		"lastError":            lastError,
		"cancelPending":        cancelPending,
	})
}

func (a *appServer) handleHarnessRescan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := a.refreshHarnessDiscovery(r.Context()); err != nil {
		a.setError("service_unavailable")
	}
	a.handleStatus(w, r)
}

func (a *appServer) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		LocalURL string `json:"localUrl"`
		DataRoot string `json:"dataRoot"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.LocalURL == "" {
		_ = a.refreshHarnessDiscovery(r.Context())
		a.mu.Lock()
		req.LocalURL = a.localURL
		if req.DataRoot == "" {
			req.DataRoot = a.dataRoot
		}
		a.mu.Unlock()
	} else if req.DataRoot == "" {
		a.mu.Lock()
		req.DataRoot = a.dataRoot
		a.mu.Unlock()
	}
	if _, err := (remote.Config{Harness: remote.HarnessConfig{LocalURL: req.LocalURL}}).LocalBackend(); err != nil {
		http.Error(w, "service_unavailable", http.StatusBadRequest)
		return
	}
	state, err := randomState()
	if err != nil {
		http.Error(w, "service_unavailable", http.StatusInternalServerError)
		return
	}
	client, err := a.identityClient()
	if err != nil {
		http.Error(w, "service_unavailable", http.StatusBadRequest)
		return
	}

	a.mu.Lock()
	a.loginState = state
	a.localURL = req.LocalURL
	a.dataRoot = req.DataRoot
	a.authStatus = "unchecked"
	a.lastError = ""
	a.mu.Unlock()

	loginURL := client.ClientLoginURL(a.baseURL+"/callback", remoteapi.DeviceStartRequest{
		DeviceName:      a.deviceName,
		Platform:        a.platform,
		HarnessLocalURL: req.LocalURL,
	}, state, a.clientInstanceID)
	writeJSON(w, map[string]string{"loginUrl": loginURL})
}

func (a *appServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	state := query.Get("state")
	code := query.Get("code")
	remoteError := query.Get("error")

	a.mu.Lock()
	expectedState := a.loginState
	localURL := a.localURL
	dataRoot := a.dataRoot
	a.mu.Unlock()

	if remoteError != "" {
		a.setLoginError(state, "login_failed")
		http.Error(w, "authorization failed", http.StatusForbidden)
		return
	}
	if state == "" || state != expectedState {
		a.setLoginError(state, "Penguin Go callback state mismatch")
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	if code == "" {
		a.setLoginError(state, "Penguin Go callback did not include a code")
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	client, err := a.identityClient()
	if err != nil {
		a.setLoginError(state, "service_unavailable")
		http.Error(w, "service_unavailable", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result, err := client.ExchangeClientCode(ctx, remoteapi.ClientLoginExchangeRequest{
		Code:            code,
		State:           state,
		HarnessLocalURL: localURL,
		// Repeated here as well as in the login URL, so a login that was
		// interrupted - or one started from a link left open in a browser - is
		// still attributed to this installation.
		ClientInstanceID: a.clientInstanceID,
	})
	if err != nil {
		a.setLoginError(state, "service_unavailable")
		http.Error(w, "login_failed", http.StatusBadGateway)
		return
	}

	// Every subscription state completes the administrator login, but a response
	// the client cannot trust must not overwrite the config already on disk.
	cfg, err := remote.ConfigFromLogin(result, remote.HarnessConfig{LocalURL: localURL, DataRoot: dataRoot}, time.Now())
	if err != nil {
		a.setLoginError(state, "service_unavailable")
		http.Error(w, "service_unavailable", http.StatusBadGateway)
		return
	}
	if err := a.installIdentity(expectedState, cfg); err != nil {
		http.Error(w, "login_failed", http.StatusConflict)
		return
	}
	a.mu.Lock()
	language := a.language
	a.mu.Unlock()
	writeCallbackPage(w, http.StatusOK, identityCallbackPage(language, cfg.SubscriptionAt(time.Now())))
}

func (a *appServer) refreshHarnessDiscovery(ctx context.Context) error {
	found, err := remote.DiscoverHarness(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.discovered = nil
		a.authStatus = "unchecked"
		return err
	}
	a.discovered = found
	for _, instance := range found {
		if instance.URL == a.localURL {
			a.authStatus = "unchecked"
			return nil
		}
	}
	a.localURL = found[0].URL
	a.authStatus = "unchecked"
	return nil
}

func (a *appServer) setError(message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastError = message
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

// configPublicURL only reports an address while the subscription is active,
// because the tunnel it points at cannot be running otherwise.
func configPublicURL(cfg *remote.Config, subscription remote.Subscription) string {
	if cfg != nil && cfg.DeviceAccessToken != "" && cfg.Grant != nil && cfg.Tunnel != nil {
		return cfg.Tunnel.PublicURL
	}
	if cfg == nil || cfg.Tunnel == nil || subscription.Status != remote.EntitlementStateActive {
		return ""
	}
	return cfg.Tunnel.PublicURL
}

func configDeviceID(cfg *remote.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Device.ID
}

func configUserName(cfg *remote.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Device.UserName
}
