// Package publicerror renders the public Remote failure page and API errors.
package publicerror

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const Path = "/_penguin/remote/error"
const Header = "X-Penguin-Remote-Error"

//go:embed assets/*
var assets embed.FS

var pageTemplate = template.Must(template.ParseFS(assets, "assets/error.html"))

type message struct {
	status                               int
	zhTitle, enTitle, zhDetail, enDetail string
	login                                bool
}

var messages = map[string]message{
	"login_required":                 {401, "请登录后继续访问", "Sign in to continue", "请使用开启这条远程连接的 Penguin Go 账号登录。", "Sign in with the Penguin Go account that started this remote connection.", true},
	"remote_host_forbidden":          {403, "当前账号无法访问", "This account cannot access this link", "请使用开启这条远程连接的账号登录，然后重新访问。", "Sign in with the account that started this remote connection, then try again.", true},
	"remote_host_not_found":          {404, "远程链接暂时不可用", "Remote link unavailable", "请确认链接是否正确，以及当前账号是否可以访问。也可以在原设备上检查远程访问是否已开启。", "Check the link and whether your account can access it. You can also check that remote access is on at the original device.", false},
	"admin_required":                 {403, "当前账号尚未获准使用 Remote", "Remote access is not enabled", "请确认登录账号是否正确。如需使用 Remote，请联系管理员。", "Check that you signed in with the correct account. Contact an administrator to enable Remote access.", false},
	"subscription_required":          {403, "Remote 服务尚未开通", "Remote subscription required", "请在 Penguin Go 开通 Remote 服务，再开启远程访问。", "Subscribe to Remote in Penguin Go, then turn on remote access.", false},
	"subscription_expired":           {403, "Remote 服务已到期", "Remote subscription expired", "请在 Penguin Go 续订 Remote 服务，再返回重试。", "Renew your Remote subscription in Penguin Go, then try again.", false},
	"subscription_not_provisioned":   {403, "暂无可用连接额度", "No connection allowance available", "请在 Penguin Go 检查 Remote 的订阅和连接额度，再返回重试。", "Check your Remote subscription and connection allowance in Penguin Go, then try again.", false},
	"invalid_remote_code":            {400, "登录链接已失效", "Sign-in link is invalid", "请重新登录后访问。请勿重复使用或分享登录回跳链接。", "Sign in again to continue. Do not reuse or share a sign-in callback link.", true},
	"remote_code_invalid_or_expired": {401, "登录链接已失效", "Sign-in link expired", "登录链接已过期或已使用，请重新登录后访问。", "This sign-in link has expired or was already used. Sign in again to continue.", true},
	"authorization_unavailable":      {502, "暂时无法验证访问权限", "Access verification unavailable", "暂时无法连接 Penguin Go 验证访问权限，请稍后重试。", "Penguin Go could not verify access right now. Please try again shortly.", false},
	"gateway_secret_invalid":         {403, "远程服务配置异常", "Remote service configuration issue", "远程服务暂时无法验证访问权限，请联系管理员。", "The remote service cannot verify access. Please contact an administrator.", false},
	"gateway_secret_not_configured":  {503, "远程服务尚未就绪", "Remote service is not ready", "远程服务暂时无法验证访问权限，请联系管理员。", "The remote service cannot verify access. Please contact an administrator.", false},
	"tunnel_unavailable":             {503, "远程连接暂时中断", "Remote connection interrupted", "请确认原设备已联网，并已开启 Penguin Remote。连接恢复后，请重试。", "Check that the original device is online and Penguin Remote is on. Try again when the connection is restored.", false},
	"tunnel_timeout":                 {504, "远程连接响应超时", "Remote connection timed out", "原设备暂时没有响应，请检查网络和远程连接状态后重试。", "The original device did not respond in time. Check its network and remote connection, then try again.", false},
	"harness_unavailable":            {502, "无法连接本机 Harness", "Harness is unavailable", "请在原设备上确认 Penguin Harness 已启动，再返回重试。", "Check that Penguin Harness is running on the original device, then try again.", false},
	"harness_session_failed":         {502, "暂时无法连接 Harness", "Cannot access Harness right now", "请在原设备上检查 Penguin Harness 和 Remote 的连接状态，再返回重试。", "Check Penguin Harness and Remote on the original device, then try again.", false},
	"access_denied":                  {403, "暂时无法访问此链接", "Cannot access this link", "请检查当前账号及远程连接状态。如问题持续，请联系管理员。", "Check your account and remote connection. Contact an administrator if the issue persists.", false},
}

// KnownCode never exposes arbitrary upstream errors or account information.
func KnownCode(code string) string {
	if _, ok := messages[code]; ok {
		return code
	}
	return "access_denied"
}

func Status(code string) int { return messages[KnownCode(code)].status }

// IsNavigation keeps API, stream and WebSocket failures out of the HTML flow.
func IsNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") || r.Header.Get("Upgrade") != "" {
		return false
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "text/html") && !strings.Contains(accept, "text/event-stream") && !strings.Contains(accept, "application/json")
}

// ReturnPath permits only local paths and removes one-time sign-in credentials.
func ReturnPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(u.Path, "\\\r\n\x00") {
		return "/"
	}
	if u.Path == Path {
		return "/"
	}
	q := u.Query()
	q.Del("remote_code")
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String()
}

type pageData struct {
	Lang, Title, Detail, Badge, Action, ActionURL, Retry, RetryURL string
	Logo                                                           template.URL
}

// Write uses the same renderer for Gateway and local Harness proxy failures.
// The status is retained; only recognized codes and static copy reach visitors.
func Write(w http.ResponseWriter, r *http.Request, status int, code, loginURL string) {
	code = KnownCode(code)
	m := messages[code]
	if status < 400 || status > 599 {
		status = m.status
	}
	en := strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "en")
	title, detail := m.zhTitle, m.zhDetail
	if en {
		title, detail = m.enTitle, m.enDetail
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Add("Vary", "Accept, Accept-Language")
	if !IsNavigation(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": title, "reason": code, "message": detail})
		}
		return
	}
	d := pageData{Lang: "zh-CN", Title: title, Detail: detail, Badge: "远程访问", Action: "重新尝试", Retry: "重新尝试", RetryURL: ReturnPath(r.URL.RequestURI())}
	if r.URL.Path == Path {
		d.RetryURL = ReturnPath(r.URL.Query().Get("return_to"))
	}
	d.ActionURL = d.RetryURL
	if en {
		d.Lang, d.Badge, d.Action, d.Retry = "en", "Remote access", "Try again", "Try again"
	}
	if m.login {
		if u, err := url.Parse(loginURL); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil {
			d.ActionURL = u.String()
			d.Action = "登录 Penguin Go"
			if en {
				d.Action = "Sign in to Penguin Go"
			}
		}
	}
	logo, _ := assets.ReadFile("assets/penguin-logo.svg")
	d.Logo = template.URL("data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(logo))
	var body bytes.Buffer
	if err := pageTemplate.Execute(&body, d); err != nil {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Length", strconv.Itoa(body.Len()))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}
