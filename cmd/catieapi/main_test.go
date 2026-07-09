package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func codexChatSSE(text string) string {
	return fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}}\n\ndata: [DONE]\n\n", text)
}

func codexImageSSE(b64 string) string {
	return fmt.Sprintf("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"image_generation_call\",\"result\":%q}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"created_at\":1780000000,\"output\":[]}}\n\ndata: [DONE]\n\n", b64)
}

func codexImageCompletedSSE(b64 string) string {
	return fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1780000000,\"output\":[{\"type\":\"image_generation_call\",\"result\":%q,\"size\":\"1024x1024\"}]}}\n\ndata: [DONE]\n\n", b64)
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
		{ID: "chn_1001", Name: "OpenAI Compatible", Provider: "openai", BaseURL: "https://upstream-one.example.test/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-5.5", "gpt-5.4"}},
		{ID: "chn_1002", Name: "Backup Provider", Provider: "compatible", BaseURL: "https://upstream-two.example.test/v1", Status: "standby", Priority: 2, Weight: 20, Models: []string{"claude-fable-5", "gemini-3.1", "deepseek-v4"}},
	}
	server.state.Models = []Model{
		{ID: "gpt-5.5", Name: "GPT-5.5", Vendor: "OpenAI", Aliases: []string{"gpt", "gpt55"}, Category: "通用", Description: "Test model", Price: "高", InputPricePer1K: 0.03, OutputPricePer1K: 0.06, Context: "长上下文", Status: "available", Recommended: true},
		{ID: "gpt-5.4", Name: "GPT-5.4", Vendor: "OpenAI", Aliases: []string{"gpt54"}, Category: "通用", Description: "Test model", Price: "中", InputPricePer1K: 0.01, OutputPricePer1K: 0.02, Context: "长上下文", Status: "available", Recommended: false},
		{ID: "claude-fable-5", Name: "Claude Fable 5", Vendor: "Claude", Aliases: []string{"f5"}, Category: "写作", Description: "Test model", Price: "中", InputPricePer1K: 0.01, OutputPricePer1K: 0.02, Context: "长上下文", Status: "available", Recommended: true},
		{ID: "gemini-3.1", Name: "Gemini 3.1", Vendor: "Google", Aliases: []string{"gemini"}, Category: "多模态", Description: "Test model", Price: "中", InputPricePer1K: 0.01, OutputPricePer1K: 0.02, Context: "超长上下文", Status: "available", Recommended: false},
		{ID: "deepseek-v4", Name: "DeepSeek V4", Vendor: "DeepSeek", Aliases: []string{"ds", "deepseek"}, Category: "推理", Description: "Test model", Price: "低", InputPricePer1K: 0.002, OutputPricePer1K: 0.004, Context: "长上下文", Status: "available", Recommended: true},
	}
	for i := range server.state.Models {
		server.state.Models[i].PricingConfigured = server.state.Models[i].InputPricePer1K > 0 || server.state.Models[i].OutputPricePer1K > 0
	}
	server.state.Logs = []RequestLog{
		{ID: "req_fixture_1", UserID: stringPtr("usr_1002"), APIKeyPrefix: stringPtr("cat_live"), Model: stringPtr("gpt-5.5"), Channel: stringPtr("OpenAI Compatible"), Status: "success", Cost: 0.04, LatencyMS: 820, CreatedAt: "2026-07-04T01:22:00.000Z"},
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

func TestOverviewUsesClientLocalDayForSuccessRate(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	clientLocation := time.FixedZone("test-client", 8*60*60)
	now := time.Now().In(clientLocation)
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, clientLocation)
	yesterday := today.AddDate(0, 0, -1)

	server.mu.Lock()
	server.state.Logs = []RequestLog{
		{ID: "success-1", Status: "success", CreatedAt: today.Add(-time.Hour).UTC().Format(time.RFC3339Nano)},
		{ID: "success-2", Status: "success", CreatedAt: today.Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)},
		{ID: "failed-1", Status: "failed", CreatedAt: today.Add(-3 * time.Hour).UTC().Format(time.RFC3339Nano)},
		{ID: "old-success", Status: "success", CreatedAt: yesterday.UTC().Format(time.RFC3339Nano)},
	}
	server.mu.Unlock()

	response := perform(router, http.MethodGet, "/api/overview?timezoneOffset=-480", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("overview status = %d body = %s", response.Code, response.Body.String())
	}
	var result struct {
		RequestsToday int `json:"requestsToday"`
		SuccessRate   int `json:"successRate"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if result.RequestsToday != 3 {
		t.Fatalf("requestsToday = %d, want 3", result.RequestsToday)
	}
	if result.SuccessRate != 67 {
		t.Fatalf("successRate = %d, want 67", result.SuccessRate)
	}
}

func TestCORSAllowsConfiguredOriginsAndIgnoresPlaceholder(t *testing.T) {
	withEnv(t, map[string]string{
		"PERSISTENCE": "memory",
		"CORS_ORIGIN": "https://console.example, https://bot.hanbaoyu.ggff.net/",
	})
	_, router := testServerRouter(t)

	allowed := perform(router, http.MethodOptions, "/models", "", map[string]string{
		"Origin":                         "https://bot.hanbaoyu.ggff.net",
		"Access-Control-Request-Method":  http.MethodGet,
		"Access-Control-Request-Headers": "authorization",
	})
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("allowed preflight status = %d", allowed.Code)
	}
	if origin := allowed.Header().Get("Access-Control-Allow-Origin"); origin != "https://bot.hanbaoyu.ggff.net" {
		t.Fatalf("allowed origin = %q", origin)
	}
	if allowed.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("configured origin did not allow credentials")
	}

	blocked := perform(router, http.MethodOptions, "/models", "", map[string]string{
		"Origin":                        "https://unknown.example",
		"Access-Control-Request-Method": http.MethodGet,
	})
	if origin := blocked.Header().Get("Access-Control-Allow-Origin"); origin != "" {
		t.Fatalf("unexpected allowed origin = %q", origin)
	}

	if normalized := normalizeCORSOriginConfig("https://your-domain.example"); normalized != "*" {
		t.Fatalf("placeholder CORS origin normalized to %q", normalized)
	}
}

func TestLogsSupportServerSidePaginationAndFilters(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	firstPage := perform(router, http.MethodGet, "/api/logs?page=1&pageSize=1", "", nil)
	if firstPage.Code != http.StatusOK {
		t.Fatalf("logs status = %d body = %s", firstPage.Code, firstPage.Body.String())
	}
	var pageResult struct {
		Logs     []RequestLog `json:"logs"`
		Total    int          `json:"total"`
		Page     int          `json:"page"`
		PageSize int          `json:"pageSize"`
	}
	if err := json.Unmarshal(firstPage.Body.Bytes(), &pageResult); err != nil {
		t.Fatalf("decode logs page: %v", err)
	}
	if len(pageResult.Logs) != 1 || pageResult.Total != 2 || pageResult.Page != 1 || pageResult.PageSize != 1 {
		t.Fatalf("unexpected logs page: %#v", pageResult)
	}

	failed := perform(router, http.MethodGet, "/api/logs?status=failed&q=timeout", "", nil)
	if failed.Code != http.StatusOK {
		t.Fatalf("filtered logs status = %d body = %s", failed.Code, failed.Body.String())
	}
	var filtered struct {
		Logs  []RequestLog `json:"logs"`
		Total int          `json:"total"`
	}
	if err := json.Unmarshal(failed.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("decode filtered logs: %v", err)
	}
	if filtered.Total != 1 || len(filtered.Logs) != 1 || filtered.Logs[0].ErrorCode != "upstream_timeout" {
		t.Fatalf("unexpected filtered logs: %#v", filtered)
	}
}

func TestMaintenanceSettingsPruneOperationalHistory(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	nowValue := time.Now().UTC()

	server.mu.Lock()
	server.state.Logs = []RequestLog{{ID: "expired", Status: "failed", CreatedAt: nowValue.AddDate(0, 0, -30).Format(time.RFC3339Nano)}}
	for index := 0; index < 101; index++ {
		server.state.Logs = append(server.state.Logs, RequestLog{
			ID:        fmt.Sprintf("recent-%03d", index),
			Status:    "success",
			CreatedAt: nowValue.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano),
		})
		server.state.QuotaLedger = append(server.state.QuotaLedger, QuotaEntry{
			ID:        fmt.Sprintf("quota-%03d", index),
			Amount:    -0.01,
			CreatedAt: nowValue.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano),
		})
	}
	server.mu.Unlock()

	response := perform(router, http.MethodPatch, "/api/settings/maintenance", `{
		"logRetentionDays":7,
		"maxLogs":100,
		"maxQuotaEntries":100
	}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("maintenance status = %d body = %s", response.Code, response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.state.Logs) != 100 || len(server.state.QuotaLedger) != 100 {
		t.Fatalf("retained logs=%d quota=%d", len(server.state.Logs), len(server.state.QuotaLedger))
	}
	for _, log := range server.state.Logs {
		if log.ID == "expired" || log.ID == "recent-000" {
			t.Fatalf("old log was retained: %s", log.ID)
		}
	}
}

func TestRegistrationDefaultBalanceAndBulkUserActions(t *testing.T) {
	withEnv(t, map[string]string{
		"PERSISTENCE": "memory",
	})
	server, router := testServerRouter(t)
	server.mu.Lock()
	server.state.Accounts = []Account{{ID: "acct_admin", UserID: "usr_admin", Username: "admin", Role: "admin", Status: "active"}}
	server.state.Users = []User{
		{ID: "usr_admin", Name: "Admin", Role: "admin", Status: "active"},
		{ID: "usr_existing", Name: "Existing", Role: "user", Status: "active", Balance: 4},
	}
	server.mu.Unlock()

	settings := perform(router, http.MethodPatch, "/api/settings/auth", `{
		"registrationEnabled":true,
		"registrationMode":"username",
		"defaultBalance":25
	}`, nil)
	if settings.Code != http.StatusOK || !bytes.Contains(settings.Body.Bytes(), []byte(`"defaultBalance":25`)) {
		t.Fatalf("auth settings status = %d body = %s", settings.Code, settings.Body.String())
	}

	register := perform(router, http.MethodPost, "/api/auth/register", `{
		"username":"automatic_quota",
		"password":"automatic-quota-password",
		"displayName":"Automatic Quota"
	}`, nil)
	if register.Code != http.StatusCreated {
		t.Fatalf("registration status = %d body = %s", register.Code, register.Body.String())
	}

	server.mu.Lock()
	var registered *User
	for i := range server.state.Users {
		if server.state.Users[i].Name == "Automatic Quota" {
			registered = &server.state.Users[i]
			break
		}
	}
	if registered == nil || registered.Balance != 25 {
		t.Fatalf("registered user balance = %#v", registered)
	}
	registeredID := registered.ID
	if len(server.state.QuotaLedger) != 1 || server.state.QuotaLedger[0].Amount != 25 {
		t.Fatalf("initial quota ledger = %#v", server.state.QuotaLedger)
	}
	server.mu.Unlock()

	bulk := perform(router, http.MethodPost, "/api/users/bulk", fmt.Sprintf(`{
		"userIds":["usr_existing",%q],
		"action":"adjust_balance",
		"amount":6,
		"reason":"测试活动"
	}`, registeredID), nil)
	if bulk.Code != http.StatusOK || !bytes.Contains(bulk.Body.Bytes(), []byte(`"updated":2`)) {
		t.Fatalf("bulk quota status = %d body = %s", bulk.Code, bulk.Body.String())
	}

	atomicFailure := perform(router, http.MethodPost, "/api/users/bulk", fmt.Sprintf(`{
		"userIds":["usr_existing",%q],
		"action":"adjust_balance",
		"amount":-20
	}`, registeredID), nil)
	if atomicFailure.Code != http.StatusBadRequest {
		t.Fatalf("bulk negative balance status = %d body = %s", atomicFailure.Code, atomicFailure.Body.String())
	}
	adminLockout := perform(router, http.MethodPost, "/api/users/bulk", `{
		"userIds":["usr_admin"],
		"action":"set_status",
		"value":"disabled"
	}`, nil)
	if adminLockout.Code != http.StatusBadRequest {
		t.Fatalf("bulk admin lockout status = %d body = %s", adminLockout.Code, adminLockout.Body.String())
	}
	singleAdminLockout := perform(router, http.MethodPatch, "/api/users/usr_admin", `{"status":"disabled"}`, nil)
	if singleAdminLockout.Code != http.StatusBadRequest {
		t.Fatalf("single admin lockout status = %d body = %s", singleAdminLockout.Code, singleAdminLockout.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.findUser("usr_existing").Balance != 10 || server.findUser(registeredID).Balance != 31 {
		t.Fatalf("bulk balances existing=%v registered=%v", server.findUser("usr_existing").Balance, server.findUser(registeredID).Balance)
	}
	if len(server.state.QuotaLedger) != 3 {
		t.Fatalf("quota ledger entries = %d", len(server.state.QuotaLedger))
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
		"discordUserId":"100000000000000001",
		"registrationEnabled":true,
		"registrationMode":"email",
		"defaultBalance":12.5
	}`, nil)
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup status = %d body = %s", setup.Code, setup.Body.String())
	}
	setupStatus := perform(router, http.MethodGet, "/api/auth/status", "", nil)
	if setupStatus.Code != http.StatusOK ||
		!bytes.Contains(setupStatus.Body.Bytes(), []byte(`"registrationEnabled":true`)) ||
		!bytes.Contains(setupStatus.Body.Bytes(), []byte(`"registrationMode":"email"`)) {
		t.Fatalf("setup auth settings status = %d body = %s", setupStatus.Code, setupStatus.Body.String())
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
	stored.Models = []Model{
		{ID: "gpt-5.4", Name: "GPT-5.4", Vendor: "OpenAI", Status: "available"},
		{ID: "gpt-5.6", Name: "GPT-5.6", Vendor: "OpenAI", Status: "available"},
		{ID: "real-model", Name: "Real Model", Vendor: "Custom", Status: "available"},
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
	server, router := testServerRouter(t)

	if len(server.state.Users) != 1 || server.state.Users[0].ID != "usr_real" {
		t.Fatalf("users after seed cleanup = %#v", server.state.Users)
	}
	if len(server.state.APIKeys) != 1 || server.state.APIKeys[0].ID != "key_real" {
		t.Fatalf("api keys after seed cleanup = %#v", server.state.APIKeys)
	}
	if len(server.state.Channels) != 1 || server.state.Channels[0].ID != "chn_real" {
		t.Fatalf("channels after seed cleanup = %#v", server.state.Channels)
	}
	if server.state.Channels[0].Models == nil {
		t.Fatal("legacy null channel models were not normalized")
	}
	if len(server.state.Models) != 1 || server.state.Models[0].ID != "real-model" {
		t.Fatalf("models after seed cleanup = %#v", server.state.Models)
	}
	if server.state.Models[0].Aliases == nil {
		t.Fatal("legacy null model aliases were not normalized")
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
	modelsResponse := perform(router, http.MethodGet, "/api/models", "", nil)
	if modelsResponse.Code != http.StatusOK || !bytes.Contains(modelsResponse.Body.Bytes(), []byte(`"aliases":[]`)) {
		t.Fatalf("normalized models response = %d body = %s", modelsResponse.Code, modelsResponse.Body.String())
	}
}

func TestDefaultStateStartsWithoutModels(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	_, router := testServerRouter(t)

	response := perform(router, http.MethodGet, "/api/models", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("models status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"models":[]`)) {
		t.Fatalf("default models were not empty: %s", response.Body.String())
	}
}

func TestDeleteModelCleansReferences(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	server.mu.Lock()
	server.state.APIKeys[1].AllowedModels = []string{"deepseek-v4", "gpt-5.5"}
	server.mu.Unlock()

	response := perform(router, http.MethodDelete, "/api/models/deepseek-v4", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("delete model status = %d body = %s", response.Code, response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.findModel("deepseek-v4") != nil {
		t.Fatal("deleted model still exists")
	}
	for _, channel := range server.state.Channels {
		for _, modelID := range channel.Models {
			if modelID == "deepseek-v4" {
				t.Fatalf("deleted model still referenced by channel %#v", channel)
			}
		}
	}
	for _, apiKey := range server.state.APIKeys {
		for _, modelID := range apiKey.AllowedModels {
			if modelID == "deepseek-v4" {
				t.Fatalf("deleted model still referenced by api key %#v", apiKey)
			}
		}
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

func TestAPIKeyAllowedModelsRestrictsChat(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	created := perform(router, http.MethodPost, "/api/users/usr_1002/api-keys", `{"name":"Scoped","allowedModels":["ds"]}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create scoped key status = %d body = %s", created.Code, created.Body.String())
	}
	var payload struct {
		Secret string       `json:"secret"`
		APIKey PublicAPIKey `json:"apiKey"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode scoped key response: %v", err)
	}
	if len(payload.APIKey.AllowedModels) != 1 || payload.APIKey.AllowedModels[0] != "deepseek-v4" {
		t.Fatalf("allowed models were not canonicalized: %#v", payload.APIKey.AllowedModels)
	}

	body := `{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer " + payload.Secret})
	if response.Code != http.StatusForbidden {
		t.Fatalf("restricted model status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`model_not_allowed`)) {
		t.Fatalf("restricted model error not returned: %s", response.Body.String())
	}
}

func TestUpdatedAPIKeyAllowedModelsRestrictsChat(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	created := perform(router, http.MethodPost, "/api/users/usr_1002/api-keys", `{"name":"Manual Scope"}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create key status = %d body = %s", created.Code, created.Body.String())
	}
	var payload struct {
		Secret string       `json:"secret"`
		APIKey PublicAPIKey `json:"apiKey"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode key response: %v", err)
	}

	updated := perform(router, http.MethodPatch, "/api/api-keys/"+payload.APIKey.ID, `{"allowedModels":["DS"]}`, nil)
	if updated.Code != http.StatusOK {
		t.Fatalf("update key status = %d body = %s", updated.Code, updated.Body.String())
	}
	if !bytes.Contains(updated.Body.Bytes(), []byte(`"allowedModels":["deepseek-v4"]`)) {
		t.Fatalf("updated key did not save canonical allowed model: %s", updated.Body.String())
	}

	blockedBody := `{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}]}`
	blocked := perform(router, http.MethodPost, "/v1/chat/completions", blockedBody, map[string]string{"Authorization": "Bearer " + payload.Secret})
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("manually restricted key status = %d body = %s", blocked.Code, blocked.Body.String())
	}

	allowedBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	allowed := perform(router, http.MethodPost, "/v1/chat/completions", allowedBody, map[string]string{"Authorization": "Bearer " + payload.Secret})
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed model status = %d body = %s", allowed.Code, allowed.Body.String())
	}
}

func TestOpenAIModelsAreFilteredByAPIKeyAllowedModels(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	created := perform(router, http.MethodPost, "/api/users/usr_1002/api-keys", `{"name":"Model List Scope","allowedModels":["ds"]}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create scoped key status = %d body = %s", created.Code, created.Body.String())
	}
	var payload struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode scoped key response: %v", err)
	}

	models := perform(router, http.MethodGet, "/models", "", map[string]string{"Authorization": "Bearer " + payload.Secret})
	if models.Code != http.StatusOK {
		t.Fatalf("filtered models status = %d body = %s", models.Code, models.Body.String())
	}
	if !bytes.Contains(models.Body.Bytes(), []byte(`"id":"deepseek-v4"`)) {
		t.Fatalf("filtered model list missing allowed model: %s", models.Body.String())
	}
	if bytes.Contains(models.Body.Bytes(), []byte(`"id":"gpt-5.5"`)) {
		t.Fatalf("filtered model list included disallowed model: %s", models.Body.String())
	}

	allowed := perform(router, http.MethodGet, "/models/ds", "", map[string]string{"Authorization": "Bearer " + payload.Secret})
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed model detail status = %d body = %s", allowed.Code, allowed.Body.String())
	}
	blocked := perform(router, http.MethodGet, "/models/gpt-5.5", "", map[string]string{"Authorization": "Bearer " + payload.Secret})
	if blocked.Code != http.StatusNotFound {
		t.Fatalf("disallowed model detail status = %d body = %s", blocked.Code, blocked.Body.String())
	}
}

func TestAPIKeyRateLimitOverride(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	created := perform(router, http.MethodPost, "/api/users/usr_1002/api-keys", `{"name":"Limited","rateLimitPerMinute":1}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create limited key status = %d body = %s", created.Code, created.Body.String())
	}
	var payload struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode limited key response: %v", err)
	}
	body := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	first := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer " + payload.Secret})
	if first.Code != http.StatusOK {
		t.Fatalf("first rate-limited request status = %d body = %s", first.Code, first.Body.String())
	}
	second := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer " + payload.Secret})
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second rate-limited request status = %d body = %s", second.Code, second.Body.String())
	}
}

func TestExpiredAPIKeyIsRejected(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.APIKeys[1].ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	server.mu.Unlock()

	body := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired key status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestXAPIKeyHeaderIsAccepted(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	chatBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/chat/completions", chatBody, map[string]string{"X-API-Key": "cat_fixture_live_secret"})
	if chat.Code != http.StatusOK {
		t.Fatalf("chat with x-api-key status = %d body = %s", chat.Code, chat.Body.String())
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte(`"model":"deepseek-v4"`)) {
		t.Fatalf("x-api-key request did not resolve model: %s", chat.Body.String())
	}
}

func TestInvalidAPIKeyLogKeepsRequestedModel(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	body := `{"model":"gcli-gemini-2.5-pro","messages":[{"role":"user","content":"hello"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer cat_wrong"})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid key status = %d body = %s", response.Code, response.Body.String())
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	last := server.state.Logs[len(server.state.Logs)-1]
	if last.ErrorCode != "invalid_api_key" {
		t.Fatalf("invalid key log error = %#v", last)
	}
	if last.Model == nil || *last.Model != "gcli-gemini-2.5-pro" {
		t.Fatalf("invalid key log did not keep requested model: %#v", last)
	}
}

func TestLegacyCompletionsEndpointUsesChatFlow(t *testing.T) {
	var upstreamPayload map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_completion","object":"chat.completion","model":"deepseek-v4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "compatible",
		"UPSTREAM_API_KEY": "upstream-secret",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.findChannel("chn_1002").BaseURL = upstream.URL + "/v1"
	server.mu.Unlock()

	body := `{"model":"ds","prompt":["hello","world"],"temperature":0.2}`
	response := perform(router, http.MethodPost, "/v1/completions", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("legacy completions status = %d body = %s", response.Code, response.Body.String())
	}
	if upstreamPayload["prompt"] != nil {
		t.Fatalf("legacy prompt leaked to chat upstream: %#v", upstreamPayload)
	}
	if upstreamPayload["temperature"] != 0.2 {
		t.Fatalf("legacy completion options were not forwarded: %#v", upstreamPayload)
	}
	messages, ok := upstreamPayload["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("legacy prompt was not converted to messages: %#v", upstreamPayload["messages"])
	}
}

func TestResponsesEndpointUsesChatFlow(t *testing.T) {
	var upstreamPayload map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_response","object":"chat.completion","model":"deepseek-v4","choices":[{"index":0,"message":{"role":"assistant","content":"response ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "compatible",
		"UPSTREAM_API_KEY": "upstream-secret",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.findChannel("chn_1002").BaseURL = upstream.URL + "/v1"
	server.mu.Unlock()

	body := `{"model":"ds","instructions":"be concise","input":"hello"}`
	response := perform(router, http.MethodPost, "/v1/responses", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("responses status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"object":"response"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"output_text":"response ok"`)) {
		t.Fatalf("responses body was not response-shaped: %s", response.Body.String())
	}
	messages, ok := upstreamPayload["messages"].([]interface{})
	if !ok || len(messages) != 2 {
		t.Fatalf("responses input was not converted to system/user messages: %#v", upstreamPayload["messages"])
	}
	if upstreamPayload["input"] != nil || upstreamPayload["instructions"] != nil {
		t.Fatalf("responses-only fields leaked to chat upstream: %#v", upstreamPayload)
	}
}

func TestOpenAIAccountPoolChatUsesChatGPTCodexResponses(t *testing.T) {
	var upstreamPayload map[string]interface{}
	var upstreamAuth string
	var upstreamPath string
	var upstreamBeta string
	var upstreamOriginator string
	var upstreamAccountID string
	var upstreamUserAgent string
	var upstreamVersion string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		upstreamPath = r.URL.Path
		upstreamBeta = r.Header.Get("OpenAI-Beta")
		upstreamOriginator = r.Header.Get("Originator")
		upstreamAccountID = r.Header.Get("Chatgpt-Account-Id")
		upstreamUserAgent = r.Header.Get("User-Agent")
		upstreamVersion = r.Header.Get("Version")
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode codex request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexChatSSE("hello from codex")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	channel := server.findChannel("chn_1001")
	channel.BaseURL = "https://api.openai.com/v1"
	channel.OpenAIAccounts = []OpenAIAccount{
		{ID: "oaiacc_chat", Email: "chat@example.com", AccessToken: "oauth-token", AccountID: "chatgpt-account", Status: "healthy"},
	}
	server.mu.Unlock()

	body := `{"model":"gpt-5.5","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("chat status = %d body = %s", response.Code, response.Body.String())
	}
	if upstreamPath != "/backend-api/codex/responses" || upstreamAuth != "Bearer oauth-token" {
		t.Fatalf("unexpected codex upstream path/auth = %s %s", upstreamPath, upstreamAuth)
	}
	if upstreamBeta != "" || upstreamOriginator != chatGPTCodexTUIProfile || upstreamAccountID != "chatgpt-account" ||
		upstreamUserAgent != chatGPTCodexTUIUserAgent || upstreamVersion != chatGPTCodexTUIVersion {
		t.Fatalf("missing codex headers beta=%s originator=%s account=%s ua=%s version=%s", upstreamBeta, upstreamOriginator, upstreamAccountID, upstreamUserAgent, upstreamVersion)
	}
	if upstreamPayload["model"] != "gpt-5.5" || upstreamPayload["stream"] != true || upstreamPayload["store"] != false {
		t.Fatalf("unexpected codex payload = %#v", upstreamPayload)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"content":"hello from codex"`)) {
		t.Fatalf("chat response was not converted: %s", response.Body.String())
	}
}

func TestOpenAIAccountPoolChatUsesImportedClientProfile(t *testing.T) {
	var upstreamOriginator string
	var upstreamUserAgent string
	var upstreamVersion string
	var upstreamSessionID string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamOriginator = r.Header.Get("Originator")
		upstreamUserAgent = r.Header.Get("User-Agent")
		upstreamVersion = r.Header.Get("Version")
		upstreamSessionID = r.Header.Get("Session_id")
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexChatSSE("hello from imported profile")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	channel := server.findChannel("chn_1001")
	channel.OpenAIAccounts = []OpenAIAccount{
		{ID: "oaiacc_cpa", Email: "cpa@example.com", AccessToken: "oauth-token", Source: "cpa", Status: "healthy"},
	}
	server.mu.Unlock()

	body := `{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("chat status = %d body = %s", response.Code, response.Body.String())
	}
	if upstreamOriginator != chatGPTCodexTUIProfile || upstreamUserAgent != chatGPTCodexTUIUserAgent || upstreamVersion != chatGPTCodexTUIVersion || upstreamSessionID == "" {
		t.Fatalf("unexpected imported codex profile originator=%s ua=%s version=%s", upstreamOriginator, upstreamUserAgent, upstreamVersion)
	}
}

func TestOpenAIAccountPoolChatRetriesNextAccountOnUsageLimit(t *testing.T) {
	authHeaders := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		authHeaders = append(authHeaders, auth)
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		if auth == "Bearer limited-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"upstream_error","message":"The usage limit has been reached","param":"model","type":"usage_limit_reached"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexChatSSE("hello after quota retry")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	channel := server.findChannel("chn_1001")
	channel.BaseURL = "https://api.openai.com/v1"
	channel.OpenAIAccounts = []OpenAIAccount{
		{ID: "oaiacc_limited", Email: "limited@example.com", AccessToken: "limited-token", Status: "healthy"},
		{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy"},
	}
	server.mu.Unlock()

	body := `{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}]}`
	response := perform(router, http.MethodPost, "/v1/chat/completions", body, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("chat status = %d body = %s", response.Code, response.Body.String())
	}
	if len(authHeaders) != 2 || authHeaders[0] != "Bearer limited-token" || authHeaders[1] != "Bearer good-token" {
		t.Fatalf("upstream auth sequence = %#v", authHeaders)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"content":"hello after quota retry"`)) {
		t.Fatalf("chat response was not retried successfully: %s", response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel = server.findChannel("chn_1001")
	if channel.OpenAIAccounts[0].Status != "unchecked" || !strings.Contains(channel.OpenAIAccounts[0].LastError, "usage limit") {
		t.Fatalf("limited account was not marked unchecked: %#v", channel.OpenAIAccounts[0])
	}
	if channel.OpenAIAccounts[1].Status != "healthy" {
		t.Fatalf("good account status changed: %#v", channel.OpenAIAccounts[1])
	}
}

func TestEmbeddingsEndpointUsesGatewayAuthentication(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	_, router := testServerRouter(t)

	response := perform(router, http.MethodPost, "/v1/embeddings", `{"model":"embedding","input":"hello"}`, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("embeddings status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`invalid_api_key`)) {
		t.Fatalf("embeddings did not use gateway authentication: %s", response.Body.String())
	}
}

func TestEmbeddingsEndpointForwardsToCompatibleUpstream(t *testing.T) {
	var upstreamPayload map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("embeddings upstream path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer embedding-secret" {
			t.Fatalf("embeddings upstream authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode embedding request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.1,0.2],"index":0}],"model":"deepseek-v4","usage":{"prompt_tokens":2,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{"PERSISTENCE": "memory", "PROVIDER_MODE": "compatible", "UPSTREAM_API_KEY": "moderation-secret"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	channel := server.findChannel("chn_1002")
	channel.BaseURL = upstream.URL + "/v1"
	channel.UpstreamAPIKey = "embedding-secret"
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/embeddings", `{"model":"ds","input":["hello","world"],"encoding_format":"float"}`, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK {
		t.Fatalf("embeddings status = %d body = %s", response.Code, response.Body.String())
	}
	if upstreamPayload["model"] != "deepseek-v4" || upstreamPayload["encoding_format"] != "float" {
		t.Fatalf("embedding payload was not forwarded: %#v", upstreamPayload)
	}
}

func TestAudioSpeechForwardsBinaryResponse(t *testing.T) {
	var upstreamPayload map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Fatalf("audio upstream path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode audio request: %v", err)
		}
		w.Header().Set("Content-Type", "audio/ogg")
		_, _ = w.Write([]byte("OggS-audio"))
	}))
	defer upstream.Close()
	withEnv(t, map[string]string{"PERSISTENCE": "memory", "PROVIDER_MODE": "compatible", "UPSTREAM_API_KEY": "moderation-secret"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	channel := server.findChannel("chn_1002")
	channel.BaseURL = upstream.URL + "/v1"
	channel.UpstreamAPIKey = "audio-secret"
	server.mu.Unlock()
	response := perform(router, http.MethodPost, "/v1/audio/speech", `{"model":"ds","input":"hello","voice":"alloy","response_format":"opus"}`, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "audio/ogg" || response.Body.String() != "OggS-audio" {
		t.Fatalf("audio response = %d %s %q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	if upstreamPayload["model"] != "deepseek-v4" || upstreamPayload["voice"] != "alloy" || upstreamPayload["response_format"] != "opus" {
		t.Fatalf("audio payload was not forwarded: %#v", upstreamPayload)
	}
}

func TestModerationsForwardsToCompatibleUpstream(t *testing.T) {
	var payload map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/moderations" {
			t.Fatalf("moderation upstream path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode moderation request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"modr_1","model":"omni-moderation-latest","results":[{"flagged":false}]}`))
	}))
	defer upstream.Close()
	withEnv(t, map[string]string{"PERSISTENCE": "memory", "PROVIDER_MODE": "compatible", "UPSTREAM_API_KEY": "moderation-secret"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	channel := server.findChannel("chn_1002")
	channel.BaseURL = upstream.URL + "/v1"
	channel.UpstreamAPIKey = "moderation-secret"
	server.mu.Unlock()
	response := perform(router, http.MethodPost, "/v1/moderations", `{"input":"hello","model":"omni-moderation-latest"}`, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"flagged":false`)) {
		t.Fatalf("moderation response = %d %s", response.Code, response.Body.String())
	}
	if payload["input"] != "hello" {
		t.Fatalf("moderation payload was not forwarded: %#v", payload)
	}
}

func TestUnpricedModelAllowsZeroBalance(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.findUser("usr_1002").Balance = 0
	model := server.findModel("deepseek-v4")
	model.InputPricePer1K = 0
	model.OutputPricePer1K = 0
	model.PricingConfigured = false
	server.mu.Unlock()

	body := `{"model":"ds","messages":[{"role":"user","content":"free call"}]}`
	response := perform(router, http.MethodPost, "/chat/completions", body, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("free model with zero balance status = %d body = %s", response.Code, response.Body.String())
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.findUser("usr_1002").Balance != 0 {
		t.Fatalf("free model changed balance to %v", server.findUser("usr_1002").Balance)
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

func TestCPAChannelSyncsModelsThroughCompatibleUpstream(t *testing.T) {
	var upstreamAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/models" {
			t.Fatalf("CPA upstream path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5.6-sol"},{"id":"claude-sonnet-4"}]}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	_, router := testServerRouter(t)

	body := `{"name":"CPA Local","provider":"cpa","baseUrl":"` + upstream.URL + `/v1","upstreamApiKey":"cpa-secret"}`
	created := perform(router, http.MethodPost, "/api/channels", body, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create CPA channel status = %d body = %s", created.Code, created.Body.String())
	}
	var payload struct {
		Channel PublicChannel `json:"channel"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode CPA channel: %v", err)
	}
	if payload.Channel.Provider != "cpa" || payload.Channel.UpstreamKeySet != true {
		t.Fatalf("CPA channel not stored correctly: %#v", payload.Channel)
	}

	synced := perform(router, http.MethodPost, "/api/channels/"+payload.Channel.ID+"/sync-models", `{}`, nil)
	if synced.Code != http.StatusOK {
		t.Fatalf("sync CPA models status = %d body = %s", synced.Code, synced.Body.String())
	}
	if upstreamAuth != "Bearer cpa-secret" {
		t.Fatalf("CPA upstream auth = %s", upstreamAuth)
	}
	if !bytes.Contains(synced.Body.Bytes(), []byte(`gpt-5.6-sol`)) || !bytes.Contains(synced.Body.Bytes(), []byte(`claude-sonnet-4`)) {
		t.Fatalf("sync CPA models response missing model: %s", synced.Body.String())
	}
}

func TestCodexChannelModelIDsIncludeGPT56Family(t *testing.T) {
	for _, modelID := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		if !containsString(codexChannelModelIDs(), modelID) {
			t.Fatalf("codexChannelModelIDs missing %s: %#v", modelID, codexChannelModelIDs())
		}
	}
}

func TestProviderLabelSupportsCPA(t *testing.T) {
	if got := providerLabel("cpa"); got != "CLIProxyAPI" {
		t.Fatalf("providerLabel(cpa) = %q", got)
	}
	if got := providerLabel("CLIProxyAPI"); got != "CLIProxyAPI" {
		t.Fatalf("providerLabel(CLIProxyAPI) = %q", got)
	}
}

func TestSyncChannelModelsPullsFromUpstream(t *testing.T) {
	var upstreamAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/models" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"provider-model-a"},{"id":"provider-model-b"}]}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	_, router := testServerRouter(t)

	created := perform(router, http.MethodPost, "/api/channels", `{"name":"Upstream","baseUrl":"`+upstream.URL+`/v1","upstreamApiKey":"sync-secret","models":["stale-provider-model"]}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create channel status = %d body = %s", created.Code, created.Body.String())
	}
	var payload struct {
		Channel PublicChannel `json:"channel"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode channel: %v", err)
	}

	synced := perform(router, http.MethodPost, "/api/channels/"+payload.Channel.ID+"/sync-models", `{}`, nil)
	if synced.Code != http.StatusOK {
		t.Fatalf("sync models status = %d body = %s", synced.Code, synced.Body.String())
	}
	if upstreamAuth != "Bearer sync-secret" {
		t.Fatalf("upstream auth = %s", upstreamAuth)
	}
	if !bytes.Contains(synced.Body.Bytes(), []byte(`provider-model-a`)) || !bytes.Contains(synced.Body.Bytes(), []byte(`provider-model-b`)) {
		t.Fatalf("sync models response missing upstream ids: %s", synced.Body.String())
	}
	if !bytes.Contains(synced.Body.Bytes(), []byte(`stale-provider-model`)) {
		t.Fatalf("sync models response missing removed stale model: %s", synced.Body.String())
	}
	models := perform(router, http.MethodGet, "/api/models", "", nil)
	if !bytes.Contains(models.Body.Bytes(), []byte(`"id":"provider-model-a"`)) {
		t.Fatalf("synced model was not created: %s", models.Body.String())
	}
	if bytes.Contains(models.Body.Bytes(), []byte(`"id":"stale-provider-model"`)) {
		t.Fatalf("stale synced model was not pruned: %s", models.Body.String())
	}
	channels := perform(router, http.MethodGet, "/api/channels", "", nil)
	if bytes.Contains(channels.Body.Bytes(), []byte(`stale-provider-model`)) {
		t.Fatalf("stale synced model was still attached to channel: %s", channels.Body.String())
	}
}

func TestDeleteChannelPrunesImportedModels(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)

	created := perform(router, http.MethodPost, "/api/channels", `{"name":"Temporary","baseUrl":"https://temporary.example.test/v1","models":["temporary-model"]}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create channel status = %d body = %s", created.Code, created.Body.String())
	}
	var payload struct {
		Channel PublicChannel `json:"channel"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode channel: %v", err)
	}

	server.mu.Lock()
	server.state.APIKeys = append(server.state.APIKeys, APIKey{ID: "key_temp", UserID: "usr_temp", AllowedModels: []string{"temporary-model"}})
	server.mu.Unlock()

	deleted := perform(router, http.MethodDelete, "/api/channels/"+payload.Channel.ID, "", nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete channel status = %d body = %s", deleted.Code, deleted.Body.String())
	}
	if !bytes.Contains(deleted.Body.Bytes(), []byte(`temporary-model`)) {
		t.Fatalf("delete channel response missing removed model: %s", deleted.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.findModel("temporary-model") != nil {
		t.Fatal("temporary model was not pruned")
	}
	for _, apiKey := range server.state.APIKeys {
		if containsString(apiKey.AllowedModels, "temporary-model") {
			t.Fatalf("pruned model still referenced by api key %#v", apiKey)
		}
	}
}

func TestCheckChannelUpdatesHealthState(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"provider-model-a"}]}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	_, router := testServerRouter(t)

	created := perform(router, http.MethodPost, "/api/channels", `{"name":"Health","baseUrl":"`+upstream.URL+`/v1","status":"healthy"}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create channel status = %d body = %s", created.Code, created.Body.String())
	}
	var payload struct {
		Channel PublicChannel `json:"channel"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode channel: %v", err)
	}
	enabled := perform(router, http.MethodPatch, "/api/channels/"+payload.Channel.ID, `{"status":"healthy"}`, nil)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable channel status = %d body = %s", enabled.Code, enabled.Body.String())
	}
	checked := perform(router, http.MethodPost, "/api/channels/"+payload.Channel.ID+"/check", `{}`, nil)
	if checked.Code != http.StatusOK {
		t.Fatalf("check channel status = %d body = %s", checked.Code, checked.Body.String())
	}
	if !bytes.Contains(checked.Body.Bytes(), []byte(`"ok":true`)) || !bytes.Contains(checked.Body.Bytes(), []byte(`"lastCheckedAt"`)) {
		t.Fatalf("check channel response missing health state: %s", checked.Body.String())
	}

	upstream.Close()
	failed := perform(router, http.MethodPost, "/api/channels/"+payload.Channel.ID+"/check", `{}`, nil)
	if failed.Code != http.StatusBadGateway {
		t.Fatalf("failed check status = %d body = %s", failed.Code, failed.Body.String())
	}
	if !bytes.Contains(failed.Body.Bytes(), []byte(`"status":"standby"`)) || !bytes.Contains(failed.Body.Bytes(), []byte(`"lastError"`)) {
		t.Fatalf("failed check did not mark channel standby with error: %s", failed.Body.String())
	}
}

func TestOpenAICompatibleProviderForwardsRequest(t *testing.T) {
	var upstreamModel string
	var upstreamPath string
	var upstreamAuth string
	var upstreamPayload map[string]interface{}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		upstreamAuth = r.Header.Get("Authorization")

		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		upstreamModel, _ = upstreamPayload["model"].(string)

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

	patchBody := `{"baseUrl":"` + upstream.URL + `/v1","upstreamApiKey":"channel-secret","inputPricePer1K":1000,"outputPricePer1K":2000}`
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
	if !bytes.Contains(patched.Body.Bytes(), []byte(`"pricingConfigured":true`)) {
		t.Fatalf("patch response did not expose channel pricing: %s", patched.Body.String())
	}

	chatBody := `{"model":"ds","temperature":0.25,"tools":[{"type":"function","function":{"name":"lookup"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
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
	if upstreamPayload["temperature"] != 0.25 || upstreamPayload["tools"] == nil {
		t.Fatalf("OpenAI request fields were not forwarded: %#v", upstreamPayload)
	}
	messages, ok := upstreamPayload["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("multimodal messages were not forwarded: %#v", upstreamPayload["messages"])
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte(`from upstream`)) {
		t.Fatalf("upstream response was not returned: %s", chat.Body.String())
	}

	ledger := perform(router, http.MethodGet, "/api/quota-ledger", "", nil)
	if ledger.Code != http.StatusOK {
		t.Fatalf("quota ledger status = %d body = %s", ledger.Code, ledger.Body.String())
	}
	if !bytes.Contains(ledger.Body.Bytes(), []byte(`"amount":-3`)) {
		t.Fatalf("channel usage price was not recorded: %s", ledger.Body.String())
	}
}

func TestOpenAICompatibleProviderAcceptsCompleteChatEndpointBaseURL(t *testing.T) {
	var upstreamPath string
	upstreamCalls := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		upstreamPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_complete","object":"chat.completion","model":"deepseek-v4","choices":[{"index":0,"message":{"role":"assistant","content":"complete endpoint ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "compatible",
		"UPSTREAM_API_KEY": "upstream-secret",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.findChannel("chn_1002").BaseURL = upstream.URL + "/api/coding/v3/chat/completions"
	server.mu.Unlock()

	chatBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if chat.Code != http.StatusOK {
		t.Fatalf("chat status = %d body = %s", chat.Code, chat.Body.String())
	}
	if upstreamCalls != 1 || upstreamPath != "/api/coding/v3/chat/completions" {
		t.Fatalf("upstream calls=%d path=%s", upstreamCalls, upstreamPath)
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte(`complete endpoint ok`)) {
		t.Fatalf("complete endpoint response was not returned: %s", chat.Body.String())
	}
}

func TestGatewayFailsOverToNextChannelOnRetryableError(t *testing.T) {
	firstCalls := 0
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"upstream_busy","message":"temporarily unavailable","type":"api_error"}}`))
	}))
	defer first.Close()

	secondCalls := 0
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_fallback","object":"chat.completion","model":"gpt-5.5","choices":[{"index":0,"message":{"role":"assistant","content":"fallback ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer second.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "compatible",
		"UPSTREAM_API_KEY": "test-upstream-key",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Channels = []Channel{
		{ID: "chn_primary", Name: "Primary", BaseURL: first.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-5.5"}},
		{ID: "chn_fallback", Name: "Fallback", BaseURL: second.URL + "/v1", Status: "standby", Priority: 2, Weight: 100, Models: []string{"gpt-5.5"}},
	}
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}]}`, map[string]string{
		"Authorization": "Bearer cat_fixture_admin_secret",
	})
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`fallback ok`)) {
		t.Fatalf("fallback response status = %d body = %s", response.Code, response.Body.String())
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("channel calls primary=%d fallback=%d", firstCalls, secondCalls)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.findChannel("chn_primary").Status != "standby" || server.findChannel("chn_fallback").Status != "healthy" {
		t.Fatalf("channel health primary=%s fallback=%s", server.findChannel("chn_primary").Status, server.findChannel("chn_fallback").Status)
	}
	lastLog := server.state.Logs[len(server.state.Logs)-1]
	if lastLog.Status != "success" || stringValue(lastLog.Channel) != "Fallback" {
		t.Fatalf("fallback log = %#v", lastLog)
	}
}

func TestChannelKeyForwardsWithoutGlobalCompatibleMode(t *testing.T) {
	var upstreamCalled bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer channel-secret" {
			t.Fatalf("upstream auth = %s", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_real","object":"chat.completion","model":"deepseek-v4","choices":[{"index":0,"message":{"role":"assistant","content":"real upstream"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":   "memory",
		"PROVIDER_MODE": "mock",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	patchBody := `{"baseUrl":"` + upstream.URL + `/v1","upstreamApiKey":"channel-secret"}`
	patched := perform(router, http.MethodPatch, "/api/channels/chn_1002", patchBody, nil)
	if patched.Code != http.StatusOK {
		t.Fatalf("patch channel status = %d body = %s", patched.Code, patched.Body.String())
	}

	chatBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if chat.Code != http.StatusOK {
		t.Fatalf("chat status = %d body = %s", chat.Code, chat.Body.String())
	}
	if !upstreamCalled || !bytes.Contains(chat.Body.Bytes(), []byte(`real upstream`)) {
		t.Fatalf("channel key request did not forward upstream: called=%v body=%s", upstreamCalled, chat.Body.String())
	}
}

func TestImageGenerationsForwardToOpenAICompatibleUpstream(t *testing.T) {
	var upstreamCalled bool
	var upstreamAuth string
	var upstreamPath string
	var upstreamPayload map[string]interface{}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		upstreamAuth = r.Header.Get("Authorization")
		upstreamPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode upstream image request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1780000000,"data":[{"b64_json":"image-bytes"}]}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":   "memory",
		"PROVIDER_MODE": "mock",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-1", Name: "GPT Image", Vendor: "OpenAI", Aliases: []string{"image"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image", Name: "Image Provider", Provider: "openai", BaseURL: upstream.URL + "/v1", UpstreamAPIKey: "channel-image-secret", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-1"},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"image","prompt":"a small moon cat","size":"1024x1024","response_format":"b64_json"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if !upstreamCalled {
		t.Fatal("upstream image endpoint was not called")
	}
	if upstreamPath != "/v1/images/generations" {
		t.Fatalf("upstream image path = %s", upstreamPath)
	}
	if upstreamAuth != "Bearer channel-image-secret" {
		t.Fatalf("upstream auth = %s", upstreamAuth)
	}
	if upstreamPayload["model"] != "gpt-image-1" || upstreamPayload["prompt"] != "a small moon cat" || upstreamPayload["size"] != "1024x1024" {
		t.Fatalf("unexpected upstream payload = %#v", upstreamPayload)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"image-bytes"`)) {
		t.Fatalf("image response was not proxied: %s", response.Body.String())
	}
}

func TestImageGenerationsKeepsConnectionAliveDuringSlowUpstream(t *testing.T) {
	oldInterval := imageJSONKeepaliveInterval
	imageJSONKeepaliveInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		imageJSONKeepaliveInterval = oldInterval
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var upstreamPayload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode upstream image request: %v", err)
		}
		time.Sleep(35 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1780000000,"data":[{"b64_json":"slow-image-bytes"}]}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":   "memory",
		"PROVIDER_MODE": "mock",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-1", Name: "GPT Image", Vendor: "OpenAI", Aliases: []string{"image"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image", Name: "Image Provider", Provider: "openai", BaseURL: upstream.URL + "/v1", UpstreamAPIKey: "channel-image-secret", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-1"},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"image","prompt":"a slow moon cat"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if !response.Flushed {
		t.Fatal("slow image response did not flush keepalive bytes")
	}
	if !strings.HasPrefix(response.Body.String(), " \n") {
		t.Fatalf("slow image response did not start with JSON keepalive whitespace: %q", response.Body.String())
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("slow image response should remain valid JSON after keepalive: %v body=%q", err, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"slow-image-bytes"`)) {
		t.Fatalf("image response was not proxied: %s", response.Body.String())
	}
}

func TestImageGenerationsRetryNextOpenAIAccountOnBillingError(t *testing.T) {
	authHeaders := []string{}
	upstreamPayloads := []map[string]interface{}{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		authHeaders = append(authHeaders, auth)
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
		var upstreamPayload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode codex image request: %v", err)
		}
		upstreamPayloads = append(upstreamPayloads, upstreamPayload)
		if auth == "Bearer billing-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"code":"billing_not_active","message":"billing inactive","type":"billing_not_active"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexImageSSE("image-after-retry")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-1", Name: "GPT Image", Vendor: "OpenAI", Aliases: []string{"image"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_pool", Name: "Image Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-1"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_billing", Email: "billing@example.com", AccessToken: "billing-token", Status: "healthy"},
			{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"image","prompt":"retry account","n":2}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if len(authHeaders) != 2 || authHeaders[0] != "Bearer billing-token" || authHeaders[1] != "Bearer good-token" {
		t.Fatalf("upstream auth sequence = %#v", authHeaders)
	}
	if len(upstreamPayloads) != 2 {
		t.Fatalf("upstream payload count = %d", len(upstreamPayloads))
	}
	tools, ok := upstreamPayloads[1]["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("unexpected codex image tools = %#v", upstreamPayloads[1]["tools"])
	}
	tool, ok := tools[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected codex image tool = %#v", tools[0])
	}
	if tool["type"] != "image_generation" || tool["action"] != "generate" || tool["model"] != "gpt-image-1" || tool["output_format"] != "png" {
		t.Fatalf("unexpected codex image tool payload = %#v", tool)
	}
	if _, ok := tool["n"]; ok {
		t.Fatalf("codex image tool should not include n: %#v", tool)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"image-after-retry"`)) {
		t.Fatalf("image response was not retried successfully: %s", response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_image_pool")
	if channel.OpenAIAccounts[0].Status != "invalid" || !strings.Contains(channel.OpenAIAccounts[0].LastError, "billing inactive") {
		t.Fatalf("billing account was not marked invalid: %#v", channel.OpenAIAccounts[0])
	}
	if channel.OpenAIAccounts[1].Status != "healthy" {
		t.Fatalf("good account status changed: %#v", channel.OpenAIAccounts[1])
	}
}

func TestImageGenerationsOpenAIAccountUsesCodexResponsesForGPTImage2(t *testing.T) {
	var upstreamAuth string
	var upstreamAccept string
	var upstreamPayload map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		upstreamAccept = r.Header.Get("Accept")
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode responses image request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexImageSSE("responses-image")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-2", Name: "GPT Image 2", Vendor: "OpenAI", Aliases: []string{"image2"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_direct", Name: "Image Direct Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-2"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"gpt-image-2","prompt":"direct account","n":2,"stream":true}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if upstreamAuth != "Bearer good-token" || upstreamAccept != "text/event-stream" {
		t.Fatalf("unexpected responses headers auth=%s accept=%s", upstreamAuth, upstreamAccept)
	}
	if upstreamPayload["model"] != chatGPTCodexImageModel || upstreamPayload["stream"] != true || upstreamPayload["tool_choice"] == nil {
		t.Fatalf("unexpected responses payload = %#v", upstreamPayload)
	}
	tools, ok := upstreamPayload["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("unexpected responses tools = %#v", upstreamPayload["tools"])
	}
	tool, ok := tools[0].(map[string]interface{})
	if !ok || tool["type"] != "image_generation" || tool["model"] != "gpt-image-2" {
		t.Fatalf("unexpected responses tool = %#v", tools[0])
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"responses-image"`)) {
		t.Fatalf("responses image response was not proxied: %s", response.Body.String())
	}
}

func TestImageGenerationsOpenAIAccountReadsCompletedOutputImage(t *testing.T) {
	var upstreamPayload map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode responses image request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexImageCompletedSSE("completed-image")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-2", Name: "GPT Image 2", Vendor: "OpenAI", Aliases: []string{"image2"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_completed", Name: "Image Completed Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-2"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_completed", Email: "completed@example.com", AccessToken: "completed-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"gpt-image-2","prompt":"plain image","n":2}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	tools, ok := upstreamPayload["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("unexpected responses tools = %#v", upstreamPayload["tools"])
	}
	tool, ok := tools[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected responses tool = %#v", tools[0])
	}
	if _, exists := tool["n"]; exists {
		t.Fatalf("responses image tool should not include n: %#v", tool)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"completed-image"`)) {
		t.Fatalf("responses completed image response was not proxied: %s", response.Body.String())
	}
}

func TestImageEditsOpenAIAccountUsesCodexResponsesMultipart(t *testing.T) {
	var upstreamPayload map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			t.Fatalf("decode responses image request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexImageSSE("edited-image")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-2", Name: "GPT Image 2", Vendor: "OpenAI", Aliases: []string{"image2"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_edit", Name: "Image Edit Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-2"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_edit", Email: "edit@example.com", AccessToken: "edit-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "gpt-image-2"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("prompt", "replace the background"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("input_fidelity", "high"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("n", "2"); err != nil {
		t.Fatal(err)
	}
	imagePart, err := writer.CreateFormFile("image", "source.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := imagePart.Write([]byte("\x89PNG\r\n\x1a\nsource")); err != nil {
		t.Fatal(err)
	}
	maskPart, err := writer.CreateFormFile("mask", "mask.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maskPart.Write([]byte("\x89PNG\r\n\x1a\nmask")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	request.Header.Set("Authorization", "Bearer cat_fixture_live_secret")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("image edit status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	tools, ok := upstreamPayload["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("unexpected responses tools = %#v", upstreamPayload["tools"])
	}
	tool, ok := tools[0].(map[string]interface{})
	if !ok || tool["action"] != "edit" || tool["model"] != "gpt-image-2" || tool["input_fidelity"] != "high" {
		t.Fatalf("unexpected responses tool = %#v", tools[0])
	}
	if _, exists := tool["n"]; exists {
		t.Fatalf("responses image tool should not include n: %#v", tool)
	}
	mask, ok := tool["input_image_mask"].(map[string]interface{})
	if !ok || !strings.HasPrefix(fmt.Sprint(mask["image_url"]), "data:image/") {
		t.Fatalf("unexpected edit mask = %#v", tool["input_image_mask"])
	}
	input, ok := upstreamPayload["input"].([]interface{})
	if !ok || len(input) != 1 {
		t.Fatalf("unexpected responses input = %#v", upstreamPayload["input"])
	}
	message, ok := input[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected responses message = %#v", input[0])
	}
	content, ok := message["content"].([]interface{})
	if !ok || len(content) < 2 {
		t.Fatalf("unexpected responses content = %#v", message["content"])
	}
	imageContent, ok := content[1].(map[string]interface{})
	if !ok || imageContent["type"] != "input_image" || !strings.HasPrefix(fmt.Sprint(imageContent["image_url"]), "data:image/") {
		t.Fatalf("unexpected edit input image = %#v", content[1])
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"b64_json":"edited-image"`)) {
		t.Fatalf("responses image edit response was not proxied: %s", recorder.Body.String())
	}
}

func TestImageGenerationsOpenAIAccountFallsBackWhenCodexDirectImageIsEmpty(t *testing.T) {
	directCalls := 0
	responsesCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/codex/images/generations":
			directCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"created":1780000000,"data":[]}`))
		case "/backend-api/codex/responses":
			responsesCalls++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(codexImageSSE("fallback-image-empty")))
		default:
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-2", Name: "GPT Image 2", Vendor: "OpenAI", Aliases: []string{"image2"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_direct_empty", Name: "Image Direct Empty Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-2"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"gpt-image-2","prompt":"fallback empty"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if directCalls != 0 || responsesCalls != 1 {
		t.Fatalf("unexpected upstream calls direct=%d responses=%d", directCalls, responsesCalls)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"fallback-image-empty"`)) {
		t.Fatalf("fallback image response was not returned: %s", response.Body.String())
	}
}

func TestImageGenerationsOpenAIAccountRetriesCodexResponsesMainModelOn504(t *testing.T) {
	mainModels := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode responses image request: %v", err)
		}
		mainModels = append(mainModels, completionPrompt(payload["model"]))
		if len(mainModels) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(`{"error":{"code":"gateway_timeout","message":"Gateway Timeout","type":"api_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexImageSSE("image-after-main-model-retry")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-2", Name: "GPT Image 2", Vendor: "OpenAI", Aliases: []string{"image2"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_main_model_retry", Name: "Image Main Model Retry Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-2"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"gpt-image-2","prompt":"retry main model"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	expectedModels := []string{chatGPTCodexImageModel, "gpt-5.5"}
	if !reflect.DeepEqual(mainModels, expectedModels) {
		t.Fatalf("main model sequence = %#v", mainModels)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"image-after-main-model-retry"`)) {
		t.Fatalf("retry image response was not returned: %s", response.Body.String())
	}
}

func TestImageGenerationsOpenAIAccountFallsBackWhenCodexDirectImageGatewayTimesOut(t *testing.T) {
	directCalls := 0
	responsesCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/codex/images/generations":
			directCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(`{"error":{"code":"gateway_timeout","message":"Gateway Timeout","type":"api_error"}}`))
		case "/backend-api/codex/responses":
			responsesCalls++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(codexImageSSE("fallback-image-504")))
		default:
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-2", Name: "GPT Image 2", Vendor: "OpenAI", Aliases: []string{"image2"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_direct_504", Name: "Image Direct Timeout Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-2"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"gpt-image-2","prompt":"fallback 504"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if directCalls != 0 || responsesCalls != 1 {
		t.Fatalf("unexpected upstream calls direct=%d responses=%d", directCalls, responsesCalls)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"fallback-image-504"`)) {
		t.Fatalf("fallback image response was not returned: %s", response.Body.String())
	}
}

func TestImageGenerationsOpenAIAccountFallsBackWhenCodexDirectImageUsageLimited(t *testing.T) {
	directCalls := 0
	responsesCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/codex/images/generations":
			directCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"usage_limit_reached","message":"The usage limit has been reached","type":"rate_limit_error"}}`))
		case "/backend-api/codex/responses":
			responsesCalls++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(codexImageSSE("fallback-image-direct-limit")))
		default:
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-2", Name: "GPT Image 2", Vendor: "OpenAI", Aliases: []string{"image2"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_direct_limit", Name: "Image Direct Limit Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-2"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy", QuotaLimits: []OpenAIQuotaLimit{
				{Label: "5h", Window: "5h", Limit: 100, Used: 82, Remaining: 18, PercentRemaining: 18},
				{Label: "Weekly", Window: "weekly", Limit: 100, Used: 13, Remaining: 87, PercentRemaining: 87},
			}},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"gpt-image-2","prompt":"fallback direct limit"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if directCalls != 0 || responsesCalls != 1 {
		t.Fatalf("unexpected upstream calls direct=%d responses=%d", directCalls, responsesCalls)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"fallback-image-direct-limit"`)) {
		t.Fatalf("fallback image response was not returned: %s", response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_image_direct_limit")
	if channel.OpenAIAccounts[0].Status != "healthy" || strings.Contains(channel.OpenAIAccounts[0].LastError, "usage limit") {
		t.Fatalf("direct limit should not mark account limited after fallback success: %#v", channel.OpenAIAccounts[0])
	}
}

func TestImageGenerationsRetryNextOpenAIAccountOnUsageLimit(t *testing.T) {
	authHeaders := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		authHeaders = append(authHeaders, auth)
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
		if auth == "Bearer limited-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"upstream_error","message":"The usage limit has been reached","param":"model","type":"usage_limit_reached"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexImageSSE("image-after-quota-retry")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-1", Name: "GPT Image", Vendor: "OpenAI", Aliases: []string{"image"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_pool", Name: "Image Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-1"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_limited", Email: "limited@example.com", AccessToken: "limited-token", Status: "healthy"},
			{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"image","prompt":"retry usage limit"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if len(authHeaders) != 2 || authHeaders[0] != "Bearer limited-token" || authHeaders[1] != "Bearer good-token" {
		t.Fatalf("upstream auth sequence = %#v", authHeaders)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"image-after-quota-retry"`)) {
		t.Fatalf("image response was not retried successfully: %s", response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_image_pool")
	if channel.OpenAIAccounts[0].Status != "unchecked" || !strings.Contains(channel.OpenAIAccounts[0].LastError, "usage limit") {
		t.Fatalf("limited account was not marked unchecked: %#v", channel.OpenAIAccounts[0])
	}
	if channel.OpenAIAccounts[1].Status != "healthy" {
		t.Fatalf("good account status changed: %#v", channel.OpenAIAccounts[1])
	}
}

func TestImageGenerationsRetryNextOpenAIAccountOnTransientUpstreamError(t *testing.T) {
	authHeaders := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		authHeaders = append(authHeaders, auth)
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
		if auth == "Bearer gateway-timeout-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(`{"error":{"code":"gateway_timeout","message":"temporary upstream timeout","type":"server_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexImageSSE("image-after-504-retry")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-1", Name: "GPT Image", Vendor: "OpenAI", Aliases: []string{"image"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_pool", Name: "Image Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-1"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_504", Email: "timeout@example.com", AccessToken: "gateway-timeout-token", Status: "healthy"},
			{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"image","prompt":"retry transient"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	expectedAuthHeaders := []string{"Bearer gateway-timeout-token", "Bearer gateway-timeout-token", "Bearer gateway-timeout-token", "Bearer good-token"}
	if !reflect.DeepEqual(authHeaders, expectedAuthHeaders) {
		t.Fatalf("upstream auth sequence = %#v", authHeaders)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"image-after-504-retry"`)) {
		t.Fatalf("image response was not retried successfully: %s", response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_image_pool")
	if channel.OpenAIAccounts[0].Status != "healthy" || channel.OpenAIAccounts[0].LastError != "" {
		t.Fatalf("transient 504 account should not be marked bad: %#v", channel.OpenAIAccounts[0])
	}
}

func TestImageGenerationsRetryNextOpenAIAccountOnMissingImageScope(t *testing.T) {
	authHeaders := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		authHeaders = append(authHeaders, auth)
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
		if auth == "Bearer no-image-scope-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"upstream_error","message":"You have insufficient permissions for this operation. Missing scopes: api.model.images.request.","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexImageSSE("image-after-scope-retry")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-1", Name: "GPT Image", Vendor: "OpenAI", Aliases: []string{"image"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_pool", Name: "Image Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-1"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_no_scope", Email: "no-scope@example.com", AccessToken: "no-image-scope-token", Status: "healthy"},
			{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"image","prompt":"retry image scope"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if len(authHeaders) != 2 || authHeaders[0] != "Bearer no-image-scope-token" || authHeaders[1] != "Bearer good-token" {
		t.Fatalf("upstream auth sequence = %#v", authHeaders)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"image-after-scope-retry"`)) {
		t.Fatalf("image response was not retried successfully: %s", response.Body.String())
	}

	providerErr := providerErrorFromUpstream(http.StatusForbidden, []byte(`{"error":{"code":"upstream_error","message":"You have insufficient permissions for this operation. Missing scopes: api.model.images.request.","type":"invalid_request_error"}}`))
	if providerErr.Code != "upstream_missing_image_scope" {
		t.Fatalf("image scope error code = %s", providerErr.Code)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_image_pool")
	if channel.OpenAIAccounts[0].Status != "invalid" || !strings.Contains(channel.OpenAIAccounts[0].LastError, "api.model.images.request") {
		t.Fatalf("missing-scope account was not marked invalid: %#v", channel.OpenAIAccounts[0])
	}
	if channel.OpenAIAccounts[1].Status != "healthy" {
		t.Fatalf("good account status changed: %#v", channel.OpenAIAccounts[1])
	}
}

func TestImageGenerationsRetryNextOpenAIAccountOnInvalidatedToken(t *testing.T) {
	authHeaders := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		authHeaders = append(authHeaders, auth)
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
		if auth == "Bearer invalidated-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"upstream_token_invalidated","message":"Your authentication token has been invalidated. Please try signing in again.","param":"model","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexImageSSE("image-after-token-retry")))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-1", Name: "GPT Image", Vendor: "OpenAI", Aliases: []string{"image"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_pool", Name: "Image Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-1"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_invalidated", Email: "invalidated@example.com", AccessToken: "invalidated-token", Status: "healthy"},
			{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"image","prompt":"retry invalidated token"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if len(authHeaders) != 2 || authHeaders[0] != "Bearer invalidated-token" || authHeaders[1] != "Bearer good-token" {
		t.Fatalf("upstream auth sequence = %#v", authHeaders)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json":"image-after-token-retry"`)) {
		t.Fatalf("image response was not retried successfully: %s", response.Body.String())
	}

	providerErr := providerErrorFromUpstream(http.StatusUnauthorized, []byte(`{"error":{"code":"upstream_token_invalidated","message":"Your authentication token has been invalidated. Please try signing in again.","param":"model","type":"invalid_request_error"}}`))
	if providerErr.Code != "upstream_token_invalidated" {
		t.Fatalf("invalidated token error code = %s", providerErr.Code)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_image_pool")
	if channel.OpenAIAccounts[0].Status != "invalid" || !strings.Contains(channel.OpenAIAccounts[0].LastError, "token has been invalidated") {
		t.Fatalf("invalidated account was not marked invalid: %#v", channel.OpenAIAccounts[0])
	}
	if channel.OpenAIAccounts[1].Status != "healthy" {
		t.Fatalf("good account status changed: %#v", channel.OpenAIAccounts[1])
	}
}

func TestImageGenerationsReturnPoolUnavailableWhenAllOpenAIAccountsInvalidated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("upstream image path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"upstream_token_invalidated","message":"Your authentication token has been invalidated. Please try signing in again.","param":"model","type":"invalid_request_error"}}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "mock",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-1", Name: "GPT Image", Vendor: "OpenAI", Aliases: []string{"image"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image_pool", Name: "Image Pool", Provider: "openai", BaseURL: upstream.URL + "/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-1"},
		OpenAIAccounts: []OpenAIAccount{
			{ID: "oaiacc_invalidated_one", Email: "invalidated-one@example.com", AccessToken: "invalidated-one-token", Status: "healthy"},
			{ID: "oaiacc_invalidated_two", Email: "invalidated-two@example.com", AccessToken: "invalidated-two-token", Status: "healthy"},
		},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"image","prompt":"all invalidated"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusBadGateway {
		t.Fatalf("image generation status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`upstream_accounts_unavailable`)) {
		t.Fatalf("expected pool unavailable error: %s", response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_image_pool")
	if channel.OpenAIAccounts[0].Status != "invalid" || channel.OpenAIAccounts[1].Status != "invalid" {
		t.Fatalf("invalidated accounts were not marked invalid: %#v", channel.OpenAIAccounts)
	}
}

func TestImageGenerationsWorkWithoutV1Prefix(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID: "gpt-image-1", Name: "GPT Image", Vendor: "OpenAI", Aliases: []string{"image"}, Category: "图像", Status: "available",
	})
	server.state.Channels = append(server.state.Channels, Channel{
		ID: "chn_image", Name: "Image Provider", Provider: "openai", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-image-1"},
	})
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/images/generations", `{"model":"image","prompt":"test image"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("image generation without v1 status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"b64_json"`)) {
		t.Fatalf("mock image response missing data: %s", response.Body.String())
	}
}

func TestOpenAICompatibleProviderPreservesUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_api_key","message":"bad upstream key","type":"invalid_request_error"}}`))
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

	chatBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if chat.Code != http.StatusUnauthorized {
		t.Fatalf("chat upstream error status = %d body = %s", chat.Code, chat.Body.String())
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte(`upstream_invalid_api_key`)) || !bytes.Contains(chat.Body.Bytes(), []byte(`bad upstream key`)) {
		t.Fatalf("upstream error was not preserved: %s", chat.Body.String())
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

func TestOpenAICompatibleStreamWrapsUpstreamNotFoundAsSSE(t *testing.T) {
	streamModes := []bool{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		streamModes = append(streamModes, payload.Stream)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"stream endpoint not found","type":"not_found"}}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "compatible",
		"UPSTREAM_API_KEY": "upstream-secret",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.findChannel("chn_1002").BaseURL = upstream.URL + "/v1"
	server.findChannel("chn_1002").StreamMode = "real"
	server.mu.Unlock()

	chatBody := `{"model":"ds","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if chat.Code != http.StatusOK {
		t.Fatalf("stream error status = %d body = %s", chat.Code, chat.Body.String())
	}
	if !reflect.DeepEqual(streamModes, []bool{true}) {
		t.Fatalf("upstream stream modes = %#v", streamModes)
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte(`stream endpoint not found`)) || !bytes.Contains(chat.Body.Bytes(), []byte("data: [DONE]")) {
		t.Fatalf("stream error was not wrapped as SSE: %s", chat.Body.String())
	}
}

func TestOpenAICompatibleStreamWrapsFinalUpstreamErrorAsSSE(t *testing.T) {
	streamModes := []bool{}
	models := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		models = append(models, payload.Model)
		streamModes = append(streamModes, payload.Stream)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"route_not_found","message":"Route not found","type":"invalid_request_error"}}`))
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"PROVIDER_MODE":    "compatible",
		"UPSTREAM_API_KEY": "upstream-secret",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	server.state.Models = append(server.state.Models, Model{
		ID:     "doubao-seed-2.0-pro",
		Name:   "doubao-seed-2.0-pro",
		Vendor: "Doubao",
		Status: "available",
	})
	channel := server.findChannel("chn_1002")
	channel.BaseURL = upstream.URL + "/api/coding/v3"
	channel.StreamMode = "real"
	channel.Models = []string{"doubao-seed-2.0-pro"}
	server.mu.Unlock()

	chatBody := `{"model":"doubao-seed-2.0-pro","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	chat := perform(router, http.MethodPost, "/v1/chat/completions", chatBody, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
	if chat.Code != http.StatusOK {
		t.Fatalf("stream error status = %d body = %s", chat.Code, chat.Body.String())
	}
	if got := chat.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("stream error content-type = %s", got)
	}
	if !reflect.DeepEqual(streamModes, []bool{true}) {
		t.Fatalf("upstream stream modes = %#v", streamModes)
	}
	if !reflect.DeepEqual(models, []string{"doubao-seed-2.0-pro"}) {
		t.Fatalf("upstream models = %#v", models)
	}
	if !bytes.Contains(chat.Body.Bytes(), []byte(`"status":404`)) || !bytes.Contains(chat.Body.Bytes(), []byte("data: [DONE]")) {
		t.Fatalf("stream error was not wrapped as SSE: %s", chat.Body.String())
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

func TestImportCPAJSONAddsEncryptedAccountToChannel(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "state.json")
	accessToken := "cpa-access-token-secret"
	withEnv(t, map[string]string{
		"PERSISTENCE": "file",
		"DATA_FILE":   dataFile,
		"SECRET_KEY":  "import-secret-key",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	body := `{
		"id_token":"cpa-id-token-secret",
		"access_token":"` + accessToken + `",
		"refresh_token":"cpa-refresh-token-secret",
		"account_id":"acc_cpa",
		"last_refresh":"2026-07-06T00:00:00.000Z",
		"email":"cpa@example.com",
		"type":"codex",
		"expired":"2026-07-06T01:00:00.000Z"
	}`
	response := perform(router, http.MethodPost, "/api/channels/chn_1002/import-openai-accounts", body, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("CPA import status = %d body = %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte(accessToken)) || bytes.Contains(response.Body.Bytes(), []byte("cpa-refresh-token-secret")) {
		t.Fatalf("import response leaked token: %s", response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"imported":1`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"openaiAccountCount":1`)) {
		t.Fatalf("import response missing summary: %s", response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte(`"accessToken"`)) || bytes.Contains(response.Body.Bytes(), []byte(`"refreshToken"`)) {
		t.Fatalf("import response exposed protected fields: %s", response.Body.String())
	}

	stateContent, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if bytes.Contains(stateContent, []byte(accessToken)) {
		t.Fatal("plain CPA access token was stored")
	}
	if !bytes.Contains(stateContent, []byte("enc:v1:")) {
		t.Fatalf("encrypted token marker missing from state: %s", string(stateContent))
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_1002")
	if channel == nil || len(channel.OpenAIAccounts) != 1 {
		t.Fatalf("channel account count = %#v", channel)
	}
	if channel.OpenAIAccounts[0].ClientProfile != chatGPTCodexTUIProfile {
		t.Fatalf("CPA account client profile = %s", channel.OpenAIAccounts[0].ClientProfile)
	}
}

func TestImportSub2APIJSONAddsAccountsToExistingChannel(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	body := `{
		"baseUrl":"https://api.openai.com/v1",
		"models":["gpt-image-2"],
		"data":{
			"exported_at":"2026-07-06T00:00:00Z",
			"models":["gpt-5.4"],
			"accounts":[
				{
					"name":"first@example.com",
					"platform":"openai",
					"type":"oauth",
					"credentials":{
						"access_token":"first-access-token",
						"refresh_token":"first-refresh-token",
						"email":"first@example.com",
						"chatgpt_account_id":"acc_first"
					}
				},
				{
					"name":"second@example.com",
					"platform":"openai",
					"type":"oauth",
					"credentials":{
						"email":"second@example.com",
						"chatgpt_account_id":"acc_second"
					},
					"codex_auth":{
						"tokens":{
							"access_token":"second-access-token",
							"refresh_token":"second-refresh-token"
						}
					}
				},
				{
					"email":"third@example.com",
					"platform":"openai",
					"type":"oauth",
					"codex_auth":{
						"tokens":{
							"access_token":"third-access-token",
							"refresh_token":"third-refresh-token",
							"account_id":"acc_third",
							"user_id":"user_third",
							"expires_at":"2026-07-06T02:00:00Z"
						}
					}
				}
			],
			"type":"sub2api-data",
			"version":1
		}
	}`
	response := perform(router, http.MethodPost, "/api/channels/chn_1002/import-openai-accounts", body, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("Sub2 import status = %d body = %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("first-access-token")) ||
		bytes.Contains(response.Body.Bytes(), []byte("second-access-token")) ||
		bytes.Contains(response.Body.Bytes(), []byte("third-access-token")) {
		t.Fatalf("Sub2 import response leaked token: %s", response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"imported":3`)) {
		t.Fatalf("Sub2 import response missing imported count: %s", response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_1002")
	if channel == nil {
		t.Fatal("target channel missing")
	}
	if len(channel.OpenAIAccounts) != 3 {
		t.Fatalf("imported account count = %d", len(channel.OpenAIAccounts))
	}
	if channel.OpenAIAccounts[2].Email != "third@example.com" ||
		channel.OpenAIAccounts[2].AccountID != "acc_third" ||
		channel.OpenAIAccounts[2].UserID != "user_third" ||
		channel.OpenAIAccounts[2].ExpiresAt != "2026-07-06T02:00:00Z" ||
		channel.OpenAIAccounts[2].ClientProfile != chatGPTCodexTUIProfile {
		t.Fatalf("Sub2 codex_auth token fields were not imported: %#v", channel.OpenAIAccounts[2])
	}
	upstreamKey, err := server.channelUpstreamKey(*channel)
	if err != nil {
		t.Fatalf("channel upstream key: %v", err)
	}
	if upstreamKey != "" {
		t.Fatalf("OAuth account token leaked into OpenAI-compatible upstream key: %s", upstreamKey)
	}
	if !containsString(channel.Models, "gpt-image-2") {
		t.Fatalf("import did not add model to target channel: %#v", channel.Models)
	}
	if !containsString(channel.Models, "gpt-5.4") {
		t.Fatalf("import did not add nested Sub2 model to target channel: %#v", channel.Models)
	}
	if !containsString(channel.Models, "gpt-image-1") {
		t.Fatalf("import did not add default image model to target channel: %#v", channel.Models)
	}
	if server.findModel("gpt-image-2") == nil {
		t.Fatal("import did not create missing model")
	}
	if server.findModel("gpt-image-1") == nil {
		t.Fatal("import did not create default image model")
	}
}

func TestParseOpenAIAccountImportSupportsCamelCaseCPAAndSub2API(t *testing.T) {
	accounts, _, invalid, err := parseOpenAIAccountImportJSON([]byte(`{
		"accounts":[
			{
				"name":"camel-sub@example.com",
				"platform":"openai",
				"type":"oauth",
				"credentials":{
					"accessToken":"sub-access-token",
					"refreshToken":"sub-refresh-token",
					"accountId":"acc_sub_camel",
					"userId":"user_sub_camel",
					"expiresAt":"2026-07-06T02:00:00Z",
					"planType":"pro"
				}
			}
		],
		"type":"sub2api-data"
	}`))
	if err != nil || invalid != 0 || len(accounts) != 1 {
		t.Fatalf("camel Sub2API import accounts=%#v invalid=%d err=%v", accounts, invalid, err)
	}
	if accounts[0].AccessToken != "sub-access-token" ||
		accounts[0].RefreshToken != "sub-refresh-token" ||
		accounts[0].AccountID != "acc_sub_camel" ||
		accounts[0].UserID != "user_sub_camel" ||
		accounts[0].PlanType != "pro" ||
		codexClientProfileForImport(accounts[0].Source, accounts[0].ClientProfile) != chatGPTCodexTUIProfile {
		t.Fatalf("camel Sub2API fields were not parsed: %#v", accounts[0])
	}

	accounts, _, invalid, err = parseOpenAIAccountImportJSON([]byte(`{
		"accessToken":"cpa-access-token",
		"refreshToken":"cpa-refresh-token",
		"idToken":"cpa-id-token",
		"accountId":"acc_cpa_camel",
		"userId":"user_cpa_camel",
		"expiresAt":"2026-07-06T03:00:00Z",
		"planType":"plus",
		"email":"camel-cpa@example.com"
	}`))
	if err != nil || invalid != 0 || len(accounts) != 1 {
		t.Fatalf("camel CPA import accounts=%#v invalid=%d err=%v", accounts, invalid, err)
	}
	if accounts[0].AccessToken != "cpa-access-token" ||
		accounts[0].RefreshToken != "cpa-refresh-token" ||
		accounts[0].IDToken != "cpa-id-token" ||
		accounts[0].AccountID != "acc_cpa_camel" ||
		accounts[0].UserID != "user_cpa_camel" ||
		accounts[0].ExpiresAt != "2026-07-06T03:00:00Z" ||
		codexClientProfileForImport(accounts[0].Source, accounts[0].ClientProfile) != chatGPTCodexTUIProfile {
		t.Fatalf("camel CPA fields were not parsed: %#v", accounts[0])
	}
}

func TestParseOpenAIAccountImportSupportsChatGPTAuthSessionShape(t *testing.T) {
	accounts, _, invalid, err := parseOpenAIAccountImportJSON([]byte(`{
		"user":{"id":"user-session","email":"session@example.com","name":"Session User"},
		"accessToken":"session-access-token",
		"expires":"2026-10-07T19:47:47.256Z",
		"account":{"id":"account-session","plan_type":"free"}
	}`))
	if err != nil || invalid != 0 || len(accounts) != 1 {
		t.Fatalf("auth session import accounts=%#v invalid=%d err=%v", accounts, invalid, err)
	}
	account := accounts[0]
	if account.AccessToken != "session-access-token" ||
		account.AccountID != "account-session" ||
		account.UserID != "user-session" ||
		account.Email != "session@example.com" ||
		account.ExpiresAt != "2026-10-07T19:47:47.256Z" ||
		account.PlanType != "free" {
		t.Fatalf("auth session fields were not parsed: %#v", account)
	}
}

func TestImportOpenAIAccountsFromZipAddsAccountsToExistingChannel(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	var zipBody bytes.Buffer
	zipWriter := zip.NewWriter(&zipBody)
	cpaFile, err := zipWriter.Create("cpa/no-refresh.json")
	if err != nil {
		t.Fatalf("create cpa zip file: %v", err)
	}
	_, _ = cpaFile.Write([]byte(`{
		"access_token":"zip-cpa-access-token",
		"account_id":"acc_zip_cpa",
		"email":"zip-cpa@example.com",
		"type":"codex"
	}`))
	sub2File, err := zipWriter.Create("sub2/accounts.json")
	if err != nil {
		t.Fatalf("create sub2 zip file: %v", err)
	}
	_, _ = sub2File.Write([]byte(`{
		"type":"sub2api-data",
		"accounts":[
			{
				"name":"zip-sub2@example.com",
				"credentials":{
					"access_token":"zip-sub2-access-token",
					"email":"zip-sub2@example.com",
					"chatgpt_account_id":"acc_zip_sub2"
				}
			}
		]
	}`))
	invalidFile, err := zipWriter.Create("notes/not-an-account.json")
	if err != nil {
		t.Fatalf("create invalid zip file: %v", err)
	}
	_, _ = invalidFile.Write([]byte(`{"hello":"world"}`))
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	var multipartBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&multipartBody)
	part, err := multipartWriter.CreateFormFile("file", "accounts.zip")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	_, _ = part.Write(zipBody.Bytes())
	if err := multipartWriter.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/channels/chn_1002/import-openai-accounts", &multipartBody)
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-admin-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("ZIP import status = %d body = %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("zip-cpa-access-token")) || bytes.Contains(response.Body.Bytes(), []byte("zip-sub2-access-token")) {
		t.Fatalf("ZIP import response leaked token: %s", response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"imported":2`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"skipped":1`)) {
		t.Fatalf("ZIP import response missing summary: %s", response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel := server.findChannel("chn_1002")
	if channel == nil {
		t.Fatal("target channel missing")
	}
	if len(channel.OpenAIAccounts) != 2 {
		t.Fatalf("imported ZIP account count = %d", len(channel.OpenAIAccounts))
	}
	if channel.OpenAIAccounts[0].RefreshToken != "" {
		t.Fatal("missing refresh_token should be stored as empty and still imported")
	}
	if !containsString(channel.Models, "gpt-image-2") || !containsString(channel.Models, "gpt-image-1") {
		t.Fatalf("ZIP import did not add default image models: %#v", channel.Models)
	}
}

func TestNormalizeStateCollectionsAddsImageModelsToOpenAIAccountPools(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, _ := testServerRouter(t)
	server.mu.Lock()
	defer server.mu.Unlock()
	server.state.Models = []Model{
		{ID: "gpt-5.4", Name: "GPT-5.4", Vendor: "OpenAI", Status: "available"},
	}
	server.state.Channels = []Channel{
		{
			ID:       "chn_oauth_pool",
			Name:     "OpenAI OAuth Pool",
			Provider: "openai",
			Status:   "healthy",
			Models:   []string{"gpt-5.4"},
			OpenAIAccounts: []OpenAIAccount{
				{ID: "oaiacc_existing", Email: "existing@example.com", AccessToken: "existing-token", Status: "healthy"},
			},
		},
	}

	if !server.normalizeStateCollections() {
		t.Fatal("normalizeStateCollections did not report image model migration")
	}
	channel := server.findChannel("chn_oauth_pool")
	if channel == nil {
		t.Fatal("oauth pool channel missing")
	}
	if channel.Provider != "codex" {
		t.Fatalf("oauth pool provider = %q", channel.Provider)
	}
	if channel.BaseURL != defaultChatGPTAPIBaseURL {
		t.Fatalf("oauth pool base URL = %q", channel.BaseURL)
	}
	for _, modelID := range codexChannelModelIDs() {
		if !containsString(channel.Models, modelID) {
			t.Fatalf("oauth pool did not gain Codex model %s: %#v", modelID, channel.Models)
		}
	}
	for _, modelID := range codexChannelModelIDs() {
		if server.findModel(modelID) == nil {
			t.Fatalf("Codex model %s was not imported: %#v", modelID, server.state.Models)
		}
	}
}

func TestCodexChannelSyncAndCheckDoNotFetchOpenAIModels(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		t.Fatalf("codex channel should not call upstream model list: %s", r.URL.Path)
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	channel := server.findChannel("chn_1002")
	channel.Provider = "openai"
	channel.BaseURL = upstream.URL + "/v1"
	channel.Models = []string{}
	channel.OpenAIAccounts = []OpenAIAccount{
		{ID: "oaiacc_existing", Email: "existing@example.com", AccessToken: "existing-token", Status: "healthy"},
	}
	server.mu.Unlock()

	synced := perform(router, http.MethodPost, "/api/channels/chn_1002/sync-models", `{}`, nil)
	if synced.Code != http.StatusOK {
		t.Fatalf("sync codex models status = %d body = %s", synced.Code, synced.Body.String())
	}
	checked := perform(router, http.MethodPost, "/api/channels/chn_1002/check", `{}`, nil)
	if checked.Code != http.StatusOK {
		t.Fatalf("check codex channel status = %d body = %s", checked.Code, checked.Body.String())
	}
	if upstreamCalled {
		t.Fatal("codex channel called upstream /models")
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel = server.findChannel("chn_1002")
	if channel.Provider != "codex" {
		t.Fatalf("codex sync did not migrate provider: %s", channel.Provider)
	}
	for _, modelID := range codexChannelModelIDs() {
		if !containsString(channel.Models, modelID) {
			t.Fatalf("codex channel missing model %s: %#v", modelID, channel.Models)
		}
	}
}

func TestCheckOpenAIAccountsUpdatesAccountHealth(t *testing.T) {
	var usageAuthHeaders []string
	var usageAccountIDs []string
	var codexImageAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/backend-api/wham/usage":
			usageAuthHeaders = append(usageAuthHeaders, auth)
			usageAccountIDs = append(usageAccountIDs, r.Header.Get("Chatgpt-Account-Id"))
			switch {
			case strings.Contains(auth, "good-token"), strings.Contains(auth, "rate-limit-token"):
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"usage","limits":[{"name":"5h","used":4,"limit":100,"reset_at":"2026-07-06T05:00:00Z"},{"name":"weekly","remaining":90,"limit":100,"reset_at":"2026-07-13T00:00:00Z"}]}`))
			case strings.Contains(auth, "exhausted-token"):
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"usage","limits":[{"name":"5h","used":100,"limit":100,"reset_at":"2026-07-06T05:00:00Z"},{"name":"weekly","remaining":0,"limit":100,"reset_at":"2026-07-13T00:00:00Z"}]}`))
			case strings.Contains(auth, "denied-token"):
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"token_rejected","message":"token rejected for usage","type":"invalid_request_error"}}`))
			case strings.Contains(auth, "billing-token"):
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = w.Write([]byte(`{"error":{"code":"billing_not_active","message":"billing inactive","type":"billing_not_active"}}`))
			default:
				t.Fatalf("unexpected image auth = %s", auth)
			}
		case "/backend-api/codex/responses":
			codexImageAuth = auth
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(codexImageSSE("image-after-check")))
		default:
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	channel := server.findChannel("chn_1002")
	channel.BaseURL = upstream.URL + "/v1"
	channel.Status = "disabled"
	channel.Models = []string{}
	channel.OpenAIAccounts = []OpenAIAccount{
		{ID: "oaiacc_good", Email: "good@example.com", AccessToken: "good-token", AccountID: "acc-good", Status: "unchecked"},
		{ID: "oaiacc_rate_limit", Email: "rate-limit@example.com", AccessToken: "rate-limit-token", Status: "unchecked"},
		{ID: "oaiacc_exhausted", Email: "exhausted@example.com", AccessToken: "exhausted-token", Status: "unchecked"},
		{ID: "oaiacc_denied", Email: "denied@example.com", AccessToken: "denied-token", Status: "unchecked"},
		{ID: "oaiacc_billing", Email: "billing@example.com", AccessToken: "billing-token", Status: "unchecked"},
		{ID: "oaiacc_expired_no_refresh", Email: "expired@example.com", AccessToken: "expired-no-refresh-token", ExpiresAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), Status: "unchecked"},
		{ID: "oaiacc_missing_tokens", Email: "missing@example.com", Status: "unchecked"},
	}
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/api/channels/chn_1002/openai-accounts/check", `{}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("check accounts status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"checked":7`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"healthy":3`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"failed":1`)) {
		t.Fatalf("check response missing summary: %s", response.Body.String())
	}
	if len(usageAuthHeaders) != 5 {
		t.Fatalf("usage auth request count = %d", len(usageAuthHeaders))
	}
	if usageAccountIDs[0] != "acc-good" {
		t.Fatalf("ChatGPT account id header was not forwarded: %#v", usageAccountIDs)
	}

	server.mu.Lock()
	channel = server.findChannel("chn_1002")
	if channel.OpenAIAccounts[0].Status != "healthy" ||
		channel.OpenAIAccounts[1].Status != "healthy" ||
		channel.OpenAIAccounts[2].Status != "healthy" ||
		channel.OpenAIAccounts[3].Status != "unchecked" ||
		channel.OpenAIAccounts[4].Status != "invalid" ||
		channel.OpenAIAccounts[5].Status != "unchecked" ||
		channel.OpenAIAccounts[6].Status != "unchecked" {
		t.Fatalf("account statuses = %#v", channel.OpenAIAccounts)
	}
	if channel.Status != "healthy" || !containsString(channel.Models, "gpt-image-2") || !containsString(channel.Models, "gpt-image-1") {
		t.Fatalf("healthy check did not enable image pool: status=%s models=%#v", channel.Status, channel.Models)
	}
	if len(channel.OpenAIAccounts[0].QuotaLimits) != 2 || channel.OpenAIAccounts[0].QuotaLimits[0].Label != "5h" || channel.OpenAIAccounts[0].QuotaLimits[0].PercentRemaining != 96 {
		t.Fatalf("quota limits were not parsed: %#v", channel.OpenAIAccounts[0].QuotaLimits)
	}
	if !strings.Contains(channel.OpenAIAccounts[2].LastError, "usage limit reached") {
		t.Fatalf("exhausted usage should keep reset hint: %#v", channel.OpenAIAccounts[2])
	}
	if !strings.Contains(channel.OpenAIAccounts[3].LastError, "HTTP 401") {
		t.Fatalf("401 usage failure should be left unchecked: %#v", channel.OpenAIAccounts[3])
	}
	if !strings.Contains(channel.OpenAIAccounts[4].LastError, "billing inactive") {
		t.Fatalf("402 without refresh should be invalid: %#v", channel.OpenAIAccounts[4])
	}
	if !strings.Contains(channel.OpenAIAccounts[5].LastError, "expired") {
		t.Fatalf("expired access without refresh should stay unchecked: %#v", channel.OpenAIAccounts[5])
	}
	if !strings.Contains(channel.OpenAIAccounts[6].LastError, "missing access_token and refresh_token") {
		t.Fatalf("missing tokens should stay unchecked: %#v", channel.OpenAIAccounts[6])
	}
	server.mu.Unlock()

	imageResponse := perform(router, http.MethodPost, "/v1/images/generations", `{"model":"gpt-image-2","prompt":"after check"}`, map[string]string{
		"Authorization": "Bearer cat_fixture_live_secret",
	})
	if imageResponse.Code != http.StatusOK {
		t.Fatalf("image generation after check status = %d body = %s", imageResponse.Code, imageResponse.Body.String())
	}
	if codexImageAuth != "Bearer good-token" {
		t.Fatalf("image generation did not use first healthy account: %s", codexImageAuth)
	}
	if !bytes.Contains(imageResponse.Body.Bytes(), []byte(`"b64_json":"image-after-check"`)) {
		t.Fatalf("image response was not proxied after check: %s", imageResponse.Body.String())
	}
}

func TestDeleteOpenAIAccountRemovesAccountFromChannel(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	channel := server.findChannel("chn_1002")
	channel.OpenAIAccounts = []OpenAIAccount{
		{ID: "oaiacc_keep", Email: "keep@example.com", AccessToken: "keep-token", Status: "healthy"},
		{ID: "oaiacc_delete", Email: "delete@example.com", AccessToken: "delete-token", Status: "healthy"},
	}
	server.mu.Unlock()

	response := perform(router, http.MethodDelete, "/api/channels/chn_1002/openai-accounts/oaiacc_delete", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("delete account status = %d body = %s", response.Code, response.Body.String())
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"deleted":true`)) || bytes.Contains(response.Body.Bytes(), []byte("delete@example.com")) {
		t.Fatalf("delete account response did not remove account: %s", response.Body.String())
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel = server.findChannel("chn_1002")
	if len(channel.OpenAIAccounts) != 1 || channel.OpenAIAccounts[0].ID != "oaiacc_keep" {
		t.Fatalf("channel accounts after delete = %#v", channel.OpenAIAccounts)
	}
}

func TestActiveOpenAIAccountsOrdersUsageLimitedLast(t *testing.T) {
	accounts := []OpenAIAccount{
		{ID: "limited", AccessToken: "limited-token", Status: "healthy", QuotaLimits: []OpenAIQuotaLimit{{Label: "5h", Limit: 100, Used: 100, Remaining: 0, ResetAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}},
		{ID: "healthy", AccessToken: "healthy-token", Status: "healthy", QuotaLimits: []OpenAIQuotaLimit{{Label: "5h", Limit: 100, Remaining: 10, PercentRemaining: 10}}},
		{ID: "stale-last-error", AccessToken: "stale-last-error-token", Status: "healthy", LastError: "The usage limit has been reached", QuotaLimits: []OpenAIQuotaLimit{{Label: "5h", Limit: 100, Used: 1, Remaining: 99, PercentRemaining: 99}}},
		{ID: "reset-elapsed", AccessToken: "reset-elapsed-token", Status: "healthy", LastError: "The usage limit has been reached", QuotaLimits: []OpenAIQuotaLimit{{Label: "5h", Limit: 100, Used: 100, Remaining: 0, ResetAt: "2000-01-01T00:00:00Z"}}},
		{ID: "unchecked", AccessToken: "unchecked-token", Status: "unchecked"},
		{ID: "last-error-expired", AccessToken: "last-error-expired-token", Status: "unchecked", LastError: "The usage limit has been reached", LastCheckedAt: "2000-01-01T00:00:00Z"},
		{ID: "invalid", AccessToken: "invalid-token", Status: "invalid"},
		{ID: "last-error", AccessToken: "last-error-token", Status: "unchecked", LastError: "The usage limit has been reached"},
	}

	active := activeOpenAIAccounts(accounts)
	ids := []string{}
	for _, account := range active {
		ids = append(ids, account.ID)
	}
	expected := []string{"healthy", "stale-last-error", "reset-elapsed", "unchecked", "last-error-expired", "limited", "last-error"}
	if !reflect.DeepEqual(ids, expected) {
		t.Fatalf("active account order = %#v", ids)
	}
}

func TestOpenAIQuotaLimitsRequirePrimaryWindowWhenPresent(t *testing.T) {
	if openAIQuotaLimitsHaveRemaining([]OpenAIQuotaLimit{
		{Label: "5h", Window: "5h", Limit: 100, Used: 100, Remaining: 0, PercentRemaining: 0},
		{Label: "Weekly", Window: "weekly", Limit: 100, Used: 10, Remaining: 90, PercentRemaining: 90},
	}) {
		t.Fatal("weekly remaining quota should not make an exhausted 5h window usable")
	}
	if !openAIQuotaLimitsHaveRemaining([]OpenAIQuotaLimit{
		{Label: "5h", Window: "5h", Limit: 100, Used: 99, Remaining: 1, PercentRemaining: 1},
		{Label: "Weekly", Window: "weekly", Limit: 100, Used: 100, Remaining: 0, PercentRemaining: 0},
	}) {
		t.Fatal("5h remaining quota should be usable even when weekly is exhausted")
	}
	if !openAIQuotaLimitsHaveRemaining([]OpenAIQuotaLimit{
		{Label: "Weekly", Window: "weekly", Limit: 100, Used: 10, Remaining: 90, PercentRemaining: 90},
	}) {
		t.Fatal("non-Codex quota payloads without 5h should still use any remaining window")
	}
}

func TestParseWhamUsageQuotaLimitsDerivesRemainingFromCapAndUsed(t *testing.T) {
	limits := parseWhamUsageQuotaLimits([]byte(`{
		"primary_window":{"cap":100,"num_used":0,"reset_time":"2026-07-06T05:46:00Z"},
		"secondary_window":{"cap":100,"num_used":25,"reset_time":"2026-07-13T00:46:00Z"},
		"rate_limits":[{"name":"fractional","remaining_percent":0.42,"reset_after_seconds":3600}]
	}`))
	if len(limits) < 3 {
		t.Fatalf("limits len = %d: %#v", len(limits), limits)
	}
	byName := map[string]OpenAIQuotaLimit{}
	for _, limit := range limits {
		byName[limit.Name] = limit
	}
	primary := byName["primary_window"]
	if primary.Remaining != 100 || primary.PercentRemaining != 100 {
		t.Fatalf("primary cap/num_used not derived as full quota: %#v", primary)
	}
	secondary := byName["secondary_window"]
	if secondary.Remaining != 75 || secondary.PercentRemaining != 75 {
		t.Fatalf("secondary cap/num_used not derived correctly: %#v", secondary)
	}
	fractional := byName["fractional"]
	if fractional.PercentRemaining != 42 {
		t.Fatalf("fractional percent not converted: %#v", fractional)
	}
}

func TestParseWhamUsageQuotaLimitsUsesCodexRateLimitWindows(t *testing.T) {
	limits := parseWhamUsageQuotaLimits([]byte(`{
		"rate_limit":{
			"primary_window":{"used_percent":25,"limit_window_seconds":18000,"reset_after_seconds":3600},
			"secondary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_at":1783353600},
			"monthly_window":{"used_percent":10,"limit_window_seconds":2592000}
		},
		"plan_type":"pro"
	}`))
	if len(limits) != 3 {
		t.Fatalf("limits len = %d: %#v", len(limits), limits)
	}
	byLabel := map[string]OpenAIQuotaLimit{}
	for _, limit := range limits {
		byLabel[limit.Label] = limit
	}
	primary := byLabel["5h"]
	if primary.Remaining != 75 || primary.PercentRemaining != 75 || primary.Window != "5h" {
		t.Fatalf("primary used_percent not converted: %#v", primary)
	}
	weekly := byLabel["Weekly"]
	if weekly.Remaining != 0 || weekly.PercentRemaining != 0 || weekly.Window != "weekly" || weekly.ResetAt == "" {
		t.Fatalf("weekly used_percent/reset not converted: %#v", weekly)
	}
	monthly := byLabel["Monthly"]
	if monthly.Remaining != 90 || monthly.PercentRemaining != 90 {
		t.Fatalf("monthly used_percent not converted: %#v", monthly)
	}
}

func TestParseWhamUsageQuotaLimitsTreatsUsedPercentAsWholePercent(t *testing.T) {
	limits := parseWhamUsageQuotaLimits([]byte(`{
		"rate_limit":{
			"primary_window":{"used_percent":1,"limit_window_seconds":18000},
			"secondary_window":{"used_percent":0,"limit_window_seconds":604800}
		}
	}`))
	byLabel := map[string]OpenAIQuotaLimit{}
	for _, limit := range limits {
		byLabel[limit.Label] = limit
	}
	primary := byLabel["5h"]
	if primary.Used != 1 || primary.Remaining != 99 || primary.PercentRemaining != 99 {
		t.Fatalf("used_percent=1 should mean 1%% used, got %#v", primary)
	}
	if !openAIQuotaLimitsHaveRemaining(limits) {
		t.Fatalf("5h with 99%% remaining should be usable: %#v", limits)
	}
}

func TestCheckOpenAIAccountRefreshesExpiredAccessTokenBeforeUsage(t *testing.T) {
	var refreshPayload url.Values
	var usageAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse refresh request: %v", err)
			}
			refreshPayload = r.Form
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fresh-token","refresh_token":"fresh-refresh","expires_in":3600}`))
		case "/backend-api/wham/usage":
			usageAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"usage","rate_limits":[{"name":"5h","remaining":10,"limit":100,"reset_after_seconds":3600}]}`))
		default:
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
	}))
	defer upstream.Close()

	withEnv(t, map[string]string{
		"PERSISTENCE":      "memory",
		"CHATGPT_API_BASE": upstream.URL + "/backend-api",
		"OPENAI_AUTH_BASE": upstream.URL,
	})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)
	server.mu.Lock()
	channel := server.findChannel("chn_1002")
	channel.OpenAIAccounts = []OpenAIAccount{
		{
			ID:           "oaiacc_refresh",
			Email:        "refresh@example.com",
			AccessToken:  "expired-token",
			RefreshToken: "refresh-token",
			ExpiresAt:    time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
			Status:       "unchecked",
		},
	}
	server.mu.Unlock()

	response := perform(router, http.MethodPost, "/api/channels/chn_1002/openai-accounts/check", `{}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("check accounts status = %d body = %s", response.Code, response.Body.String())
	}
	if refreshPayload.Get("client_id") != openAIAuthClientID ||
		refreshPayload.Get("grant_type") != "refresh_token" ||
		refreshPayload.Get("refresh_token") != "refresh-token" ||
		refreshPayload.Get("scope") != "openid profile email" {
		t.Fatalf("unexpected refresh payload = %#v", refreshPayload)
	}
	if usageAuth != "Bearer fresh-token" {
		t.Fatalf("usage auth = %s", usageAuth)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	channel = server.findChannel("chn_1002")
	if channel.OpenAIAccounts[0].Status != "healthy" || channel.OpenAIAccounts[0].AccessToken != "fresh-token" || channel.OpenAIAccounts[0].RefreshToken != "fresh-refresh" {
		t.Fatalf("refreshed account was not stored healthy: %#v", channel.OpenAIAccounts[0])
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

func TestOpenAICompatibleRoutesNormalizeRepeatedSlashes(t *testing.T) {
	withEnv(t, map[string]string{"PERSISTENCE": "memory"})
	server, router := testServerRouter(t)
	seedGatewayFixtures(server)

	chatBody := `{"model":"ds","messages":[{"role":"user","content":"hello"}]}`
	for _, path := range []string{
		"//chat/completions",
		"//v1/chat/completions",
		"/prefix/v1/chat/completions",
		"/admin/v1/chat/completions",
		"/api/v1/chat/completions",
		"/api/coding/v3/chat/completions",
	} {
		chat := perform(router, http.MethodPost, path, chatBody, map[string]string{"Authorization": "Bearer cat_fixture_live_secret"})
		if chat.Code != http.StatusOK {
			t.Fatalf("chat with repeated slashes at %s status = %d body = %s", path, chat.Code, chat.Body.String())
		}
		if !bytes.Contains(chat.Body.Bytes(), []byte(`"model":"deepseek-v4"`)) {
			t.Fatalf("chat with repeated slashes at %s did not resolve model: %s", path, chat.Body.String())
		}
	}
}
