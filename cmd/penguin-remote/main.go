package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
)

const defaultConfigPath = "remote.dev.json"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "penguin-remote: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "dev-config":
		return runDevConfig(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	case "run":
		return runClient(args[1:])
	case "login":
		return runLogin(args[1:])
	case "app":
		return runApp(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runDevConfig(args []string) error {
	fs := flag.NewFlagSet("dev-config", flag.ContinueOnError)
	out := fs.String("out", defaultConfigPath, "config output path")
	deviceID := fs.String("device-id", hostnameFallback(), "Penguin Remote device id")
	userID := fs.String("user-id", "", "Penguin Go user id for metadata")
	serverAddr := fs.String("server-addr", "", "frps server address")
	serverPort := fs.Int("server-port", 7000, "frps server port")
	token := fs.String("token", "", "frps auth token")
	publicURL := fs.String("public-url", "", "public URL assigned by Penguin Go/gateway")
	customDomain := fs.String("domain", "", "custom domain registered on frps")
	subdomain := fs.String("subdomain", "", "subdomain registered on frps")
	localURL := fs.String("local", remote.DefaultHarnessURL, "local Penguin Harness URL")
	dataRoot := fs.String("data-root", "", "local Penguin Harness data root")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// The developer config is marked as such so only the CLI `run` command can
	// use it. The desktop product flow ignores the source and still requires a
	// Penguin Go subscription.
	cfg := remote.Config{
		Source: remote.SourceDevConfig,
		Device: remote.DeviceConfig{
			ID:     *deviceID,
			UserID: *userID,
		},
		Tunnel: &remote.TunnelConfig{
			ServerAddr:   *serverAddr,
			ServerPort:   *serverPort,
			Token:        *token,
			PublicURL:    *publicURL,
			CustomDomain: *customDomain,
			Subdomain:    *subdomain,
		},
		Harness: remote.HarnessConfig{
			LocalURL: *localURL,
			DataRoot: *dataRoot,
		},
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := remote.SaveConfig(*out, cfg); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", *out)
	return nil
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	out := fs.String("out", defaultConfigPath, "config output path")
	penguinGoURL := fs.String("penguin-go-url", remoteapi.DefaultBaseURL, "Penguin Go base URL")
	deviceName := fs.String("device-name", hostnameFallback(), "Penguin Remote device name")
	platform := fs.String("platform", runtime.GOOS, "device platform")
	localURL := fs.String("local", remote.DefaultHarnessURL, "local Penguin Harness URL")
	dataRoot := fs.String("data-root", "", "local Penguin Harness data root")
	listenAddr := fs.String("listen", "127.0.0.1:0", "local callback listen address")
	noBrowser := fs.Bool("no-browser", false, "print the login URL instead of opening a browser")
	timeout := fs.Duration("timeout", 5*time.Minute, "time to wait for browser login")
	requestTimeout := fs.Duration("request-timeout", 15*time.Second, "Penguin Go API request timeout")
	systemProxy := fs.String("system-proxy", "", "private desktop system-proxy bridge URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	network := remote.NetworkConfig{SystemProxyURL: *systemProxy, SystemProxySecret: os.Getenv("PENGUIN_REMOTE_SYSTEM_PROXY_SECRET")}
	if _, err := (remote.Config{Harness: remote.HarnessConfig{LocalURL: *localURL}}).LocalBackend(); err != nil {
		return err
	}
	httpClient, err := network.HTTPClient()
	if err != nil {
		return err
	}
	httpClient.Timeout = *requestTimeout

	state, err := randomState()
	if err != nil {
		return err
	}
	// The installation id is generated on first use and kept next to the config
	// file, so repeated logins from this install are recognisable as the same
	// one instead of as a new device every time.
	clientInstanceID, err := remote.LoadClientInstanceID(*out)
	if err != nil {
		return err
	}
	listener, callbackURL, err := listenForCallback(*listenAddr)
	if err != nil {
		return err
	}
	defer listener.Close()

	client, err := remoteapi.NewClient(remoteapi.ClientConfig{
		BaseURL:    *penguinGoURL,
		HTTPClient: httpClient,
	})
	if err != nil {
		return err
	}

	// The callback handler completes the exchange itself so the page it returns
	// can report the subscription state that Penguin Go actually granted.
	callbacks := make(chan loginCallback, 1)
	authorize := func(ctx context.Context, code string) loginCallback {
		exchangeCtx, cancel := context.WithTimeout(ctx, *requestTimeout)
		defer cancel()
		result, err := client.ExchangeClientCode(exchangeCtx, remoteapi.ClientLoginExchangeRequest{
			Code:            code,
			State:           state,
			HarnessLocalURL: *localURL,
			// Sent here as well as in the login URL, so a login that was
			// interrupted still carries the installation it belongs to.
			ClientInstanceID: clientInstanceID,
		})
		if err != nil {
			return loginCallback{Err: fmt.Errorf("exchange Penguin Remote login code: %w", err)}
		}
		// An active, expired and unprovisioned account all complete the login;
		// only a response the client cannot trust fails here, and it leaves the
		// previously saved config untouched.
		cfg, err := remote.ConfigFromLogin(result, remote.HarnessConfig{LocalURL: *localURL, DataRoot: *dataRoot}, time.Now())
		if err != nil {
			return loginCallback{Err: fmt.Errorf("Penguin Go returned an unusable Remote login response: %w", err)}
		}
		cfg.PenguinGoURL = apiIdentity(*penguinGoURL)
		if old, err := remote.LoadConfig(*out); err == nil && configServiceMatches(*old, *penguinGoURL) {
			if old.Device.ID == cfg.Device.ID && old.Device.UserID == cfg.Device.UserID {
				cfg.Grant = old.Grant
				cfg.Tunnel = old.Tunnel
				cfg.Desired = old.Desired
				cfg.PendingRequestID = old.PendingRequestID
				cfg.Harness = old.Harness
			} else {
				cli, _ := remoteapi.NewClient(remoteapi.ClientConfig{BaseURL: *penguinGoURL, HTTPClient: httpClient})
				if old.Grant != nil && cli != nil {
					_ = cli.Release(ctx, old.DeviceAccessToken, remoteapi.ReleaseRequest{GrantID: old.Grant.GrantID, SlotIndex: old.Grant.SlotIndex, Generation: old.Grant.Generation})
				}
			}
		}
		cfg.Network = network
		if err := remote.SaveConfig(*out, cfg); err != nil {
			return loginCallback{Err: err}
		}
		return loginCallback{Config: &cfg, Subscription: cfg.SubscriptionAt(time.Now())}
	}

	callbackServer := newCallbackServer(state, callbacks, authorize)
	go func() {
		if err := callbackServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			callbacks <- loginCallback{Err: err}
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = callbackServer.Shutdown(ctx)
	}()

	loginURL := client.ClientLoginURL(callbackURL, remoteapi.DeviceStartRequest{
		DeviceName:      *deviceName,
		Platform:        *platform,
		HarnessLocalURL: *localURL,
	}, state, clientInstanceID)
	fmt.Printf("Open this URL to authorize Penguin Remote:\n%s\n\n", loginURL)
	if !*noBrowser {
		if err := openBrowser(loginURL); err != nil {
			fmt.Println("Could not open browser automatically; use the authorization URL above.")
		}
	}
	fmt.Printf("Waiting for Penguin Go login callback on %s ...\n", callbackURL)

	waitCtx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var callback loginCallback
	select {
	case callback = <-callbacks:
		if callback.Err != nil {
			return callback.Err
		}
	case <-waitCtx.Done():
		return fmt.Errorf("timed out waiting for Penguin Go login callback: %w", waitCtx.Err())
	}

	cfg := callback.Config
	fmt.Printf("authorized remote device: %s\n", cfg.Device.ID)
	printSubscription(callback.Subscription)
	if cfg.Tunnel != nil && cfg.Tunnel.PublicURL != "" {
		fmt.Printf("public URL: %s\n", cfg.Tunnel.PublicURL)
	}
	fmt.Printf("wrote %s\n", *out)
	return nil
}

// printSubscription reports the subscription the login produced. It never prints
// the FRPS token, which stays inside the config file.
func printSubscription(subscription remote.Subscription) {
	switch subscription.Status {
	case remote.EntitlementStateActive:
		if subscription.ExpiresAt != nil {
			fmt.Printf("subscription: active until %s\n", subscription.ExpiresAt.Local().Format(time.RFC3339))
			return
		}
		fmt.Println("subscription: active")
	case remote.EntitlementStateExpired:
		if subscription.ExpiresAt != nil {
			fmt.Printf("subscription: expired at %s\n", subscription.ExpiresAt.Local().Format(time.RFC3339))
			return
		}
		fmt.Println("subscription: expired")
	case remote.EntitlementStateNone:
		fmt.Println("subscription: not enabled for this account")
	default:
		fmt.Println("subscription: information pending refresh")
	}
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "config path")
	timeout := fs.Duration("timeout", 3*time.Second, "local harness probe timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := remote.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: *timeout}
	resp, err := client.Get(cfg.Harness.LocalURL)
	if err != nil {
		return fmt.Errorf("local harness is not reachable at %s: %w", cfg.Harness.LocalURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("local harness returned %s", resp.Status)
	}
	printSubscription(cfg.SubscriptionAt(time.Now()))
	fmt.Printf("local harness reachable: %s\n", resp.Status)
	return nil
}

func runClient(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "config path")
	penguinGoURL := fs.String("penguin-go-url", remoteapi.DefaultBaseURL, "Penguin Go base URL")
	systemProxy := fs.String("system-proxy", "", "private desktop system-proxy bridge URL")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := remote.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Source != remote.SourceDevConfig && !configServiceMatches(*cfg, *penguinGoURL) {
		return errors.New("saved authorization does not belong to this Penguin Go service; sign in again")
	}
	if err := ensureCLIGrantSettled(*configPath, *cfg); err != nil {
		return err
	}
	cfg.Network.SystemProxyURL = *systemProxy
	cfg.Network.SystemProxySecret = os.Getenv("PENGUIN_REMOTE_SYSTEM_PROXY_SECRET")
	if err := cfg.TunnelStartError(time.Now(), remote.TunnelStartCLI); err != nil {
		return err
	}
	client, err := newClientRuntime(context.Background(), *cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); client.Close() }()

	if cfg.Tunnel.PublicURL != "" {
		fmt.Printf("public URL: %s\n", cfg.Tunnel.PublicURL)
	}
	fmt.Printf("forwarding %s through a local authenticated proxy and %s:%d on %s\n", cfg.Harness.LocalURL, cfg.Tunnel.ServerAddr, cfg.Tunnel.ServerPort, runtime.GOOS)
	return client.Run(ctx)
}

func printUsage() {
	fmt.Print(`Penguin Remote

Usage:
  penguin-remote dev-config -server-addr HOST -token TOKEN (-domain HOST|-subdomain NAME) [-local http://127.0.0.1:7364] [-data-root PATH]
  penguin-remote login [-penguin-go-url https://go.penguin.ooo] [-local http://127.0.0.1:7364] [-data-root PATH]
  penguin-remote app [-penguin-go-url https://go.penguin.ooo] [-local http://127.0.0.1:7364] [-data-root PATH]
  penguin-remote doctor [-config remote.dev.json]
  penguin-remote run [-config remote.dev.json]

dev-config writes a developer-only config that only "run" accepts. The desktop
app and any config written by "login" always require an active Penguin Go Remote
subscription before remote access can start.
`)
}

func hostnameFallback() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "penguin-remote-device"
	}
	return name
}

type loginCallback struct {
	Config       *remote.Config
	Subscription remote.Subscription
	Err          error
}

func listenForCallback(addr string) (net.Listener, string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || (host != "localhost" && !net.ParseIP(host).IsLoopback()) {
		return nil, "", errors.New("callback listener must use loopback")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("listen for local callback: %w", err)
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		listener.Close()
		return nil, "", fmt.Errorf("local callback listener returned %T, want TCP address", listener.Addr())
	}
	host = tcpAddr.IP.String()
	if host == "" || host == "::" {
		host = "127.0.0.1"
	}
	if tcpAddr.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	return listener, (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, fmt.Sprintf("%d", tcpAddr.Port)),
		Path:   "/callback",
	}).String(), nil
}

// newCallbackServer serves the Penguin Go browser redirect. The authorize
// callback performs the code exchange, so the page the browser lands on can
// report the subscription state instead of a generic "authorized" message.
func newCallbackServer(expectedState string, callbacks chan<- loginCallback, authorize func(context.Context, string) loginCallback) *http.Server {
	mux := http.NewServeMux()
	var consumed atomic.Bool
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		reject := func(status int, message string, err error) {
			http.Error(w, message, status)
			deliverCallback(callbacks, loginCallback{Err: err})
		}
		query := r.URL.Query()
		if got := query.Get("state"); got != expectedState {
			reject(http.StatusBadRequest, "invalid state", errors.New("Penguin Go callback state mismatch"))
			return
		}
		if remoteError := query.Get("error"); remoteError != "" {
			reject(http.StatusForbidden, "authorization failed", errors.New("login_failed"))
			return
		}
		code := query.Get("code")
		if code == "" {
			reject(http.StatusBadRequest, "missing code", errors.New("Penguin Go callback did not include a code"))
			return
		}
		if !consumed.CompareAndSwap(false, true) {
			http.Error(w, "sign-in already processed", http.StatusConflict)
			return
		}

		outcome := authorize(r.Context(), code)
		if outcome.Err != nil {
			outcome.Err = errors.New("login_failed")
			writeCallbackPage(w, http.StatusBadGateway, failureCallbackPage("Unable to complete sign-in. Please retry from Penguin Remote."))
		} else {
			writeCallbackPage(w, http.StatusOK, subscriptionCallbackPage(outcome.Subscription))
		}
		deliverCallback(callbacks, outcome)
	})
	return &http.Server{Handler: mux}
}

func deliverCallback(callbacks chan<- loginCallback, callback loginCallback) {
	select {
	case callbacks <- callback:
	default:
	}
}

// callbackPage is the browser page shown after a Penguin Go login returns to the
// local client. The desktop app and the CLI login render the same page.
type callbackPage struct {
	Heading string
	Detail  string
	// Tone drives the heading color: success, warning or danger.
	Tone string
}

func subscriptionCallbackPage(subscription remote.Subscription) callbackPage {
	return identityCallbackPage("zh", subscription)
}

func failureCallbackPage(message string) callbackPage {
	return callbackPage{Heading: "授权失败", Detail: message, Tone: "danger"}
}

func writeCallbackPage(w http.ResponseWriter, status int, page callbackPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprint(w, renderCallbackPage(page))
}

const callbackPageTemplate = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{heading}} · Penguin Remote</title>
<style>
:root { color-scheme: light dark; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; --bg: #ffffff; --surface: #ffffff; --border: #e5e7eb; --text: #111827; --text-soft: #4b5563; --success: #047857; --warning: #b45309; --danger: #b42318; }
* { box-sizing: border-box; }
body { display: flex; min-height: 100vh; align-items: center; justify-content: center; margin: 0; padding: 24px; background: var(--bg); color: var(--text); }
.card { width: 100%; max-width: 420px; padding: 28px 28px 30px; border: 1px solid var(--border); border-radius: 12px; background: var(--surface); text-align: center; }
.logo svg { width: 56px; height: 56px; border-radius: 12px; }
h1 { margin: 18px 0 0; font-size: 19px; line-height: 26px; }
p { margin: 10px 0 0; color: var(--text-soft); font-size: 14px; line-height: 22px; overflow-wrap: anywhere; }
.tone-success h1 { color: var(--success); }
.tone-warning h1 { color: var(--warning); }
.tone-danger h1 { color: var(--danger); }
@media (prefers-color-scheme: dark) { :root { --bg: #000000; --surface: #0d0d0d; --border: #262626; --text: #f3f4f6; --text-soft: #d1d5db; --success: #34d399; --warning: #fbbf24; --danger: #fca5a5; } }
</style>
</head>
<body>
<div class="card tone-{{tone}}">
<div class="logo">{{logo}}</div>
<h1>{{heading}}</h1>
<p>{{detail}}</p>
</div>
</body>
</html>
`

func renderCallbackPage(page callbackPage) string {
	return strings.NewReplacer(
		"{{tone}}", page.Tone,
		"{{logo}}", string(penguinLogoSVG),
		"{{heading}}", html.EscapeString(page.Heading),
		"{{detail}}", html.EscapeString(page.Detail),
	).Replace(callbackPageTemplate)
}

func randomState() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate login state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func openBrowser(target string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		return exec.Command("open", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}
