package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSingleUserTokenProtectsPagesAndAPI(t *testing.T) {
	server := NewServer(newAPITestRuntime(t)).WithSingleUserToken("test-access-token")

	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/runtime", nil))
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("expected bearer challenge, got status=%d headers=%v body=%s", unauthorized.Code, unauthorized.Header(), unauthorized.Body.String())
	}

	wrong := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	wrong.Header.Set("Authorization", "Bearer wrong-token")
	wrongResponse := httptest.NewRecorder()
	server.ServeHTTP(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected wrong token rejection, got %d", wrongResponse.Code)
	}

	authorized := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	authorized.Header.Set("Authorization", "Bearer test-access-token")
	authorizedResponse := httptest.NewRecorder()
	server.ServeHTTP(authorizedResponse, authorized)
	if authorizedResponse.Code != http.StatusOK {
		t.Fatalf("expected bearer access, got status=%d body=%s", authorizedResponse.Code, authorizedResponse.Body.String())
	}
}

func TestSingleUserTokenBrowserBootstrapUsesHashedSessionCookie(t *testing.T) {
	server := NewServer(newAPITestRuntime(t)).WithSingleUserToken("browser-access-token")
	bootstrap := httptest.NewRequest(http.MethodGet, "/console?lang=zh&access_token=browser-access-token", nil)
	bootstrapResponse := httptest.NewRecorder()
	server.ServeHTTP(bootstrapResponse, bootstrap)
	if bootstrapResponse.Code != http.StatusSeeOther || bootstrapResponse.Header().Get("Location") != "/console?lang=zh" {
		t.Fatalf("expected token-stripping redirect, got status=%d location=%q", bootstrapResponse.Code, bootstrapResponse.Header().Get("Location"))
	}
	cookies := bootstrapResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one auth cookie, got %#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != singleUserAuthCookie || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || strings.Contains(cookie.Value, "browser-access-token") {
		t.Fatalf("expected hashed HttpOnly SameSite cookie, got %#v", cookie)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected cookie-authenticated API access, got status=%d body=%s", response.Code, response.Body.String())
	}
}
