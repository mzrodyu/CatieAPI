package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func testRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	server := NewServer()
	router := gin.New()
	router.Use(server.requestMiddleware())
	router.Use(server.corsMiddleware())
	server.registerRoutes(router)
	return router
}

func withEnv(t *testing.T, values map[string]string) {
	t.Helper()
	previous := map[string]string{}
	present := map[string]bool{}
	for key, value := range values {
		old, ok := os.LookupEnv(key)
		previous[key] = old
		present[key] = ok
		if err := os.Setenv(key, value); err != nil {
			t.Fatalf("set env %s: %v", key, err)
		}
	}
	t.Cleanup(func() {
		for key := range values {
			if present[key] {
				_ = os.Setenv(key, previous[key])
			} else {
				_ = os.Unsetenv(key)
			}
		}
	})
}

func perform(router http.Handler, method string, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestAdminTokenProtectsManagementRoutes(t *testing.T) {
	withEnv(t, map[string]string{
		"ADMIN_TOKEN": "admin-secret",
		"PERSISTENCE": "memory",
	})
	router := testRouter(t)

	public := perform(router, http.MethodGet, "/api/health", "", nil)
	if public.Code != http.StatusOK {
		t.Fatalf("health status = %d", public.Code)
	}

	blocked := perform(router, http.MethodGet, "/api/users", "", nil)
	if blocked.Code != http.StatusUnauthorized {
		t.Fatalf("users without admin token status = %d", blocked.Code)
	}

	allowed := perform(router, http.MethodGet, "/api/users", "", map[string]string{"Authorization": "Bearer admin-secret"})
	if allowed.Code != http.StatusOK {
		t.Fatalf("users with admin token status = %d body = %s", allowed.Code, allowed.Body.String())
	}
}

func TestCreatedAPIKeyUsesSecretWithoutLeakingHash(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	router := testRouter(t)

	created := perform(router, http.MethodPost, "/api/users/usr_1002/api-keys", `{"name":"Runtime Key"}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create key status = %d body = %s", created.Code, created.Body.String())
	}
	if bytes.Contains(created.Body.Bytes(), []byte(`"hash"`)) {
		t.Fatal("api key hash leaked in response")
	}

	var payload struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode create key response: %v", err)
	}
	if payload.Secret == "" {
		t.Fatal("created key did not include one-time secret")
	}
	if !strings.HasPrefix(payload.Secret, "cat_") {
		t.Fatalf("created key secret prefix = %s", payload.Secret)
	}
	if strings.HasPrefix(payload.Secret, "cat_sk_") {
		t.Fatalf("created key still uses old cat_sk prefix = %s", payload.Secret)
	}

	chatBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer " + payload.Secret})
	if chat.Code != http.StatusOK {
		t.Fatalf("chat with created key status = %d body = %s", chat.Code, chat.Body.String())
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte(`"model":"deepseek-v4"`)) {
		t.Fatalf("alias was not resolved to deepseek-v4: %s", chat.Body.String())
	}
}

func TestInvalidManagementStatusIsRejected(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	router := testRouter(t)

	response := perform(router, http.MethodPatch, "/api/channels/chn_1001", `{"status":"weird"}`, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid channel status code = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`validation_error`)) {
		t.Fatalf("missing validation error body: %s", response.Body.String())
	}
}

func TestOpenAICompatibleProviderForwardsRequest(t *testing.T) {
	var upstreamModel string
	var upstreamPath string
	var upstreamAuth string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		upstreamAuth = r.Header.Get("Authorization")

		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		upstreamModel = payload.Model

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_upstream","object":"chat.completion","model":"deepseek-v4","choices":[{"index":0,"message":{"role":"assistant","content":"from upstream"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "compatible",
		"UPSTREAM_API_KEY": "global-secret",
	})
	router := testRouter(t)

	patchBody := `{"baseUrl":"` + upstream.URL + `/v1","upstreamApiKey":"channel-secret"}`
	patched := perform(router, http.MethodPatch, "/api/channels/chn_1002", patchBody, nil)
	if patched.Code != http.StatusOK {
		t.Fatalf("patch channel status = %d body = %s", patched.Code, patched.Body.String())
	}
	if bytes.Contains(patched.Body.Bytes(), []byte(`channel-secret`)) {
		t.Fatal("channel upstream api key leaked in patch response")
	}
	if !bytes.Contains(patched.Body.Bytes(), []byte(`"upstreamKeySet":true`)) {
		t.Fatalf("patch response did not expose upstreamKeySet: %s", patched.Body.String())
	}

	chatBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_live_test"})
	if chat.Code != http.StatusOK {
		t.Fatalf("chat status = %d body = %s", chat.Code, chat.Body.String())
	}
	if upstreamPath != "/v1/chat/completions" {
		t.Fatalf("upstream path = %s", upstreamPath)
	}
	if upstreamAuth != "Bearer channel-secret" {
		t.Fatalf("upstream auth = %s", upstreamAuth)
	}
	if upstreamModel != "deepseek-v4" {
		t.Fatalf("upstream model = %s", upstreamModel)
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte(`from upstream`)) {
		t.Fatalf("upstream response was not returned: %s", chat.Body.String())
	}

	ledger := perform(router, http.MethodGet, "/api/quota-ledger", "", nil)
	if ledger.Code != http.StatusOK {
		t.Fatalf("quota ledger status = %d body = %s", ledger.Code, ledger.Body.String())
	}
	if !bytes.Contains(ledger.Body.Bytes(), []byte(`"amount":-0.0001`)) {
		t.Fatalf("usage-based minimum cost was not recorded: %s", ledger.Body.String())
	}
}

func TestOpenAICompatibleProviderStreamsUpstreamResponse(t *testing.T) {
	var upstreamStream bool

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode upstream stream request: %v", err)
		}
		upstreamStream = payload.Stream

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "compatible",
		"UPSTREAM_API_KEY": "upstream-secret",
	})
	router := testRouter(t)

	patchBody := `{"baseUrl":"` + upstream.URL + `/v1"}`
	patched := perform(router, http.MethodPatch, "/api/channels/chn_1002", patchBody, nil)
	if patched.Code != http.StatusOK {
		t.Fatalf("patch channel status = %d body = %s", patched.Code, patched.Body.String())
	}

	chatBody := `{"model":"ds","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_live_test"})
	if chat.Code != http.StatusOK {
		t.Fatalf("stream chat status = %d body = %s", chat.Code, chat.Body.String())
	}
	if !upstreamStream {
		t.Fatal("upstream request did not include stream=true")
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte("data: [DONE]")) {
		t.Fatalf("stream response was not proxied: %s", chat.Body.String())
	}
}

func TestChannelUpstreamKeyIsEncryptedAtRestAndUsable(t *testing.T) {
	var upstreamAuth string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_encrypted","object":"chat.completion","model":"deepseek-v4","choices":[{"index":0,"message":{"role":"assistant","content":"encrypted ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	dataFile := filepath.Join(t.TempDir(), "state.json")
	withEnv(t, map[string]string{
		"PERSISTENCE":   "file",
		"DATA_FILE":     dataFile,
		"PROVIDER_MODE": "compatible",
		"SECRET_KEY":    "local-test-secret",
	})
	router := testRouter(t)

	plainKey := "channel-encrypted-secret"
	patchBody := `{"baseUrl":"` + upstream.URL + `/v1","upstreamApiKey":"` + plainKey + `"}`
	patched := perform(router, http.MethodPatch, "/api/channels/chn_1002", patchBody, nil)
	if patched.Code != http.StatusOK {
		t.Fatalf("patch channel status = %d body = %s", patched.Code, patched.Body.String())
	}

	stateContent, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if bytes.Contains(stateContent, []byte(plainKey)) {
		t.Fatal("plain upstream key was stored in state file")
	}
	if !bytes.Contains(stateContent, []byte("enc:v1:")) {
		t.Fatalf("encrypted upstream key marker missing from state file: %s", string(stateContent))
	}

	chatBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_live_test"})
	if chat.Code != http.StatusOK {
		t.Fatalf("chat status = %d body = %s", chat.Code, chat.Body.String())
	}
	if upstreamAuth != "Bearer "+plainKey {
		t.Fatalf("upstream auth = %s", upstreamAuth)
	}
}

func TestDiscordOAuthRoleGateCreatesSessionForAdminRoutes(t *testing.T) {
	var tokenForm url.Values
	var memberPath string

	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			tokenForm = r.Form
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"discord-access","token_type":"Bearer","expires_in":3600,"scope":"identify guilds.members.read"}`))
		case "/api/v10/users/@me":
			if r.Header.Get("Authorization") != "Bearer discord-access" {
				t.Fatalf("discord user auth = %s", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"dc_user_1","username":"catie","global_name":"Catie"}`))
		case "/api/v10/users/@me/guilds/guild_1/member":
			memberPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"roles":["role_ok","role_other"]}`))
		default:
			t.Fatalf("unexpected discord path: %s", r.URL.Path)
		}
	}))
	defer discord.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":              "memory",
		"ADMIN_TOKEN":              "admin-secret",
		"DISCORD_CLIENT_ID":        "client-id",
		"DISCORD_CLIENT_SECRET":    "client-secret",
		"DISCORD_REDIRECT_URI":     "http://localhost:8787/api/auth/discord/callback",
		"DISCORD_ALLOWED_GUILD_ID": "guild_1",
		"DISCORD_ALLOWED_ROLE_ID":  "role_ok",
		"DISCORD_OAUTH_BASE":       discord.URL + "/oauth2",
		"DISCORD_API_BASE":         discord.URL + "/api/v10",
		"AUTH_SUCCESS_URL":         "http://localhost:5173/",
	})
	router := testRouter(t)

	start := perform(router, http.MethodGet, "/api/auth/discord/start", "", nil)
	if start.Code != http.StatusFound {
		t.Fatalf("discord start status = %d body = %s", start.Code, start.Body.String())
	}
	location := start.Header().Get("Location")
	if !strings.Contains(location, "guilds.members.read") {
		t.Fatalf("discord auth URL missing guilds.members.read scope: %s", location)
	}
	authURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	state := authURL.Query().Get("state")
	if state == "" {
		t.Fatal("discord auth URL missing state")
	}

	callback := perform(router, http.MethodGet, "/api/auth/discord/callback?code=oauth-code&state="+url.QueryEscape(state), "", nil)
	if callback.Code != http.StatusFound {
		t.Fatalf("discord callback status = %d body = %s", callback.Code, callback.Body.String())
	}
	if tokenForm.Get("code") != "oauth-code" {
		t.Fatalf("token form code = %s", tokenForm.Get("code"))
	}
	if memberPath != "/api/v10/users/@me/guilds/guild_1/member" {
		t.Fatalf("member path = %s", memberPath)
	}
	cookies := callback.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("discord callback did not set session cookie")
	}

	users := perform(router, http.MethodGet, "/api/users", "", map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value})
	if users.Code != http.StatusOK {
		t.Fatalf("session did not authorize admin route: %d body = %s", users.Code, users.Body.String())
	}
}

func TestStaticSPAFallbackDoesNotCaptureAPIRoutes(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html>CatieAPI</html>"), 0644); err != nil {
		t.Fatalf("write static index: %v", err)
	}
	withEnv(t, map[string]string{
		"PERSISTENCE": "memory",
		"STATIC_DIR":  staticDir,
	})
	router := testRouter(t)

	page := perform(router, http.MethodGet, "/console/users", "", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("spa fallback status = %d body = %s", page.Code, page.Body.String())
	}
	if !bytes.Contains(page.Body.Bytes(), []byte("CatieAPI")) {
		t.Fatalf("spa fallback did not serve index: %s", page.Body.String())
	}

	api := perform(router, http.MethodGet, "/api/missing", "", nil)
	if api.Code != http.StatusNotFound {
		t.Fatalf("api missing status = %d body = %s", api.Code, api.Body.String())
	}
	if !bytes.Contains(api.Body.Bytes(), []byte("route_not_found")) {
		t.Fatalf("api missing did not return json error: %s", api.Body.String())
	}
}

func TestOpenAICompatibleRoutesWorkWithoutV1Prefix(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	router := testRouter(t)

	models := perform(router, http.MethodGet, "/models", "", map[string]string{"Authorization": "Bearer cat_live_test"})
	if models.Code != http.StatusOK {
		t.Fatalf("models without v1 status = %d body = %s", models.Code, models.Body.String())
	}
	if !bytes.Contains(models.Body.Bytes(), []byte(`"object":"list"`)) {
		t.Fatalf("models without v1 did not return model list: %s", models.Body.String())
	}

	model := perform(router, http.MethodGet, "/models/ds", "", map[string]string{"Authorization": "Bearer cat_live_test"})
	if model.Code != http.StatusOK {
		t.Fatalf("model without v1 status = %d body = %s", model.Code, model.Body.String())
	}
	if !bytes.Contains(model.Body.Bytes(), []byte(`"id":"deepseek-v4"`)) {
		t.Fatalf("model alias without v1 was not resolved: %s", model.Body.String())
	}

	chatBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_live_test"})
	if chat.Code != http.StatusOK {
		t.Fatalf("chat without v1 status = %d body = %s", chat.Code, chat.Body.String())
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte(`"model":"deepseek-v4"`)) {
		t.Fatalf("chat without v1 did not resolve model: %s", chat.Body.String())
	}
}
