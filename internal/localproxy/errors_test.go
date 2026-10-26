package localproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Prism-Shadow/penguin-remote-client/internal/harnessapi"
	"github.com/Prism-Shadow/penguin-remote-client/internal/publicerror"
)

type failedIssuer struct{}

func (failedIssuer) IssueRemoteSession(context.Context) (harnessapi.RemoteSession, error) {
	return harnessapi.RemoteSession{}, errors.New("private-token 127.0.0.1:7364")
}
func (failedIssuer) ValidateSession(context.Context, string) (bool, error) {
	return false, errors.New("private-token 127.0.0.1:7364")
}

func TestLocalFailuresUseSanitizedRemotePage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	target := upstream.URL
	upstream.Close()
	for _, tc := range []struct {
		name, code string
		issuer     SessionIssuer
		initial    harnessapi.RemoteSession
	}{
		{"Harness stopped", "harness_unavailable", &fakeIssuer{}, testSession("initial")},
		{"session issue failed", "harness_session_failed", failedIssuer{}, harnessapi.RemoteSession{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Start(target, "", "http://public.remote.test", tc.issuer, tc.initial)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close(context.Background())
			ip, port := p.Backend()
			req, _ := http.NewRequest("GET", "http://"+net.JoinHostPort(ip, strconv.Itoa(port))+"/", nil)
			req.Header.Set("Accept", "text/html")
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != 502 || response.Header.Get(publicerror.Header) != tc.code || !strings.Contains(string(body), "Penguin Remote") {
				t.Fatal("missing unified error page")
			}
			if strings.Contains(string(body), "private-token") || strings.Contains(string(body), "127.0.0.1:") {
				t.Fatal("raw backend error exposed")
			}
		})
	}
}
