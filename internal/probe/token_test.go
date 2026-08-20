package probe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientCredentials_ExchangesSecretForAccessToken(t *testing.T) {
	var gotForm, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm = string(body)
		user, pass, _ := r.BasicAuth()
		gotAuth = user + ":" + pass
		_, _ = io.WriteString(w, `{"access_token":"at-1","token_type":"Bearer","expires_in":3600}`)
	}))
	defer srv.Close()

	cc := ClientCredentials{TokenURL: srv.URL, ClientID: "id", ClientSecret: "secret", Scope: "openid", HTTP: srv.Client()}
	token, err := cc.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "at-1" {
		t.Errorf("token: got %q", token)
	}
	if gotAuth != "id:secret" {
		t.Errorf("client must authenticate with basic auth: got %q", gotAuth)
	}
	if !strings.Contains(gotForm, "grant_type=client_credentials") || !strings.Contains(gotForm, "scope=openid") {
		t.Errorf("form: %s", gotForm)
	}
}

func TestClientCredentials_RejectsErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cc := ClientCredentials{TokenURL: srv.URL, HTTP: srv.Client()}
	if _, err := cc.Token(context.Background()); err == nil {
		t.Fatal("want an error for a 401 token exchange")
	}
}

func TestClientCredentials_EmptyTokenIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	cc := ClientCredentials{TokenURL: srv.URL, HTTP: srv.Client()}
	if _, err := cc.Token(context.Background()); err == nil {
		t.Fatal("an empty access_token must not pass as a token")
	}
}
