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
	_, router := testServerRouter(t)
	return router
}

func testServerRouter(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	server := NewServer()
	if server.adminToken == "" {
		server.adminToken = "test-admin-token"
	}
	router := gin.New()
	router.Use(server.requestMiddleware())
	router.Use(server.corsMiddleware())
	server.registerRoutes(router)
	return server, router
}

func seedGatewayFixtures(server *Server) {
	server.mu.Lock()
	defer server.mu.Unlock()

	server.state.Users = []User{
		{ID: "usr_1001", Name: "Demo Admin", Email: "demo-admin@example.test", Role: "admin", Status: "active", Balance: 128.5, RequestsToday: 42, TotalRequests: 1380, CreatedAt: "2026-07-01T10:00:00.000Z", LastLoginAt: "2026-07-04T01:10:00.000Z", Note: "内部测试管理员"},
		{ID: "usr_1002", Name: "Demo User", Email: "demo-user@example.test", Role: "user", Status: "active", Balance: 36.2, RequestsToday: 18, TotalRequests: 526, CreatedAt: "2026-07-02T08:30:00.000Z", LastLoginAt: "2026-07-03T21:14:00.000Z", Note: "普通用户"},
		{ID: "usr_1003", Name: "Limited User", Email: "limited-user@example.test", Role: "user", Status: "limited", Balance: 2.4, RequestsToday: 5, TotalRequests: 80, CreatedAt: "2026-07-03T12:20:00.000Z", LastLoginAt: "2026-07-03T23:48:00.000Z", Note: "额度偏低"},
	}
	server.state.APIKeys = []APIKey{
		{ID: "key_1001", UserID: "usr_1001", Name: "Dashboard Key", Prefix: "cat_admin", Hash: hashSecret("cat_fixture_admin_secret"), Status: "active", CreatedAt: "2026-07-01T10:30:00.000Z", LastUsedAt: "2026-07-04T01:20:00.000Z", RequestCount: 910},
		{ID: "key_1002", UserID: "usr_1002", Name: "App Key", Prefix: "cat_live", Hash: hashSecret("cat_fixture_live_secret"), Status: "active", CreatedAt: "2026-07-02T09:12:00.000Z", LastUsedAt: "2026-07-03T21:28:00.000Z", RequestCount: 526},
	}
	server.state.Channels = []Channel{
		{ID: "chn_1001", Name: "OpenAI Compatible", Provider: "openai", BaseURL: "https://upstream-one.example.test/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-5.6", "gpt-5.5"}},
		{ID: "chn_1002", Name: "Backup Provider", Provider: "compatible", BaseURL: "https://upstream-two.example.test/v1", Status: "standby", Priority: 2, Weight: 20, Models: []string{"claude-fable-5", "gemini-3.1", "deepseek-v4"}},
	}
	server.state.Logs = []RequestLog{
		{ID: "req_fixture_1", UserID: stringPtr("usr_1002"), APIKeyPrefix: stringPtr("cat_live"), Model: stringPtr("gpt-5.6"), Channel: stringPtr("OpenAI Compatible"), Status: "success", Cost: 0.04, LatencyMS: 820, CreatedAt: "2026-07-04T01:22:00.000Z"},
		{ID: "req_fixture_2", UserID: stringPtr("usr_1003"), APIKeyPrefix: stringPtr("cat_trial"), Model: stringPtr("deepseek-v4"), Channel: stringPtr("Backup Provider"), Status: "failed", Cost: 0, LatencyMS: 1200, ErrorCode: "upstream_timeout", CreatedAt: "2026-07-04T01:25:00.000Z"},
	}
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
	if os.Getenv("ADMIN_TOKEN") == "" && req.Header.Get("Authorization") == "" && strings.HasPrefix(path, "/api/") {
		req.Header.Set("Authorization", "Bearer test-admin-token")
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

func TestFirstRunSetupLoginRegistrationAndRoleIsolation(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "state.json")
	withEnv(t, map[string]string{
		"ADMIN_TOKEN": "recovery-token",
		"PERSISTENCE": "file",
		"DATA_FILE":   dataFile,
	})
	router := testRouter(t)

	status := perform(router, http.MethodGet, "/api/auth/status", "", nil)
	if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte(`"initialized":false`)) {
		t.Fatalf("initial auth status = %d body = %s", status.Code, status.Body.String())
	}

	setup := perform(router, http.MethodPost, "/api/auth/setup", `{
		"username":"catie",
		"password":"correct-horse-battery",
		"displayName":"Catie",
		"email":"catie@example.com",
		"discordUserId":"100000000000000001"
	}`, nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup status = %d body = %s", setup.Code, setup.Body.String())
	}
	adminCookie := setup.Result().Cookies()[0]
	adminHeaders := map[string]string{"Cookie": adminCookie.Name + "=" + adminCookie.Value}

	protected := perform(router, http.MethodGet, "/api/users", "", adminHeaders)
	if protected.Code != http.StatusOK {
		t.Fatalf("admin session did not authorize management route: %d body = %s", protected.Code, protected.Body.String())
	}
	binding := perform(router, http.MethodPatch, "/api/account/profile", `{"discordUserId":"1446547305208746222"}`, adminHeaders)
	if binding.Code != http.StatusOK || !bytes.Contains(binding.Body.Bytes(), []byte(`"discordUserId":"1446547305208746222"`)) {
		t.Fatalf("Discord account binding status = %d body = %s", binding.Code, binding.Body.String())
	}

	stateContent, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if bytes.Contains(stateContent, []byte("correct-horse-battery")) {
		t.Fatal("plain account password was stored")
	}

	badLogin := perform(router, http.MethodPost, "/api/auth/login", `{"identifier":"catie","password":"wrong-password"}`, nil)
	if badLogin.Code != http.StatusUnauthorized {
		t.Fatalf("bad login status = %d body = %s", badLogin.Code, badLogin.Body.String())
	}
	login := perform(router, http.MethodPost, "/api/auth/login", `{"identifier":"catie","password":"correct-horse-battery"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", login.Code, login.Body.String())
	}
	passwordChange := perform(router, http.MethodPatch, "/api/account/profile", `{
		"currentPassword":"correct-horse-battery",
		"newPassword":"new-correct-password"
	}`, adminHeaders)
	if passwordChange.Code != http.StatusOK {
		t.Fatalf("password change status = %d body = %s", passwordChange.Code, passwordChange.Body.String())
	}
	oldPassword := perform(router, http.MethodPost, "/api/auth/login", `{"identifier":"catie","password":"correct-horse-battery"}`, nil)
	if oldPassword.Code != http.StatusUnauthorized {
		t.Fatalf("old password remained valid: %d body = %s", oldPassword.Code, oldPassword.Body.String())
	}
	newPassword := perform(router, http.MethodPost, "/api/auth/login", `{"identifier":"catie","password":"new-correct-password"}`, nil)
	if newPassword.Code != http.StatusOK {
		t.Fatalf("new password login status = %d body = %s", newPassword.Code, newPassword.Body.String())
	}
	stateContent, err = os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("read updated state file: %v", err)
	}
	if bytes.Contains(stateContent, []byte("new-correct-password")) {
		t.Fatal("new plain account password was stored")
	}

	register := perform(router, http.MethodPost, "/api/auth/register", `{
		"username":"demo_user",
		"password":"another-safe-password",
		"displayName":"Demo User",
		"email":"demo-user@example.test"
	}`, nil)
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d body = %s", register.Code, register.Body.String())
	}
	userCookie := register.Result().Cookies()[0]
	userHeaders := map[string]string{"Cookie": userCookie.Name + "=" + userCookie.Value}
	userProtected := perform(router, http.MethodGet, "/api/users", "", userHeaders)
	if userProtected.Code != http.StatusUnauthorized {
		t.Fatalf("ordinary user reached admin route: %d body = %s", userProtected.Code, userProtected.Body.String())
	}
	account := perform(router, http.MethodGet, "/api/account/me", "", userHeaders)
	if account.Code != http.StatusOK || !bytes.Contains(account.Body.Bytes(), []byte(`"name":"Demo User"`)) {
		t.Fatalf("ordinary user account status = %d body = %s", account.Code, account.Body.String())
	}
	ownKey := perform(router, http.MethodPost, "/api/account/api-keys", `{"name":"Personal Key"}`, userHeaders)
	if ownKey.Code != http.StatusCreated || !bytes.Contains(ownKey.Body.Bytes(), []byte(`"secret":"cat_`)) {
		t.Fatalf("ordinary user key creation status = %d body = %s", ownKey.Code, ownKey.Body.String())
	}

	emailMode := perform(router, http.MethodPatch, "/api/settings/auth", `{"registrationEnabled":true,"registrationMode":"email"}`, adminHeaders)
	if emailMode.Code != http.StatusOK || !bytes.Contains(emailMode.Body.Bytes(), []byte(`"registrationMode":"email"`)) {
		t.Fatalf("email registration mode status = %d body = %s", emailMode.Code, emailMode.Body.String())
	}
	emailRegister := perform(router, http.MethodPost, "/api/auth/register", `{
		"password":"mail-register-password",
		"displayName":"Mail User",
		"email":"mail-user@example.test"
	}`, nil)
	if emailRegister.Code != http.StatusCreated || !bytes.Contains(emailRegister.Body.Bytes(), []byte(`"username":"mail-user"`)) {
		t.Fatalf("email registration status = %d body = %s", emailRegister.Code, emailRegister.Body.String())
	}
	discordMode := perform(router, http.MethodPatch, "/api/settings/auth", `{"registrationEnabled":true,"registrationMode":"discord"}`, adminHeaders)
	if discordMode.Code != http.StatusOK || !bytes.Contains(discordMode.Body.Bytes(), []byte(`"registrationMode":"discord"`)) {
		t.Fatalf("discord registration mode status = %d body = %s", discordMode.Code, discordMode.Body.String())
	}
	passwordInDiscordMode := perform(router, http.MethodPost, "/api/auth/register", `{
		"username":"password_user",
		"password":"another-safe-password"
	}`, nil)
	if passwordInDiscordMode.Code != http.StatusForbidden {
		t.Fatalf("password registration in discord mode status = %d body = %s", passwordInDiscordMode.Code, passwordInDiscordMode.Body.String())
	}

	disableRegistration := perform(router, http.MethodPatch, "/api/settings/auth", `{"registrationEnabled":false}`, adminHeaders)
	if disableRegistration.Code != http.StatusOK {
		t.Fatalf("disable registration status = %d body = %s", disableRegistration.Code, disableRegistration.Body.String())
	}
	blockedRegistration := perform(router, http.MethodPost, "/api/auth/register", `{
		"username":"blocked_user",
		"password":"another-safe-password"
	}`, nil)
	if blockedRegistration.Code != http.StatusForbidden {
		t.Fatalf("disabled registration status = %d body = %s", blockedRegistration.Code, blockedRegistration.Body.String())
	}
}

func TestDemoSeedDataIsRemovedOnLoad(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "state.json")
	stored := defaultState()
	stored.Users = []User{
		{ID: "usr_1001", Name: "林可", Email: "lin@example.com", Role: "admin", Status: "active"},
		{ID: "usr_real", Name: "Real User", Email: "real@example.test", Role: "user", Status: "active"},
	}
	stored.APIKeys = []APIKey{
		{ID: "key_1001", UserID: "usr_1001", Name: "Dashboard Key", Prefix: "cat_admin", Hash: hashSecret("demo"), Status: "active"},
		{ID: "key_real", UserID: "usr_real", Name: "Real Key", Prefix: "cat_real", Hash: hashSecret("real"), Status: "active"},
	}
	stored.Channels = []Channel{
		{ID: "chn_1001", Name: "OpenAI Compatible", Provider: "openai", BaseURL: "https://api.openai.example/v1", Status: "healthy"},
		{ID: "chn_real", Name: "Real Provider", Provider: "compatible", BaseURL: "https://real.example.test/v1", Status: "healthy"},
	}
	stored.Logs = []RequestLog{
		{ID: "req_9001", Status: "success"},
		{ID: "req_real", Status: "success"},
	}
	stored.QuotaLedger = []QuotaEntry{
		{ID: "quota_seed", RequestID: "req_9001"},
		{ID: "quota_real", RequestID: "req_real"},
	}
	stored.Settings.Discord = DiscordSettings{
		Managed:         true,
		Enabled:         true,
		ClientID:        "100000000000000001",
		RedirectURI:     "http://localhost:8787/api/auth/discord/callback",
		AuthSuccessURL:  "https://your-domain.example/",
		SessionTTLHours: 168,
	}
	content, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal stored state: %v", err)
	}
	if err := os.WriteFile(dataFile, content, 0644); err != nil {
		t.Fatalf("write stored state: %v", err)
	}

	withEnv(t, map[string]string{
		"PERSISTENCE": "file",
		"DATA_FILE":   dataFile,
	})
	server, _ := testServerRouter(t)

	if len(server.state.Users) != 1 || server.state.Users[0].ID != "usr_real" {
		t.Fatalf("users after seed cleanup = %#v", server.state.Users)
	}
	if len(server.state.APIKeys) != 1 || server.state.APIKeys[0].ID != "key_real" {
		t.Fatalf("api keys after seed cleanup = %#v", server.state.APIKeys)
	}
	if len(server.state.Channels) != 1 || server.state.Channels[0].ID != "chn_real" {
		t.Fatalf("channels after seed cleanup = %#v", server.state.Channels)
	}
	if len(server.state.Logs) != 1 || server.state.Logs[0].ID != "req_real" {
		t.Fatalf("logs after seed cleanup = %#v", server.state.Logs)
	}
	if len(server.state.QuotaLedger) != 1 || server.state.QuotaLedger[0].ID != "quota_real" {
		t.Fatalf("quota ledger after seed cleanup = %#v", server.state.QuotaLedger)
	}
	if server.state.Settings.Discord.Managed {
		t.Fatalf("incomplete sample Discord settings were not cleared: %#v", server.state.Settings.Discord)
	}
}

func TestCreatedAPIKeyUsesSecretWithoutLeakingHash(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

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
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	response := perform(router, http.MethodPatch, "/api/channels/chn_1001", `{"status":"weird"}`, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid channel status code = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`validation_error`)) {
		t.Fatalf("missing validation error body: %s", response.Body.String())
	}
}

func TestCreateChannelDefaultsToDisabled(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	_, router := testServerRouter(t)

	created := perform(router, http.MethodPost, "/api/channels", `{"name":"Edge Provider"}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create channel status = %d body = %s", created.Code, created.Body.String())
	}
	if !bytes.Contains(created.Body.Bytes(), []byte(`"status":"disabled"`)) {
		t.Fatalf("created channel was not disabled by default: %s", created.Body.String())
	}
	var payload struct {
		Channel Channel `json:"channel"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode created channel: %v", err)
	}

	blocked := perform(router, http.MethodPatch, "/api/channels/"+payload.Channel.ID, `{"status":"healthy"}`, nil)
	if blocked.Code != http.StatusBadRequest {
		t.Fatalf("empty channel enable status = %d body = %s", blocked.Code, blocked.Body.String())
	}

	enabled := perform(router, http.MethodPatch, "/api/channels/"+payload.Channel.ID, `{"baseUrl":"https://edge.example.test/v1","status":"healthy"}`, nil)
	if enabled.Code != http.StatusOK || !bytes.Contains(enabled.Body.Bytes(), []byte(`"status":"healthy"`)) {
		t.Fatalf("configured channel enable status = %d body = %s", enabled.Code, enabled.Body.String())
	}

	channels := perform(router, http.MethodGet, "/api/channels", "", nil)
	if channels.Code != http.StatusOK || !bytes.Contains(channels.Body.Bytes(), []byte("Edge Provider")) {
		t.Fatalf("created channel was not listed: %d body = %s", channels.Code, channels.Body.String())
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
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

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
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
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
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	patchBody := `{"baseUrl":"` + upstream.URL + `/v1"}`
	patched := perform(router, http.MethodPatch, "/api/channels/chn_1002", patchBody, nil)
	if patched.Code != http.StatusOK {
		t.Fatalf("patch channel status = %d body = %s", patched.Code, patched.Body.String())
	}

	chatBody := `{"model":"ds","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
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
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

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
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
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

func TestBoundDiscordIDRestoresLocalAdminAccount(t *testing.T) {
	discordUserID := "100000000000000001"
	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"discord-access","token_type":"Bearer"}`))
		case "/api/v10/users/@me":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"` + discordUserID + `","username":"catie","global_name":"Catie"}`))
		default:
			t.Fatalf("unexpected Discord path for bound account: %s", r.URL.Path)
		}
	}))
	defer discord.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":           "memory",
		"ADMIN_TOKEN":           "recovery-token",
		"DISCORD_CLIENT_ID":     "client-id",
		"DISCORD_CLIENT_SECRET": "client-secret",
		"DISCORD_REDIRECT_URI":  "http://localhost:8787/api/auth/discord/callback",
		"DISCORD_OAUTH_BASE":    discord.URL + "/oauth2",
		"DISCORD_API_BASE":      discord.URL + "/api/v10",
		"AUTH_SUCCESS_URL":      "http://localhost:5173/",
	})
	router := testRouter(t)
	setup := perform(router, http.MethodPost, "/api/auth/setup", `{
		"username":"catie",
		"password":"correct-horse-battery",
		"discordUserId":"`+discordUserID+`"
	}`, nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("bound account setup status = %d body = %s", setup.Code, setup.Body.String())
	}

	start := perform(router, http.MethodGet, "/api/auth/discord/start", "", nil)
	authURL, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Discord authorization URL: %v", err)
	}
	callback := perform(router, http.MethodGet, "/api/auth/discord/callback?code=oauth-code&state="+url.QueryEscape(authURL.Query().Get("state")), "", nil)
	if callback.Code != http.StatusFound {
		t.Fatalf("bound Discord callback status = %d body = %s", callback.Code, callback.Body.String())
	}
	cookies := callback.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("bound Discord callback did not set a session")
	}
	admin := perform(router, http.MethodGet, "/api/users", "", map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value})
	if admin.Code != http.StatusOK {
		t.Fatalf("bound Discord account did not restore admin role: %d body = %s", admin.Code, admin.Body.String())
	}
}

func TestDiscordSettingsCanBeManagedWithoutEnvironmentVariables(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "state.json")
	withEnv(t, map[string]string{
		"PERSISTENCE": "file",
		"DATA_FILE":   dataFile,
		"SECRET_KEY":  "test-encryption-key",
	})
	router := testRouter(t)
	body := `{
		"enabled": true,
		"clientId": "100000000000000001",
		"clientSecret": "discord-secret-value",
		"redirectUri": "https://api.example.com/api/auth/discord/callback",
		"allowedGuildId": "123456789012345678",
		"allowedRoleId": "987654321098765432",
		"authSuccessUrl": "https://api.example.com/",
		"sessionTtlHours": 168
	}`

	saved := perform(router, http.MethodPatch, "/api/settings/discord", body, nil)
	if saved.Code != http.StatusOK {
		t.Fatalf("save Discord settings status = %d body = %s", saved.Code, saved.Body.String())
	}
	if bytes.Contains(saved.Body.Bytes(), []byte("discord-secret-value")) {
		t.Fatal("Discord Client Secret leaked in settings response")
	}
	if !bytes.Contains(saved.Body.Bytes(), []byte(`"clientSecretSet":true`)) {
		t.Fatalf("Discord Client Secret status missing: %s", saved.Body.String())
	}

	stateContent, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if bytes.Contains(stateContent, []byte("discord-secret-value")) {
		t.Fatal("plain Discord Client Secret was stored in state file")
	}
	if !bytes.Contains(stateContent, []byte("enc:v1:")) {
		t.Fatalf("encrypted Discord Client Secret marker missing: %s", string(stateContent))
	}

	reloaded := testRouter(t)
	start := perform(reloaded, http.MethodGet, "/api/auth/discord/start", "", nil)
	if start.Code != http.StatusFound {
		t.Fatalf("persisted Discord settings were not activated: %d body = %s", start.Code, start.Body.String())
	}
	if !strings.Contains(start.Header().Get("Location"), "client_id=100000000000000001") {
		t.Fatalf("Discord authorization URL used the wrong Client ID: %s", start.Header().Get("Location"))
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
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	models := perform(router, http.MethodGet, "/models", "", map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if models.Code != http.StatusOK {
		t.Fatalf("models without v1 status = %d body = %s", models.Code, models.Body.String())
	}
	if !bytes.Contains(models.Body.Bytes(), []byte(`"object":"list"`)) {
		t.Fatalf("models without v1 did not return model list: %s", models.Body.String())
	}

	model := perform(router, http.MethodGet, "/models/ds", "", map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if model.Code != http.StatusOK {
		t.Fatalf("model without v1 status = %d body = %s", model.Code, model.Body.String())
	}
	if !bytes.Contains(model.Body.Bytes(), []byte(`"id":"deepseek-v4"`)) {
		t.Fatalf("model alias without v1 was not resolved: %s", model.Body.String())
	}

	chatBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if chat.Code != http.StatusOK {
		t.Fatalf("chat without v1 status = %d body = %s", chat.Code, chat.Body.String())
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte(`"model":"deepseek-v4"`)) {
		t.Fatalf("chat without v1 did not resolve model: %s", chat.Body.String())
	}
}
