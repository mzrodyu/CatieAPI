package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

type AppState struct {
	Users       []User       `json:"users"`
	APIKeys     []APIKey     `json:"apiKeys"`
	Channels    []Channel    `json:"channels"`
	Models      []Model      `json:"models"`
	QuotaLedger []QuotaEntry `json:"quotaLedger"`
	Logs        []RequestLog `json:"logs"`
	Accounts    []Account    `json:"accounts,omitempty"`
	Settings    AppSettings  `json:"settings,omitempty"`
}

type AppSettings struct {
	Discord DiscordSettings `json:"discord,omitempty"`
	Auth    AuthSettings    `json:"auth,omitempty"`
}

type AuthSettings struct {
	Managed             bool    `json:"managed,omitempty"`
	RegistrationEnabled bool    `json:"registrationEnabled"`
	RegistrationMode    string  `json:"registrationMode,omitempty"`
	DefaultBalance      float64 `json:"defaultBalance"`
}

type Account struct {
	ID            string `json:"id"`
	UserID        string `json:"userId"`
	Username      string `json:"username"`
	Email         string `json:"email,omitempty"`
	DiscordUserID string `json:"discordUserId,omitempty"`
	PasswordHash  string `json:"passwordHash"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	CreatedAt     string `json:"createdAt"`
	LastLoginAt   string `json:"lastLoginAt,omitempty"`
}

type DiscordSettings struct {
	Managed         bool   `json:"managed,omitempty"`
	Enabled         bool   `json:"enabled"`
	ClientID        string `json:"clientId,omitempty"`
	ClientSecret    string `json:"clientSecret,omitempty"`
	RedirectURI     string `json:"redirectUri,omitempty"`
	AllowedGuildID  string `json:"allowedGuildId,omitempty"`
	AllowedRoleID   string `json:"allowedRoleId,omitempty"`
	AuthSuccessURL  string `json:"authSuccessUrl,omitempty"`
	SessionTTLHours int    `json:"sessionTtlHours,omitempty"`
}

type PublicDiscordSettings struct {
	Enabled         bool   `json:"enabled"`
	ClientID        string `json:"clientId"`
	ClientSecretSet bool   `json:"clientSecretSet"`
	RedirectURI     string `json:"redirectUri"`
	AllowedGuildID  string `json:"allowedGuildId"`
	AllowedRoleID   string `json:"allowedRoleId"`
	AuthSuccessURL  string `json:"authSuccessUrl"`
	SessionTTLHours int    `json:"sessionTtlHours"`
}

type User struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Email         string  `json:"email"`
	Role          string  `json:"role"`
	Status        string  `json:"status"`
	Balance       float64 `json:"balance"`
	RequestsToday int     `json:"requestsToday"`
	TotalRequests int     `json:"totalRequests"`
	CreatedAt     string  `json:"createdAt"`
	LastLoginAt   string  `json:"lastLoginAt"`
	Note          string  `json:"note"`
}

type APIKey struct {
	ID           string `json:"id"`
	UserID       string `json:"userId"`
	Name         string `json:"name"`
	Prefix       string `json:"prefix"`
	Hash         string `json:"hash,omitempty"`
	Status       string `json:"status"`
	CreatedAt    string `json:"createdAt"`
	LastUsedAt   string `json:"lastUsedAt"`
	RequestCount int    `json:"requestCount"`
}

type PublicAPIKey struct {
	ID           string `json:"id"`
	UserID       string `json:"userId"`
	Name         string `json:"name"`
	Prefix       string `json:"prefix"`
	Status       string `json:"status"`
	CreatedAt    string `json:"createdAt"`
	LastUsedAt   string `json:"lastUsedAt"`
	RequestCount int    `json:"requestCount"`
}

type Channel struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	BaseURL        string   `json:"baseUrl"`
	UpstreamAPIKey string   `json:"upstreamApiKey,omitempty"`
	Status         string   `json:"status"`
	Priority       int      `json:"priority"`
	Weight         int      `json:"weight"`
	Models         []string `json:"models"`
	LastCheckedAt  string   `json:"lastCheckedAt,omitempty"`
	LastError      string   `json:"lastError,omitempty"`
}

type PublicChannel struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	BaseURL        string   `json:"baseUrl"`
	UpstreamKeySet bool     `json:"upstreamKeySet"`
	Status         string   `json:"status"`
	Priority       int      `json:"priority"`
	Weight         int      `json:"weight"`
	Models         []string `json:"models"`
	LastCheckedAt  string   `json:"lastCheckedAt"`
	LastError      string   `json:"lastError"`
}

type Model struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Vendor            string   `json:"vendor"`
	Aliases           []string `json:"aliases"`
	Category          string   `json:"category"`
	Description       string   `json:"description"`
	Price             string   `json:"price"`
	InputPricePer1K   float64  `json:"inputPricePer1K"`
	OutputPricePer1K  float64  `json:"outputPricePer1K"`
	PricingConfigured bool     `json:"pricingConfigured"`
	Context           string   `json:"context"`
	Status            string   `json:"status"`
	Recommended       bool     `json:"recommended"`
}

type RequestLog struct {
	ID           string  `json:"id"`
	UserID       *string `json:"userId"`
	APIKeyPrefix *string `json:"apiKeyPrefix"`
	Model        *string `json:"model"`
	Channel      *string `json:"channel"`
	Status       string  `json:"status"`
	Cost         float64 `json:"cost"`
	LatencyMS    int64   `json:"latencyMs"`
	ErrorCode    string  `json:"errorCode,omitempty"`
	CreatedAt    string  `json:"createdAt"`
}

type QuotaEntry struct {
	ID        string  `json:"id"`
	UserID    string  `json:"userId"`
	RequestID string  `json:"requestId"`
	Model     string  `json:"model"`
	Amount    float64 `json:"amount"`
	Reason    string  `json:"reason"`
	CreatedAt string  `json:"createdAt"`
}

type Server struct {
	mu                    sync.Mutex
	state                 AppState
	dataFile              string
	databaseURL           string
	staticDir             string
	db                    *sql.DB
	persistence           string
	corsOrigin            string
	adminToken            string
	secretKey             []byte
	requestLimitPerMinute int
	providerMode          string
	upstreamAPIKey        string
	upstreamTimeout       time.Duration
	httpClient            *http.Client
	discordClientID       string
	discordClientSecret   string
	discordRedirectURI    string
	discordAllowedGuildID string
	discordAllowedRoleID  string
	discordOAuthBase      string
	discordAPIBase        string
	authSuccessURL        string
	sessionTTL            time.Duration
	rateLimitBuckets      map[string]int
	idempotencyCache      map[string]CachedResponse
	authStates            map[string]time.Time
	sessions              map[string]Session
}

type CachedResponse struct {
	Status    int         `json:"status"`
	Body      interface{} `json:"body"`
	CreatedAt string      `json:"createdAt"`
}

type Session struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	Avatar    string `json:"avatar"`
	GuildID   string `json:"guildId"`
	RoleID    string `json:"roleId"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}

type AuthContext struct {
	User *User
	Key  *APIKey
}

type ChatRequest struct {
	Model    string                 `json:"model"`
	Stream   bool                   `json:"stream"`
	Messages []ChatMessage          `json:"messages"`
	Payload  map[string]interface{} `json:"-"`
}

type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

func (request *ChatRequest) UnmarshalJSON(data []byte) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	var typed struct {
		Model    string        `json:"model"`
		Stream   bool          `json:"stream"`
		Messages []ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &typed); err != nil {
		return err
	}
	request.Model = typed.Model
	request.Stream = typed.Stream
	request.Messages = typed.Messages
	request.Payload = payload
	return nil
}

type GatewayCall struct {
	RequestID string
	Model     Model
	Channel   Channel
	Body      ChatRequest
}

type DiscordTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

type DiscordUser struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	GlobalName    string `json:"global_name"`
	Discriminator string `json:"discriminator"`
	Avatar        string `json:"avatar"`
}

type DiscordGuildMember struct {
	User  DiscordUser `json:"user"`
	Roles []string    `json:"roles"`
}

type DiscordRuntimeConfig struct {
	ClientID       string
	ClientSecret   string
	RedirectURI    string
	AllowedGuildID string
	AllowedRoleID  string
	OAuthBase      string
	AuthSuccessURL string
	SessionTTL     time.Duration
}

func main() {
	loadDotEnv(".env")
	gin.SetMode(gin.ReleaseMode)

	server := NewServer()
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(server.requestMiddleware())
	router.Use(server.corsMiddleware())

	server.registerRoutes(router)

	addr := ":" + env("PORT", "8787")
	fmt.Printf("CatieAPI Go server listening on http://localhost%s\n", addr)
	if err := router.Run(addr); err != nil {
		panic(err)
	}
}

func NewServer() *Server {
	persistence := env("PERSISTENCE", "file")
	dataFile := env("DATA_FILE", "data/state.json")

	s := &Server{
		state:                 defaultState(),
		dataFile:              dataFile,
		databaseURL:           env("DATABASE_URL", ""),
		staticDir:             env("STATIC_DIR", "dist"),
		persistence:           persistence,
		corsOrigin:            normalizeCORSOriginConfig(env("CORS_ORIGIN", "*")),
		adminToken:            env("ADMIN_TOKEN", ""),
		secretKey:             deriveSecretKey(env("SECRET_KEY", "")),
		requestLimitPerMinute: envInt("REQUEST_LIMIT_PER_MINUTE", 60),
		providerMode:          env("PROVIDER_MODE", "mock"),
		upstreamAPIKey:        env("UPSTREAM_API_KEY", ""),
		upstreamTimeout:       time.Duration(envInt("UPSTREAM_TIMEOUT_SECONDS", 60)) * time.Second,
		httpClient:            &http.Client{Timeout: time.Duration(envInt("UPSTREAM_TIMEOUT_SECONDS", 60)) * time.Second},
		discordClientID:       env("DISCORD_CLIENT_ID", ""),
		discordClientSecret:   env("DISCORD_CLIENT_SECRET", ""),
		discordRedirectURI:    env("DISCORD_REDIRECT_URI", ""),
		discordAllowedGuildID: env("DISCORD_ALLOWED_GUILD_ID", ""),
		discordAllowedRoleID:  env("DISCORD_ALLOWED_ROLE_ID", ""),
		discordOAuthBase:      env("DISCORD_OAUTH_BASE", "https://discord.com/api/oauth2"),
		discordAPIBase:        env("DISCORD_API_BASE", "https://discord.com/api/v10"),
		authSuccessURL:        env("AUTH_SUCCESS_URL", ""),
		sessionTTL:            time.Duration(envInt("SESSION_TTL_HOURS", 168)) * time.Hour,
		rateLimitBuckets:      map[string]int{},
		idempotencyCache:      map[string]CachedResponse{},
		authStates:            map[string]time.Time{},
		sessions:              map[string]Session{},
	}
	s.initStorage()
	s.loadState()
	s.applyPersistedDiscordSettings()
	return s
}

func (s *Server) registerRoutes(router *gin.Engine) {
	api := router.Group("/api")
	api.GET("/health", s.health)
	api.GET("/config/status", s.configStatus)
	api.GET("/auth/status", s.authStatus)
	api.POST("/auth/setup", s.setupAdmin)
	api.POST("/auth/login", s.login)
	api.POST("/auth/register", s.registerAccount)
	api.GET("/auth/discord/start", s.discordStart)
	api.GET("/auth/discord/callback", s.discordCallback)
	api.GET("/auth/session", s.currentSession)
	api.POST("/auth/logout", s.logout)
	api.GET("/catalog/models", s.publicModelCatalog)

	account := api.Group("/account")
	account.Use(s.accountMiddleware())
	account.GET("/me", s.accountMe)
	account.POST("/api-keys", s.createOwnAPIKey)
	account.PATCH("/profile", s.updateAccountProfile)

	admin := api.Group("")
	admin.Use(s.adminMiddleware())
	admin.GET("/overview", s.overview)
	admin.GET("/users", s.listUsers)
	admin.GET("/users/:id", s.getUser)
	admin.PATCH("/users/:id", s.updateUser)
	admin.POST("/users/bulk", s.bulkUpdateUsers)
	admin.POST("/users/:id/api-keys", s.createAPIKey)
	admin.PATCH("/api-keys/:id", s.updateAPIKey)
	admin.DELETE("/api-keys/:id", s.deleteAPIKey)
	admin.GET("/channels", s.listChannels)
	admin.POST("/channels", s.createChannel)
	admin.PATCH("/channels/:id", s.updateChannel)
	admin.DELETE("/channels/:id", s.deleteChannel)
	admin.POST("/channels/:id/check", s.checkChannel)
	admin.POST("/channels/:id/sync-models", s.syncChannelModels)
	admin.GET("/models", s.listModels)
	admin.POST("/models", s.createModel)
	admin.PATCH("/models/:id", s.updateModel)
	admin.GET("/logs", s.listLogs)
	admin.GET("/quota-ledger", s.quotaLedger)
	admin.GET("/settings/discord", s.getDiscordSettings)
	admin.PATCH("/settings/discord", s.updateDiscordSettings)
	admin.GET("/settings/auth", s.getAuthSettings)
	admin.PATCH("/settings/auth", s.updateAuthSettings)

	router.GET("/v1/models", s.openAIModels)
	router.GET("/v1/models/:id", s.openAIModel)
	router.POST("/v1/chat/completions", s.chatCompletions)
	router.POST("/v1/completions", s.completions)
	router.POST("/v1/responses", s.responses)
	router.POST("/v1/embeddings", s.embeddings)
	router.GET("/models", s.openAIModels)
	router.GET("/models/:id", s.openAIModel)
	router.POST("/chat/completions", s.chatCompletions)
	router.POST("/completions", s.completions)
	router.POST("/responses", s.responses)
	router.POST("/embeddings", s.embeddings)

	router.NoRoute(func(c *gin.Context) {
		if s.serveStatic(c) {
			return
		}
		s.openAIError(c, http.StatusNotFound, "route_not_found", fmt.Sprintf("Route not found: %s %s", c.Request.Method, c.Request.URL.Path), "invalid_request_error", nil)
	})
}

func (s *Server) requestMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("x-request-id")
		if requestID == "" {
			requestID = newID("req")
		}
		c.Set("requestId", requestID)
		c.Header("x-request-id", requestID)
		c.Header("x-content-type-options", "nosniff")
		c.Next()
	}
}

func (s *Server) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		allowedOrigin := matchCORSOrigin(s.corsOrigin, c.GetHeader("Origin"))
		if allowedOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowedOrigin)
		}
		if allowedOrigin != "" && allowedOrigin != "*" {
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-API-Key, X-Request-ID")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func normalizeCORSOriginConfig(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(strings.ToLower(value), "your-domain.example") {
		return "*"
	}
	seen := map[string]bool{}
	origins := []string{}
	for _, item := range strings.Split(value, ",") {
		origin := strings.TrimRight(strings.TrimSpace(item), "/")
		if origin == "" {
			continue
		}
		if origin == "*" {
			return "*"
		}
		if !seen[origin] {
			seen[origin] = true
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return "*"
	}
	return strings.Join(origins, ",")
}

func matchCORSOrigin(config string, requestOrigin string) string {
	if config == "*" {
		return "*"
	}
	requestOrigin = strings.TrimRight(strings.TrimSpace(requestOrigin), "/")
	if requestOrigin == "" {
		return ""
	}
	for _, allowed := range strings.Split(config, ",") {
		if requestOrigin == allowed {
			return requestOrigin
		}
	}
	return ""
}

func (s *Server) adminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c.GetHeader("Authorization"))
		if s.adminToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) == 1 {
			c.Next()
			return
		}
		if session, ok := s.sessionFromRequest(c); ok && session.Role == "admin" {
			c.Next()
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "Administrator login required",
				"type":    "invalid_request_error",
				"code":    "admin_login_required",
			},
		})
		c.Abort()
	}
}

func (s *Server) accountMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := s.sessionFromRequest(c); ok {
			c.Next()
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "Login required",
				"type":    "invalid_request_error",
				"code":    "login_required",
			},
		})
		c.Abort()
	}
}

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"name":         "CatieAPI",
		"mode":         s.persistence,
		"providerMode": s.providerMode,
		"time":         now(),
	})
}

func (s *Server) configStatus(c *gin.Context) {
	s.mu.Lock()
	discordEnabled := s.discordLoginEnabledLocked()
	discordGuildGate := s.discordAllowedGuildID != ""
	discordRoleGate := s.discordAllowedRoleID != ""
	s.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"runtime":                 "go",
		"framework":               "gin",
		"storage":                 s.persistence,
		"dataFile":                nullableString(s.persistence == "file", s.dataFile),
		"database":                nullableString(s.persistence == "postgres", "postgres"),
		"staticDir":               s.staticDir,
		"providerMode":            s.providerMode,
		"upstreamConfigured":      s.upstreamAPIKey != "",
		"upstreamTimeout":         int(s.upstreamTimeout.Seconds()),
		"secretEncryptionEnabled": len(s.secretKey) > 0,
		"discordLoginEnabled":     discordEnabled,
		"discordGuildGate":        discordGuildGate,
		"discordRoleGate":         discordRoleGate,
		"corsOrigin":              s.corsOrigin,
		"adminAuthEnabled":        s.adminToken != "",
		"requestLimitPerMinute":   s.requestLimitPerMinute,
		"features": gin.H{
			"openAIModels":    true,
			"chatCompletions": true,
			"stream":          true,
			"aliases":         true,
			"idempotency":     true,
			"quotaLedger":     true,
			"apiKeyCreate":    true,
			"channelToggle":   true,
			"modelToggle":     true,
			"adminAuth":       s.adminToken != "",
			"discordLogin":    discordEnabled,
		},
	})
}

func (s *Server) authStatus(c *gin.Context) {
	session, authenticated := s.sessionFromRequest(c)
	s.mu.Lock()
	initialized := len(s.state.Accounts) > 0
	registrationEnabled := s.registrationEnabledLocked()
	registrationMode := s.registrationModeLocked()
	discordEnabled := s.discordLoginEnabledLocked()
	s.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"initialized":         initialized,
		"authenticated":       authenticated,
		"registrationEnabled": registrationEnabled,
		"registrationMode":    registrationMode,
		"discordEnabled":      discordEnabled,
		"session":             nullableSession(authenticated, session),
	})
}

func (s *Server) setupAdmin(c *gin.Context) {
	var body struct {
		Username      string `json:"username"`
		Password      string `json:"password"`
		DisplayName   string `json:"displayName"`
		Email         string `json:"email"`
		DiscordUserID string `json:"discordUserId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		validationError(c, "请填写管理员账号和密码")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	body.Email = strings.TrimSpace(body.Email)
	body.DiscordUserID = strings.TrimSpace(body.DiscordUserID)
	if message := validateAccountInput(body.Username, body.Password, body.Email); message != "" {
		validationError(c, message)
		return
	}
	if body.DiscordUserID != "" && !digitsOnly(body.DiscordUserID) {
		validationError(c, "Discord 用户 ID 只能包含数字")
		return
	}
	if body.DisplayName == "" {
		body.DisplayName = body.Username
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "密码加密失败"}})
		return
	}

	s.mu.Lock()
	if len(s.state.Accounts) > 0 {
		s.mu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "站点已经完成初始化"}})
		return
	}
	user := s.findUser("usr_1001")
	if user == nil {
		s.state.Users = append(s.state.Users, User{ID: "usr_1001", CreatedAt: now(), Status: "active", Role: "admin"})
		user = s.findUser("usr_1001")
	}
	user.Name = body.DisplayName
	user.Email = body.Email
	user.Role = "admin"
	user.Status = "active"
	user.LastLoginAt = now()
	account := Account{
		ID:            newID("acct"),
		UserID:        user.ID,
		Username:      body.Username,
		Email:         body.Email,
		DiscordUserID: body.DiscordUserID,
		PasswordHash:  string(passwordHash),
		Role:          "admin",
		Status:        "active",
		CreatedAt:     now(),
		LastLoginAt:   now(),
	}
	s.state.Accounts = append(s.state.Accounts, account)
	s.saveStateLocked()
	s.mu.Unlock()

	session := s.createAccountSession(account, body.DisplayName, "password")
	s.setSessionCookie(c, session)
	c.JSON(http.StatusCreated, gin.H{"account": publicAccount(account), "session": session})
}

func (s *Server) login(c *gin.Context) {
	var body struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		validationError(c, "请填写账号和密码")
		return
	}
	identifier := strings.ToLower(strings.TrimSpace(body.Identifier))
	if identifier == "" || body.Password == "" {
		validationError(c, "请填写账号和密码")
		return
	}

	s.mu.Lock()
	account := s.findAccountByIdentifierLocked(identifier)
	if account == nil {
		s.mu.Unlock()
		invalidLogin(c)
		return
	}
	accountCopy := *account
	user := s.findUser(account.UserID)
	displayName := account.Username
	if user != nil && user.Name != "" {
		displayName = user.Name
	}
	s.mu.Unlock()

	if accountCopy.Status != "active" || bcrypt.CompareHashAndPassword([]byte(accountCopy.PasswordHash), []byte(body.Password)) != nil {
		invalidLogin(c)
		return
	}

	s.mu.Lock()
	if current := s.findAccountByIDLocked(accountCopy.ID); current != nil {
		current.LastLoginAt = now()
		accountCopy.LastLoginAt = current.LastLoginAt
	}
	if currentUser := s.findUser(accountCopy.UserID); currentUser != nil {
		currentUser.LastLoginAt = accountCopy.LastLoginAt
	}
	s.saveStateLocked()
	s.mu.Unlock()

	session := s.createAccountSession(accountCopy, displayName, "password")
	s.setSessionCookie(c, session)
	c.JSON(http.StatusOK, gin.H{"account": publicAccount(accountCopy), "session": session})
}

func (s *Server) registerAccount(c *gin.Context) {
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		validationError(c, "请完整填写注册信息")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	body.Email = strings.TrimSpace(body.Email)
	s.mu.Lock()
	initialized := len(s.state.Accounts) > 0
	registrationEnabled := s.registrationEnabledLocked()
	registrationMode := s.registrationModeLocked()
	s.mu.Unlock()
	if !initialized {
		c.JSON(http.StatusPreconditionRequired, gin.H{"error": gin.H{"message": "请先初始化站点管理员"}})
		return
	}
	if !registrationEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "当前站点未开放注册"}})
		return
	}
	if registrationMode == "email" && body.Username == "" {
		body.Username = usernameFromEmail(body.Email)
	}
	if registrationMode == "discord" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "当前站点只允许 Discord 注册"}})
		return
	}
	if registrationMode == "email" && body.Email == "" {
		validationError(c, "邮箱注册需要填写邮箱")
		return
	}
	if message := validateAccountInput(body.Username, body.Password, body.Email); message != "" {
		validationError(c, message)
		return
	}
	if body.DisplayName == "" {
		body.DisplayName = body.Username
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "密码加密失败"}})
		return
	}

	s.mu.Lock()
	if !s.registrationEnabledLocked() {
		s.mu.Unlock()
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "当前站点未开放注册"}})
		return
	}
	if s.findAccountByIdentifierLocked(strings.ToLower(body.Username)) != nil ||
		(body.Email != "" && s.findAccountByIdentifierLocked(strings.ToLower(body.Email)) != nil) {
		s.mu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "账号或邮箱已被使用"}})
		return
	}
	userID := newID("usr")
	user := User{
		ID:          userID,
		Name:        body.DisplayName,
		Email:       body.Email,
		Role:        "user",
		Status:      "active",
		Balance:     s.defaultRegistrationBalanceLocked(),
		CreatedAt:   now(),
		LastLoginAt: now(),
	}
	account := Account{
		ID:           newID("acct"),
		UserID:       userID,
		Username:     body.Username,
		Email:        body.Email,
		PasswordHash: string(passwordHash),
		Role:         "user",
		Status:       "active",
		CreatedAt:    now(),
		LastLoginAt:  now(),
	}
	s.state.Users = append(s.state.Users, user)
	s.appendInitialQuotaLocked(&user)
	s.state.Accounts = append(s.state.Accounts, account)
	s.saveStateLocked()
	s.mu.Unlock()

	session := s.createAccountSession(account, body.DisplayName, "password")
	s.setSessionCookie(c, session)
	c.JSON(http.StatusCreated, gin.H{"account": publicAccount(account), "session": session})
}

func (s *Server) getAuthSettings(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"auth": gin.H{
		"registrationEnabled": s.registrationEnabledLocked(),
		"registrationMode":    s.registrationModeLocked(),
		"defaultBalance":      s.defaultRegistrationBalanceLocked(),
	}})
}

func (s *Server) updateAuthSettings(c *gin.Context) {
	var body struct {
		RegistrationEnabled bool     `json:"registrationEnabled"`
		RegistrationMode    *string  `json:"registrationMode"`
		DefaultBalance      *float64 `json:"defaultBalance"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		validationError(c, "无效的认证设置")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	registrationMode := s.registrationModeLocked()
	if body.RegistrationMode != nil {
		registrationMode = normalizeRegistrationMode(*body.RegistrationMode)
	}
	defaultBalance := s.defaultRegistrationBalanceLocked()
	if body.DefaultBalance != nil {
		if *body.DefaultBalance < 0 || *body.DefaultBalance > 1_000_000_000 {
			validationError(c, "新用户初始额度必须在 0 到 1000000000 之间")
			return
		}
		defaultBalance = round4(*body.DefaultBalance)
	}
	s.state.Settings.Auth = AuthSettings{
		Managed:             true,
		RegistrationEnabled: body.RegistrationEnabled,
		RegistrationMode:    registrationMode,
		DefaultBalance:      defaultBalance,
	}
	s.saveStateLocked()
	c.JSON(http.StatusOK, gin.H{"auth": gin.H{
		"registrationEnabled": body.RegistrationEnabled,
		"registrationMode":    registrationMode,
		"defaultBalance":      defaultBalance,
	}})
}

func (s *Server) getDiscordSettings(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"discord": s.publicDiscordSettingsLocked(requestOrigin(c))})
}

func (s *Server) updateDiscordSettings(c *gin.Context) {
	var body struct {
		Enabled           bool   `json:"enabled"`
		ClientID          string `json:"clientId"`
		ClientSecret      string `json:"clientSecret"`
		ClearClientSecret bool   `json:"clearClientSecret"`
		RedirectURI       string `json:"redirectUri"`
		AllowedGuildID    string `json:"allowedGuildId"`
		AllowedRoleID     string `json:"allowedRoleId"`
		AuthSuccessURL    string `json:"authSuccessUrl"`
		SessionTTLHours   int    `json:"sessionTtlHours"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body", "invalid_request_error", nil)
		return
	}

	body.ClientID = strings.TrimSpace(body.ClientID)
	body.ClientSecret = strings.TrimSpace(body.ClientSecret)
	body.RedirectURI = strings.TrimSpace(body.RedirectURI)
	body.AllowedGuildID = strings.TrimSpace(body.AllowedGuildID)
	body.AllowedRoleID = strings.TrimSpace(body.AllowedRoleID)
	body.AuthSuccessURL = strings.TrimSpace(body.AuthSuccessURL)
	if body.RedirectURI == "" {
		body.RedirectURI = defaultDiscordRedirectURI(c)
	}
	if body.AuthSuccessURL == "" {
		body.AuthSuccessURL = requestOrigin(c) + "/"
	}
	if body.SessionTTLHours == 0 {
		body.SessionTTLHours = 168
	}
	if body.SessionTTLHours < 1 || body.SessionTTLHours > 8760 {
		validationError(c, "Session 有效期必须在 1 到 8760 小时之间")
		return
	}
	if body.AllowedRoleID != "" && body.AllowedGuildID == "" {
		validationError(c, "设置身份组 ID 时必须同时填写服务器 ID")
		return
	}
	if body.ClientID != "" && !digitsOnly(body.ClientID) {
		validationError(c, "Discord Client ID 只能包含数字")
		return
	}
	if body.AllowedGuildID != "" && !digitsOnly(body.AllowedGuildID) {
		validationError(c, "Discord 服务器 ID 只能包含数字")
		return
	}
	if body.AllowedRoleID != "" && !digitsOnly(body.AllowedRoleID) {
		validationError(c, "Discord 身份组 ID 只能包含数字")
		return
	}
	if body.RedirectURI != "" && !validHTTPURL(body.RedirectURI) {
		validationError(c, "Discord 回调地址必须是完整的 http:// 或 https:// 地址")
		return
	}
	if body.AuthSuccessURL != "" && !validHTTPURL(body.AuthSuccessURL) {
		validationError(c, "登录成功跳转地址必须是完整的 http:// 或 https:// 地址")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	plainSecret := s.discordClientSecret
	storedSecret := s.state.Settings.Discord.ClientSecret
	if body.ClearClientSecret {
		plainSecret = ""
		storedSecret = ""
	}
	if body.ClientSecret != "" {
		if len(s.secretKey) == 0 {
			validationError(c, "保存 Discord Client Secret 前请先设置 SECRET_KEY")
			return
		}
		protected, err := s.protectSecret(body.ClientSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Discord Client Secret 加密失败"}})
			return
		}
		plainSecret = body.ClientSecret
		storedSecret = protected
	} else if storedSecret == "" && plainSecret != "" {
		if len(s.secretKey) == 0 {
			validationError(c, "保存 Discord 配置前请先设置 SECRET_KEY")
			return
		}
		protected, err := s.protectSecret(plainSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Discord Client Secret 加密失败"}})
			return
		}
		storedSecret = protected
	}
	if body.Enabled && (body.ClientID == "" || plainSecret == "" || body.RedirectURI == "" || body.AuthSuccessURL == "") {
		validationError(c, "启用 Discord 登录前请填写 Client ID、Client Secret、回调地址和登录成功跳转地址")
		return
	}

	s.state.Settings.Discord = DiscordSettings{
		Managed:         true,
		Enabled:         body.Enabled,
		ClientID:        body.ClientID,
		ClientSecret:    storedSecret,
		RedirectURI:     body.RedirectURI,
		AllowedGuildID:  body.AllowedGuildID,
		AllowedRoleID:   body.AllowedRoleID,
		AuthSuccessURL:  body.AuthSuccessURL,
		SessionTTLHours: body.SessionTTLHours,
	}
	s.discordClientID = body.ClientID
	s.discordClientSecret = plainSecret
	s.discordRedirectURI = body.RedirectURI
	s.discordAllowedGuildID = body.AllowedGuildID
	s.discordAllowedRoleID = body.AllowedRoleID
	s.authSuccessURL = body.AuthSuccessURL
	s.sessionTTL = time.Duration(body.SessionTTLHours) * time.Hour
	if !body.Enabled {
		s.discordClientID = ""
	}
	s.saveStateLocked()

	c.JSON(http.StatusOK, gin.H{"discord": s.publicDiscordSettingsLocked(requestOrigin(c))})
}

func (s *Server) publicDiscordSettingsLocked(origin string) PublicDiscordSettings {
	settings := s.state.Settings.Discord
	enabled := s.discordLoginEnabledLocked()
	redirectURI := s.discordRedirectURI
	authSuccessURL := s.authSuccessURL
	if redirectURI == "" {
		redirectURI = origin + "/api/auth/discord/callback"
	}
	if authSuccessURL == "" {
		authSuccessURL = origin + "/"
	}
	if !settings.Managed {
		return PublicDiscordSettings{
			Enabled:         enabled,
			ClientID:        s.discordClientID,
			ClientSecretSet: s.discordClientSecret != "",
			RedirectURI:     redirectURI,
			AllowedGuildID:  s.discordAllowedGuildID,
			AllowedRoleID:   s.discordAllowedRoleID,
			AuthSuccessURL:  authSuccessURL,
			SessionTTLHours: int(s.sessionTTL.Hours()),
		}
	}
	redirectURI = settings.RedirectURI
	authSuccessURL = settings.AuthSuccessURL
	if redirectURI == "" {
		redirectURI = origin + "/api/auth/discord/callback"
	}
	if authSuccessURL == "" {
		authSuccessURL = origin + "/"
	}
	return PublicDiscordSettings{
		Enabled:         settings.Enabled && enabled,
		ClientID:        settings.ClientID,
		ClientSecretSet: settings.ClientSecret != "",
		RedirectURI:     redirectURI,
		AllowedGuildID:  settings.AllowedGuildID,
		AllowedRoleID:   settings.AllowedRoleID,
		AuthSuccessURL:  authSuccessURL,
		SessionTTLHours: settings.SessionTTLHours,
	}
}

func (s *Server) discordStart(c *gin.Context) {
	config := s.discordRuntimeConfig(c)
	if !config.enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Discord login is not configured"}})
		return
	}
	if config.AllowedRoleID != "" && config.AllowedGuildID == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "DISCORD_ALLOWED_GUILD_ID is required when DISCORD_ALLOWED_ROLE_ID is set"}})
		return
	}
	state := randomHex(16)
	expiresAt := time.Now().Add(10 * time.Minute)
	s.mu.Lock()
	s.authStates[state] = expiresAt
	s.mu.Unlock()

	values := url.Values{}
	values.Set("client_id", config.ClientID)
	values.Set("redirect_uri", config.RedirectURI)
	values.Set("response_type", "code")
	values.Set("scope", "identify guilds.members.read")
	values.Set("state", state)
	c.Redirect(http.StatusFound, strings.TrimRight(config.OAuthBase, "/")+"/authorize?"+values.Encode())
}

func (s *Server) discordCallback(c *gin.Context) {
	config := s.discordRuntimeConfig(c)
	if !config.enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Discord login is not configured"}})
		return
	}
	if config.AllowedRoleID != "" && config.AllowedGuildID == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "DISCORD_ALLOWED_GUILD_ID is required when DISCORD_ALLOWED_ROLE_ID is set"}})
		return
	}
	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" || !s.consumeAuthState(state) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid Discord OAuth state"}})
		return
	}

	token, err := s.exchangeDiscordCode(code, config)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	user, err := s.fetchDiscordUser(token.AccessToken)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	s.mu.Lock()
	boundAccount := s.findAccountByDiscordIDLocked(user.ID)
	var boundAccountCopy *Account
	boundDisplayName := user.GlobalName
	if boundAccount != nil && boundAccount.Status == "active" {
		copy := *boundAccount
		boundAccountCopy = &copy
		if localUser := s.findUser(boundAccount.UserID); localUser != nil && localUser.Name != "" {
			boundDisplayName = localUser.Name
		}
	}
	s.mu.Unlock()
	if boundAccountCopy != nil {
		session := s.createAccountSession(*boundAccountCopy, boundDisplayName, "discord")
		s.setSessionCookie(c, session)
		c.Redirect(http.StatusFound, config.AuthSuccessURL)
		return
	}

	roleID := ""
	if config.AllowedGuildID != "" {
		member, err := s.fetchDiscordGuildMember(token.AccessToken, config.AllowedGuildID)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Discord server membership required"}})
			return
		}
		if config.AllowedRoleID != "" && !containsString(member.Roles, config.AllowedRoleID) {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Discord role required"}})
			return
		}
		roleID = config.AllowedRoleID
	}

	s.mu.Lock()
	canRegisterWithDiscord := len(s.state.Accounts) > 0 && s.registrationEnabledLocked() && s.registrationModeLocked() == "discord"
	if canRegisterWithDiscord {
		displayName := user.GlobalName
		if displayName == "" {
			displayName = user.Username
		}
		if displayName == "" {
			displayName = "Discord User"
		}
		username := uniqueUsernameLocked(s, usernameFromDiscord(user), user.ID)
		userID := newID("usr")
		account := Account{
			ID:            newID("acct"),
			UserID:        userID,
			Username:      username,
			DiscordUserID: user.ID,
			Role:          "user",
			Status:        "active",
			CreatedAt:     now(),
			LastLoginAt:   now(),
		}
		s.state.Users = append(s.state.Users, User{
			ID:          userID,
			Name:        displayName,
			Role:        "user",
			Status:      "active",
			Balance:     s.defaultRegistrationBalanceLocked(),
			CreatedAt:   now(),
			LastLoginAt: now(),
		})
		s.appendInitialQuotaLocked(s.findUser(userID))
		s.state.Accounts = append(s.state.Accounts, account)
		s.saveStateLocked()
		s.mu.Unlock()

		session := s.createAccountSession(account, displayName, "discord")
		s.setSessionCookie(c, session)
		c.Redirect(http.StatusFound, config.AuthSuccessURL)
		return
	}
	s.mu.Unlock()

	session := s.createSession(user, config.AllowedGuildID, roleID, config.SessionTTL)
	s.setSessionCookie(c, session)
	c.Redirect(http.StatusFound, config.AuthSuccessURL)
}

func (s *Server) currentSession(c *gin.Context) {
	session, ok := s.sessionFromRequest(c)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"authenticated": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"authenticated": true, "session": session})
}

func (s *Server) logout(c *gin.Context) {
	cookie, err := c.Cookie("catie_session")
	if err == nil && cookie != "" {
		s.mu.Lock()
		delete(s.sessions, cookie)
		s.mu.Unlock()
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "catie_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) accountMe(c *gin.Context) {
	session, ok := s.sessionFromRequest(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Login required"}})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.findUser(session.UserID)
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}
	keys := []PublicAPIKey{}
	for _, key := range s.state.APIKeys {
		if key.UserID == user.ID {
			keys = append(keys, publicAPIKey(key))
		}
	}
	var account interface{}
	if stored := s.findAccountByUserIDLocked(user.ID); stored != nil {
		account = publicAccount(*stored)
	}
	c.JSON(http.StatusOK, gin.H{"user": user, "account": account, "apiKeys": keys, "session": session})
}

func (s *Server) updateAccountProfile(c *gin.Context) {
	session, ok := s.sessionFromRequest(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Login required"}})
		return
	}
	var body struct {
		Username        string `json:"username"`
		DisplayName     string `json:"displayName"`
		Email           string `json:"email"`
		DiscordUserID   string `json:"discordUserId"`
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		validationError(c, "无效的账号设置")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	body.Email = strings.TrimSpace(body.Email)
	body.DiscordUserID = strings.TrimSpace(body.DiscordUserID)
	if body.Username != "" {
		if message := validateUsername(body.Username); message != "" {
			validationError(c, message)
			return
		}
	}
	if body.Email != "" {
		address, err := mail.ParseAddress(body.Email)
		if err != nil || !strings.EqualFold(address.Address, body.Email) {
			validationError(c, "邮箱格式不正确")
			return
		}
	}
	if body.DiscordUserID != "" && !digitsOnly(body.DiscordUserID) {
		validationError(c, "Discord 用户 ID 只能包含数字")
		return
	}
	if body.NewPassword != "" && (len(body.NewPassword) < 8 || len(body.NewPassword) > 128) {
		validationError(c, "新密码长度必须在 8 到 128 个字符之间")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.findAccountByUserIDLocked(session.UserID)
	if account == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Account not found"}})
		return
	}
	if body.DiscordUserID != "" {
		if existing := s.findAccountByDiscordIDLocked(body.DiscordUserID); existing != nil && existing.ID != account.ID {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "该 Discord 用户 ID 已绑定其他账号"}})
			return
		}
	}
	if body.Username != "" && !strings.EqualFold(body.Username, account.Username) {
		if existing := s.findAccountByIdentifierLocked(strings.ToLower(body.Username)); existing != nil && existing.ID != account.ID {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "该账号已被使用"}})
			return
		}
		account.Username = body.Username
	}
	if body.Email != "" && !strings.EqualFold(body.Email, account.Email) {
		if existing := s.findAccountByIdentifierLocked(strings.ToLower(body.Email)); existing != nil && existing.ID != account.ID {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "该邮箱已被使用"}})
			return
		}
		account.Email = body.Email
	}
	if body.NewPassword != "" {
		if bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(body.CurrentPassword)) != nil {
			validationError(c, "当前密码不正确")
			return
		}
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "密码加密失败"}})
			return
		}
		account.PasswordHash = string(passwordHash)
	}
	account.DiscordUserID = body.DiscordUserID
	if user := s.findUser(account.UserID); user != nil {
		if body.DisplayName != "" {
			user.Name = body.DisplayName
		}
		user.Email = account.Email
	}
	s.saveStateLocked()
	c.JSON(http.StatusOK, gin.H{"account": publicAccount(*account)})
}

func (s *Server) publicModelCatalog(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	models := []Model{}
	for _, model := range s.state.Models {
		if model.Status == "available" {
			models = append(models, model)
		}
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

func (s *Server) createOwnAPIKey(c *gin.Context) {
	session, ok := s.sessionFromRequest(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Login required"}})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = c.ShouldBindJSON(&body)
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		body.Name = "API Key"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.findUser(session.UserID)
	if user == nil || user.Status == "disabled" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "User account is not available"}})
		return
	}
	secret := "cat_" + randomHex(24)
	key := APIKey{
		ID:        newID("key"),
		UserID:    user.ID,
		Name:      body.Name,
		Prefix:    secret[:12],
		Hash:      hashSecret(secret),
		Status:    "active",
		CreatedAt: now(),
	}
	s.state.APIKeys = append(s.state.APIKeys, key)
	s.saveStateLocked()
	c.JSON(http.StatusCreated, gin.H{"apiKey": publicAPIKey(key), "secret": secret})
}

func (s *Server) overview(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	activeUsers := 0
	totalBalance := 0.0
	requestsToday := 0
	successLogs := 0
	for _, user := range s.state.Users {
		if user.Status == "active" {
			activeUsers++
		}
		totalBalance += user.Balance
		requestsToday += user.RequestsToday
	}
	for _, log := range s.state.Logs {
		if log.Status == "success" {
			successLogs++
		}
	}

	successRate := 0
	if len(s.state.Logs) > 0 {
		successRate = int(float64(successLogs) / float64(len(s.state.Logs)) * 100)
	}

	c.JSON(http.StatusOK, gin.H{
		"activeUsers":   activeUsers,
		"channels":      len(s.state.Channels),
		"requestsToday": requestsToday,
		"totalBalance":  round4(totalBalance),
		"successRate":   successRate,
	})
}

func (s *Server) listUsers(c *gin.Context) {
	query := strings.ToLower(c.Query("q"))
	s.mu.Lock()
	defer s.mu.Unlock()

	users := []User{}
	for _, user := range s.state.Users {
		haystack := strings.ToLower(user.ID + " " + user.Name + " " + user.Email)
		if query == "" || strings.Contains(haystack, query) {
			users = append(users, user)
		}
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

func (s *Server) getUser(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user := s.findUser(c.Param("id"))
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	keys := []PublicAPIKey{}
	for _, key := range s.state.APIKeys {
		if key.UserID == user.ID {
			keys = append(keys, publicAPIKey(key))
		}
	}

	logs := []RequestLog{}
	for _, log := range s.state.Logs {
		if log.UserID != nil && *log.UserID == user.ID {
			logs = append(logs, log)
		}
	}

	c.JSON(http.StatusOK, gin.H{"user": user, "apiKeys": keys, "logs": logs})
}

func (s *Server) updateUser(c *gin.Context) {
	var patch map[string]interface{}
	if err := c.ShouldBindJSON(&patch); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body", "invalid_request_error", nil)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	user := s.findUser(c.Param("id"))
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	if value, ok := patch["status"].(string); ok {
		if !allowedString(value, "active", "disabled", "limited", "overdue") {
			validationError(c, "Invalid user status")
			return
		}
		if value != "active" && !s.hasActiveAdminAfterBulkLocked(map[string]struct{}{user.ID: {}}, "set_status", value) {
			validationError(c, "至少需要保留一个启用的管理员")
			return
		}
		user.Status = value
		s.syncAccountAccessLocked(user)
	}
	if value, ok := patch["role"].(string); ok {
		if !allowedString(value, "admin", "user") {
			validationError(c, "Invalid user role")
			return
		}
		if value == "user" && !s.hasActiveAdminAfterBulkLocked(map[string]struct{}{user.ID: {}}, "set_role", value) {
			validationError(c, "至少需要保留一个启用的管理员")
			return
		}
		user.Role = value
		s.syncAccountAccessLocked(user)
	}
	if value, ok := patch["note"].(string); ok {
		user.Note = value
	}
	if value, ok := asFloat(patch["balance"]); ok {
		if value < 0 {
			validationError(c, "Balance must be greater than or equal to 0")
			return
		}
		user.Balance = round4(value)
	}

	s.saveStateLocked()
	c.JSON(http.StatusOK, gin.H{"user": user})
}

func (s *Server) bulkUpdateUsers(c *gin.Context) {
	var body struct {
		UserIDs []string `json:"userIds"`
		Action  string   `json:"action"`
		Value   string   `json:"value"`
		Amount  float64  `json:"amount"`
		Reason  string   `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		validationError(c, "无效的批量操作")
		return
	}
	if len(body.UserIDs) == 0 {
		validationError(c, "请至少选择一个用户")
		return
	}
	if len(body.UserIDs) > 5000 {
		validationError(c, "单次最多处理 5000 个用户")
		return
	}

	ids := make(map[string]struct{}, len(body.UserIDs))
	for _, id := range body.UserIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			ids[id] = struct{}{}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	matched := make([]*User, 0, len(ids))
	for i := range s.state.Users {
		if _, ok := ids[s.state.Users[i].ID]; ok {
			matched = append(matched, &s.state.Users[i])
		}
	}
	if len(matched) != len(ids) {
		validationError(c, "部分用户不存在，请刷新列表后重试")
		return
	}

	switch body.Action {
	case "set_status":
		if !allowedString(body.Value, "active", "disabled", "limited", "overdue") {
			validationError(c, "无效的用户状态")
			return
		}
		if body.Value != "active" && !s.hasActiveAdminAfterBulkLocked(ids, body.Action, body.Value) {
			validationError(c, "至少需要保留一个启用的管理员")
			return
		}
	case "set_role":
		if !allowedString(body.Value, "admin", "user") {
			validationError(c, "无效的用户角色")
			return
		}
		if body.Value == "user" && !s.hasActiveAdminAfterBulkLocked(ids, body.Action, body.Value) {
			validationError(c, "至少需要保留一个启用的管理员")
			return
		}
	case "adjust_balance":
		if body.Amount == 0 || body.Amount < -1_000_000_000 || body.Amount > 1_000_000_000 {
			validationError(c, "额度调整值必须非零，且在允许范围内")
			return
		}
		for _, user := range matched {
			if round4(user.Balance+body.Amount) < 0 {
				validationError(c, "批量扣减会使部分用户额度小于 0")
				return
			}
		}
	default:
		validationError(c, "不支持的批量操作")
		return
	}

	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		reason = "管理员批量调整"
	}
	updated := make([]User, 0, len(matched))
	for _, user := range matched {
		switch body.Action {
		case "set_status":
			user.Status = body.Value
			s.syncAccountAccessLocked(user)
		case "set_role":
			user.Role = body.Value
			s.syncAccountAccessLocked(user)
		case "adjust_balance":
			user.Balance = round4(user.Balance + body.Amount)
			s.state.QuotaLedger = append(s.state.QuotaLedger, QuotaEntry{
				ID:        newID("quota"),
				UserID:    user.ID,
				Amount:    round4(body.Amount),
				Reason:    reason,
				CreatedAt: now(),
			})
		}
		updated = append(updated, *user)
	}
	s.saveStateLocked()
	c.JSON(http.StatusOK, gin.H{"users": updated, "updated": len(updated)})
}

func (s *Server) hasActiveAdminAfterBulkLocked(ids map[string]struct{}, action string, value string) bool {
	for i := range s.state.Users {
		user := s.state.Users[i]
		if _, selected := ids[user.ID]; selected {
			if action == "set_status" {
				user.Status = value
			}
			if action == "set_role" {
				user.Role = value
			}
		}
		if user.Role == "admin" && user.Status == "active" {
			return true
		}
	}
	return false
}

func (s *Server) syncAccountAccessLocked(user *User) {
	for i := range s.state.Accounts {
		if s.state.Accounts[i].UserID == user.ID {
			s.state.Accounts[i].Role = user.Role
			s.state.Accounts[i].Status = user.Status
		}
	}
}

func (s *Server) createAPIKey(c *gin.Context) {
	var body struct {
		Name string `json:"name"`
	}
	_ = c.ShouldBindJSON(&body)
	if body.Name == "" {
		body.Name = "API Key"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	user := s.findUser(c.Param("id"))
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	secret := "cat_" + randomHex(24)
	prefix := secret[:18]
	key := APIKey{
		ID:           newID("key"),
		UserID:       user.ID,
		Name:         body.Name,
		Prefix:       prefix,
		Hash:         hashSecret(secret),
		Status:       "active",
		CreatedAt:    now(),
		LastUsedAt:   "",
		RequestCount: 0,
	}
	s.state.APIKeys = append(s.state.APIKeys, key)
	s.saveStateLocked()

	c.JSON(http.StatusCreated, gin.H{"apiKey": publicAPIKey(key), "secret": secret})
}

func (s *Server) updateAPIKey(c *gin.Context) {
	var patch map[string]interface{}
	if err := c.ShouldBindJSON(&patch); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body", "invalid_request_error", nil)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.findAPIKeyByID(c.Param("id"))
	if key == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "API key not found"}})
		return
	}

	if value, ok := patch["name"].(string); ok {
		key.Name = value
	}
	if value, ok := patch["status"].(string); ok {
		if !allowedString(value, "active", "disabled") {
			validationError(c, "Invalid API key status")
			return
		}
		key.Status = value
	}
	s.saveStateLocked()

	c.JSON(http.StatusOK, gin.H{"apiKey": publicAPIKey(*key)})
}

func (s *Server) deleteAPIKey(c *gin.Context) {
	id := c.Param("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, key := range s.state.APIKeys {
		if key.ID != id {
			continue
		}
		s.state.APIKeys = append(s.state.APIKeys[:i], s.state.APIKeys[i+1:]...)
		s.saveStateLocked()
		c.JSON(http.StatusOK, gin.H{"deleted": true})
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "API key not found"}})
}

func (s *Server) listChannels(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channels := []PublicChannel{}
	for _, channel := range s.state.Channels {
		channels = append(channels, publicChannel(channel))
	}
	c.JSON(http.StatusOK, gin.H{"channels": channels})
}

func (s *Server) createChannel(c *gin.Context) {
	var body struct {
		Name           string   `json:"name"`
		Provider       string   `json:"provider"`
		BaseURL        string   `json:"baseUrl"`
		UpstreamAPIKey string   `json:"upstreamApiKey"`
		Priority       int      `json:"priority"`
		Weight         int      `json:"weight"`
		Models         []string `json:"models"`
	}
	_ = c.ShouldBindJSON(&body)
	body.Name = strings.TrimSpace(body.Name)
	body.Provider = strings.TrimSpace(body.Provider)
	body.BaseURL = strings.TrimSpace(body.BaseURL)
	if body.Name == "" {
		body.Name = "新渠道"
	}
	if body.Provider == "" {
		body.Provider = "compatible"
	}
	if body.BaseURL != "" && !strings.HasPrefix(body.BaseURL, "http://") && !strings.HasPrefix(body.BaseURL, "https://") {
		validationError(c, "Base URL must start with http:// or https://")
		return
	}
	if body.Priority <= 0 {
		body.Priority = 10
	}
	if body.Weight <= 0 {
		body.Weight = 10
	}
	protectedKey := ""
	if strings.TrimSpace(body.UpstreamAPIKey) != "" {
		var err error
		protectedKey, err = s.protectSecret(strings.TrimSpace(body.UpstreamAPIKey))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to protect upstream key"}})
			return
		}
	}

	channel := Channel{
		ID:             newID("chn"),
		Name:           body.Name,
		Provider:       body.Provider,
		BaseURL:        strings.TrimRight(body.BaseURL, "/"),
		UpstreamAPIKey: protectedKey,
		Status:         "disabled",
		Priority:       body.Priority,
		Weight:         body.Weight,
		Models:         append([]string{}, body.Models...),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Channels = append(s.state.Channels, channel)
	s.saveStateLocked()
	c.JSON(http.StatusCreated, gin.H{"channel": publicChannel(channel)})
}

func (s *Server) updateChannel(c *gin.Context) {
	var patch map[string]interface{}
	if err := c.ShouldBindJSON(&patch); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body", "invalid_request_error", nil)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	channel := s.findChannel(c.Param("id"))
	if channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}

	if value, ok := patch["status"].(string); ok {
		if !allowedString(value, "healthy", "standby", "disabled") {
			validationError(c, "Invalid channel status")
			return
		}
		channel.Status = value
	}
	if value, ok := patch["name"].(string); ok && strings.TrimSpace(value) != "" {
		channel.Name = strings.TrimSpace(value)
	}
	if value, ok := patch["provider"].(string); ok && strings.TrimSpace(value) != "" {
		channel.Provider = strings.TrimSpace(value)
	}
	if value, ok := patch["baseUrl"].(string); ok {
		value = strings.TrimSpace(value)
		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			validationError(c, "Base URL must start with http:// or https://")
			return
		}
		channel.BaseURL = strings.TrimRight(value, "/")
	}
	if value, ok := patch["upstreamApiKey"].(string); ok {
		protected, err := s.protectSecret(strings.TrimSpace(value))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to protect upstream key"}})
			return
		}
		channel.UpstreamAPIKey = protected
	}
	if value, ok := asInt(patch["priority"]); ok {
		if value < 1 {
			validationError(c, "Priority must be greater than or equal to 1")
			return
		}
		channel.Priority = value
	}
	if value, ok := asInt(patch["weight"]); ok {
		if value < 0 {
			validationError(c, "Weight must be greater than or equal to 0")
			return
		}
		channel.Weight = value
	}
	if value, ok := patch["models"].([]interface{}); ok {
		channel.Models = stringSlice(value)
	}
	if channel.Status != "disabled" && strings.TrimSpace(channel.BaseURL) == "" {
		validationError(c, "Base URL is required before enabling a channel")
		return
	}
	s.saveStateLocked()

	c.JSON(http.StatusOK, gin.H{"channel": publicChannel(*channel)})
}

func (s *Server) deleteChannel(c *gin.Context) {
	id := c.Param("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, channel := range s.state.Channels {
		if channel.ID != id {
			continue
		}
		s.state.Channels = append(s.state.Channels[:i], s.state.Channels[i+1:]...)
		s.saveStateLocked()
		c.JSON(http.StatusOK, gin.H{"deleted": true})
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
}

func (s *Server) syncChannelModels(c *gin.Context) {
	s.mu.Lock()
	channel := s.findChannel(c.Param("id"))
	if channel == nil {
		s.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	channelCopy := *channel
	upstreamKey, err := s.revealSecret(channelCopy.UpstreamAPIKey)
	if err != nil {
		s.mu.Unlock()
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	if strings.TrimSpace(upstreamKey) == "" {
		upstreamKey = s.upstreamAPIKey
	}
	s.mu.Unlock()

	modelIDs, err := s.fetchUpstreamModelIDs(channelCopy, upstreamKey)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	if len(modelIDs) == 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "上游未返回可用模型"}})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	channel = s.findChannel(channelCopy.ID)
	if channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	channel.Models = mergeStrings(channel.Models, modelIDs)
	added := []Model{}
	for _, id := range modelIDs {
		sourceName := strings.TrimSpace(channel.Name)
		if sourceName == "" {
			sourceName = providerLabel(channel.Provider)
		}
		if existing := s.findModel(id); existing != nil {
			if existing.Description == "从上游模型列表拉取" {
				existing.Vendor = sourceName
				existing.Description = "由 " + sourceName + " 拉取"
			}
			continue
		}
		model := Model{
			ID:          id,
			Name:        id,
			Vendor:      sourceName,
			Category:    "通用",
			Description: "由 " + sourceName + " 拉取",
			Price:       "自定义",
			Context:     "未配置上下文",
			Status:      "available",
		}
		s.state.Models = append(s.state.Models, model)
		added = append(added, model)
	}
	s.saveStateLocked()
	c.JSON(http.StatusOK, gin.H{"channel": publicChannel(*channel), "models": channel.Models, "addedModels": added})
}

func (s *Server) checkChannel(c *gin.Context) {
	s.mu.Lock()
	channel := s.findChannel(c.Param("id"))
	if channel == nil {
		s.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	channelCopy := *channel
	upstreamKey, err := s.revealSecret(channelCopy.UpstreamAPIKey)
	if err != nil {
		s.mu.Unlock()
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	if strings.TrimSpace(upstreamKey) == "" {
		upstreamKey = s.upstreamAPIKey
	}
	s.mu.Unlock()

	modelIDs, err := s.fetchUpstreamModelIDs(channelCopy, upstreamKey)

	s.mu.Lock()
	defer s.mu.Unlock()
	channel = s.findChannel(channelCopy.ID)
	if channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	channel.LastCheckedAt = now()
	if err != nil {
		channel.LastError = err.Error()
		if channel.Status != "disabled" {
			channel.Status = "standby"
		}
		s.saveStateLocked()
		c.JSON(http.StatusBadGateway, gin.H{
			"channel": publicChannel(*channel),
			"ok":      false,
			"error":   gin.H{"message": channel.LastError},
		})
		return
	}
	channel.LastError = ""
	if channel.Status != "disabled" {
		channel.Status = "healthy"
	}
	s.saveStateLocked()
	c.JSON(http.StatusOK, gin.H{"channel": publicChannel(*channel), "ok": true, "models": modelIDs})
}

func (s *Server) listModels(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	models := append([]Model{}, s.state.Models...)
	for i := range models {
		if models[i].Aliases == nil {
			models[i].Aliases = []string{}
		}
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

func (s *Server) createModel(c *gin.Context) {
	var body struct {
		ID               string   `json:"id"`
		Name             string   `json:"name"`
		Vendor           string   `json:"vendor"`
		Aliases          []string `json:"aliases"`
		Category         string   `json:"category"`
		Description      string   `json:"description"`
		Price            string   `json:"price"`
		InputPricePer1K  float64  `json:"inputPricePer1K"`
		OutputPricePer1K float64  `json:"outputPricePer1K"`
		Context          string   `json:"context"`
		Status           string   `json:"status"`
		Recommended      bool     `json:"recommended"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		validationError(c, "无效的模型信息")
		return
	}
	body.ID = strings.TrimSpace(body.ID)
	body.Name = strings.TrimSpace(body.Name)
	body.Vendor = strings.TrimSpace(body.Vendor)
	body.Category = strings.TrimSpace(body.Category)
	body.Description = strings.TrimSpace(body.Description)
	body.Price = strings.TrimSpace(body.Price)
	body.Context = strings.TrimSpace(body.Context)
	body.Status = strings.TrimSpace(body.Status)
	if body.ID == "" {
		validationError(c, "模型 ID 不能为空")
		return
	}
	if strings.ContainsAny(body.ID, " \t\r\n") || len(body.ID) > 128 {
		validationError(c, "模型 ID 不能包含空白字符，且不能超过 128 个字符")
		return
	}
	if body.Name == "" {
		body.Name = body.ID
	}
	if body.Vendor == "" {
		body.Vendor = "Custom"
	}
	if body.Category == "" {
		body.Category = "通用"
	}
	if body.Price == "" {
		body.Price = "自定义"
	}
	if body.Context == "" {
		body.Context = "未配置上下文"
	}
	if body.Status == "" {
		body.Status = "available"
	}
	if !allowedString(body.Status, "available", "limited", "disabled") {
		validationError(c, "Invalid model status")
		return
	}
	if body.InputPricePer1K < 0 || body.OutputPricePer1K < 0 {
		validationError(c, "Model price must be greater than or equal to 0")
		return
	}

	model := Model{
		ID:                body.ID,
		Name:              body.Name,
		Vendor:            body.Vendor,
		Aliases:           cleanAliases(body.Aliases),
		Category:          body.Category,
		Description:       body.Description,
		Price:             body.Price,
		InputPricePer1K:   round4(body.InputPricePer1K),
		OutputPricePer1K:  round4(body.OutputPricePer1K),
		PricingConfigured: body.InputPricePer1K > 0 || body.OutputPricePer1K > 0,
		Context:           body.Context,
		Status:            body.Status,
		Recommended:       body.Recommended,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.findModel(model.ID) != nil {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "模型 ID 已存在"}})
		return
	}
	s.state.Models = append(s.state.Models, model)
	s.saveStateLocked()
	c.JSON(http.StatusCreated, gin.H{"model": model})
}

func (s *Server) updateModel(c *gin.Context) {
	var patch map[string]interface{}
	if err := c.ShouldBindJSON(&patch); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body", "invalid_request_error", nil)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	model := s.findModel(c.Param("id"))
	if model == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Model not found"}})
		return
	}

	if value, ok := patch["status"].(string); ok {
		if !allowedString(value, "available", "limited", "disabled") {
			validationError(c, "Invalid model status")
			return
		}
		model.Status = value
	}
	if value, ok := patch["recommended"].(bool); ok {
		model.Recommended = value
	}
	if value, ok := patch["description"].(string); ok {
		model.Description = value
	}
	pricingChanged := false
	if value, ok := asFloat(patch["inputPricePer1K"]); ok {
		if value < 0 {
			validationError(c, "Input price must be greater than or equal to 0")
			return
		}
		model.InputPricePer1K = round4(value)
		pricingChanged = true
	}
	if value, ok := asFloat(patch["outputPricePer1K"]); ok {
		if value < 0 {
			validationError(c, "Output price must be greater than or equal to 0")
			return
		}
		model.OutputPricePer1K = round4(value)
		pricingChanged = true
	}
	if pricingChanged {
		model.PricingConfigured = model.InputPricePer1K > 0 || model.OutputPricePer1K > 0
	}
	s.saveStateLocked()

	c.JSON(http.StatusOK, gin.H{"model": model})
}

func (s *Server) openAIModels(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data := []gin.H{}
	for _, model := range s.state.Models {
		if model.Status == "available" {
			data = append(data, toOpenAIModel(model))
		}
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

func (s *Server) openAIModel(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	model := s.resolveModelLocked(c.Param("id"))
	if model == nil || model.Status != "available" {
		s.openAIError(c, http.StatusNotFound, "model_not_found", "Model not found: "+c.Param("id"), "invalid_request_error", stringPtr("model"))
		return
	}
	c.JSON(http.StatusOK, toOpenAIModel(*model))
}

func (s *Server) listLogs(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	logs := append([]RequestLog{}, s.state.Logs...)
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].CreatedAt > logs[j].CreatedAt
	})
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

func (s *Server) quotaLedger(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries := append([]QuotaEntry{}, s.state.QuotaLedger...)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CreatedAt > entries[j].CreatedAt
	})
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

func (s *Server) completions(c *gin.Context) {
	startedAt := time.Now()
	idempotencyKey := c.GetHeader("Idempotency-Key")

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body: "+err.Error(), "invalid_request_error", nil)
		return
	}

	model, _ := payload["model"].(string)
	stream, _ := payload["stream"].(bool)
	prompt := completionPrompt(payload["prompt"])
	if strings.TrimSpace(prompt) == "" {
		s.openAIError(c, http.StatusBadRequest, "invalid_prompt", "prompt is required", "invalid_request_error", stringPtr("prompt"))
		return
	}
	delete(payload, "prompt")
	body := ChatRequest{
		Model:    model,
		Stream:   stream,
		Messages: []ChatMessage{{Role: "user", Content: prompt}},
		Payload:  payload,
	}
	s.handleChatCompletion(c, body, startedAt, idempotencyKey)
}

func (s *Server) responses(c *gin.Context) {
	startedAt := time.Now()
	idempotencyKey := c.GetHeader("Idempotency-Key")

	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body: "+err.Error(), "invalid_request_error", nil)
		return
	}

	model, _ := payload["model"].(string)
	stream, _ := payload["stream"].(bool)
	if stream {
		s.openAIError(c, http.StatusNotImplemented, "unsupported_stream", "Responses streaming is not enabled yet. Use /v1/chat/completions for streaming calls.", "invalid_request_error", stringPtr("stream"))
		return
	}
	messages := responseInputMessages(payload)
	if len(messages) == 0 {
		s.openAIError(c, http.StatusBadRequest, "invalid_input", "input is required", "invalid_request_error", stringPtr("input"))
		return
	}
	delete(payload, "input")
	delete(payload, "instructions")
	body := ChatRequest{
		Model:    model,
		Stream:   stream,
		Messages: messages,
		Payload:  payload,
	}
	s.handleChatCompletionWithTransform(c, body, startedAt, idempotencyKey, responseFromChatCompletion)
}

func (s *Server) embeddings(c *gin.Context) {
	s.openAIError(c, http.StatusNotImplemented, "unsupported_endpoint", "Embeddings are not enabled yet. Configure a text-embedding model before exposing this endpoint.", "invalid_request_error", nil)
}

func (s *Server) chatCompletions(c *gin.Context) {
	startedAt := time.Now()
	idempotencyKey := c.GetHeader("Idempotency-Key")

	s.mu.Lock()
	if idempotencyKey != "" {
		if cached, ok := s.idempotencyCache[idempotencyKey]; ok {
			s.mu.Unlock()
			c.Header("x-catieapi-cache", "idempotency")
			c.JSON(cached.Status, cached.Body)
			return
		}
	}
	s.mu.Unlock()

	var body ChatRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body: "+err.Error(), "invalid_request_error", nil)
		return
	}

	s.handleChatCompletion(c, body, startedAt, idempotencyKey)
}

func (s *Server) handleChatCompletion(c *gin.Context, body ChatRequest, startedAt time.Time, idempotencyKey string) {
	s.handleChatCompletionWithTransform(c, body, startedAt, idempotencyKey, nil)
}

func (s *Server) handleChatCompletionWithTransform(c *gin.Context, body ChatRequest, startedAt time.Time, idempotencyKey string, transform func(gin.H, Model) gin.H) {
	s.mu.Lock()

	auth := s.findUserByAPIKeyLocked(apiTokenFromRequest(c))
	if auth == nil {
		s.openAIErrorForCallLocked(c, http.StatusUnauthorized, "invalid_api_key", "Invalid CatieAPI key", "invalid_request_error", nil, "", "", body.Model, "")
		s.mu.Unlock()
		return
	}
	if auth.User.Status == "limited" {
		s.openAIErrorForCallLocked(c, http.StatusPaymentRequired, "insufficient_quota", "Insufficient quota", "billing_error", nil, auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		return
	}
	if !s.checkRateLimitLocked(auth.Key) {
		s.openAIErrorForCallLocked(c, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded", "rate_limit_error", nil, auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		return
	}
	if body.Messages == nil {
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "invalid_messages", "messages must be an array", "invalid_request_error", stringPtr("messages"), auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		return
	}

	model := s.resolveModelLocked(body.Model)
	if model == nil || model.Status != "available" {
		name := body.Model
		if name == "" {
			name = "<model>"
		}
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "model_not_available", "No available model: "+name, "invalid_request_error", stringPtr("model"), auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		return
	}
	if model.PricingConfigured && auth.User.Balance <= 0 {
		s.openAIErrorForCallLocked(c, http.StatusPaymentRequired, "insufficient_quota", "Insufficient quota", "billing_error", nil, auth.User.ID, auth.Key.Prefix, model.ID, "")
		s.mu.Unlock()
		return
	}

	channel := s.primaryChannelLocked(model.ID)
	if channel == nil {
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "model_not_available", "No available channel for model: "+model.ID, "invalid_request_error", stringPtr("model"), auth.User.ID, auth.Key.Prefix, model.ID, "")
		s.mu.Unlock()
		return
	}

	authUserID := auth.User.ID
	authKeyID := auth.Key.ID
	authKeyPrefix := auth.Key.Prefix
	modelCopy := *model
	channelCopy := *channel
	requestID := newID("req")
	s.mu.Unlock()

	call := GatewayCall{RequestID: requestID, Model: modelCopy, Channel: channelCopy, Body: body}

	if body.Stream {
		if providerErr := s.writeProviderStream(c, call); providerErr != nil {
			s.recordFailedCall(authUserID, authKeyPrefix, modelCopy.ID, channelCopy.Name, requestID, providerErr.Code, startedAt)
			writeOpenAIError(c, providerErr.Status, providerErr.Code, providerErr.Message, providerErr.Type, stringPtr("model"))
			return
		}
		streamCost := calculateCallCost(modelCopy, nil, body.Messages, true)
		s.recordSuccessfulCall(authUserID, authKeyID, authKeyPrefix, modelCopy.ID, channelCopy.Name, requestID, streamCost, startedAt)
		return
	}

	responseBody, providerErr := s.callProvider(call)
	if providerErr != nil {
		s.recordFailedCall(authUserID, authKeyPrefix, modelCopy.ID, channelCopy.Name, requestID, providerErr.Code, startedAt)
		writeOpenAIError(c, providerErr.Status, providerErr.Code, providerErr.Message, providerErr.Type, stringPtr("model"))
		return
	}

	cost := calculateCallCost(modelCopy, responseBody, body.Messages, false)
	outputBody := interface{}(responseBody)
	if transform != nil {
		outputBody = transform(responseBody, modelCopy)
	}
	s.mu.Lock()
	s.recordSuccessfulCallLocked(authUserID, authKeyID, authKeyPrefix, modelCopy.ID, channelCopy.Name, requestID, cost, startedAt)
	if idempotencyKey != "" {
		s.idempotencyCache[idempotencyKey] = CachedResponse{Status: http.StatusOK, Body: outputBody, CreatedAt: now()}
	}
	s.mu.Unlock()
	c.JSON(http.StatusOK, outputBody)
}

func (s *Server) recordSuccessfulCall(userID string, keyID string, keyPrefix string, modelID string, channelName string, requestID string, cost float64, startedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordSuccessfulCallLocked(userID, keyID, keyPrefix, modelID, channelName, requestID, cost, startedAt)
}

func (s *Server) recordSuccessfulCallLocked(userID string, keyID string, keyPrefix string, modelID string, channelName string, requestID string, cost float64, startedAt time.Time) {
	user := s.findUser(userID)
	key := s.findAPIKeyByID(keyID)
	if user == nil || key == nil {
		return
	}
	user.Balance = round4(user.Balance - cost)
	if user.Balance < 0 {
		user.Balance = 0
	}
	user.RequestsToday++
	user.TotalRequests++
	key.LastUsedAt = now()
	key.RequestCount++

	s.state.QuotaLedger = append(s.state.QuotaLedger, QuotaEntry{
		ID:        fmt.Sprintf("quota_%d_%d", time.Now().UnixMilli(), len(s.state.QuotaLedger)+1),
		UserID:    userID,
		RequestID: requestID,
		Model:     modelID,
		Amount:    -cost,
		Reason:    "chat.completion",
		CreatedAt: now(),
	})

	s.state.Logs = append(s.state.Logs, RequestLog{
		ID:           requestID,
		UserID:       &userID,
		APIKeyPrefix: &keyPrefix,
		Model:        &modelID,
		Channel:      &channelName,
		Status:       "success",
		Cost:         cost,
		LatencyMS:    time.Since(startedAt).Milliseconds(),
		CreatedAt:    now(),
	})
	s.saveStateLocked()
}

func (s *Server) recordFailedCall(userID string, keyPrefix string, modelID string, channelName string, requestID string, errorCode string, startedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Logs = append(s.state.Logs, RequestLog{
		ID:           requestID,
		UserID:       &userID,
		APIKeyPrefix: &keyPrefix,
		Model:        &modelID,
		Channel:      &channelName,
		Status:       "failed",
		Cost:         0,
		LatencyMS:    time.Since(startedAt).Milliseconds(),
		ErrorCode:    errorCode,
		CreatedAt:    now(),
	})
	s.saveStateLocked()
}

type ProviderError struct {
	Status  int
	Code    string
	Message string
	Type    string
}

func (s *Server) callProvider(call GatewayCall) (gin.H, *ProviderError) {
	if s.shouldUseCompatibleProvider(call.Channel) {
		return s.callOpenAICompatible(call)
	}
	return gin.H{
		"id":      newID("chatcmpl"),
		"object":  "chat.completion",
		"created": unixNow(),
		"model":   call.Model.ID,
		"choices": []gin.H{
			{
				"index": 0,
				"message": gin.H{
					"role":    "assistant",
					"content": fmt.Sprintf("CatieAPI mock response via %s. Provider adapters can forward this request to an OpenAI-compatible upstream.", call.Channel.Name),
				},
				"finish_reason": "stop",
			},
		},
		"usage": gin.H{
			"prompt_tokens":     estimateTokens(call.Body.Messages),
			"completion_tokens": 18,
			"total_tokens":      estimateTokens(call.Body.Messages) + 18,
		},
	}, nil
}

func (s *Server) callOpenAICompatible(call GatewayCall) (gin.H, *ProviderError) {
	request, providerErr := s.newOpenAICompatibleRequest(call, false)
	if providerErr != nil {
		return nil, providerErr
	}

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_unreachable", Message: err.Error(), Type: "api_error"}
	}
	defer response.Body.Close()

	content, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Message: err.Error(), Type: "api_error"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, providerErrorFromUpstream(response.StatusCode, content)
	}

	var body gin.H
	if err := json.Unmarshal(content, &body); err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_invalid_json", Message: "Upstream returned invalid JSON", Type: "api_error"}
	}
	if _, ok := body["model"]; !ok {
		body["model"] = call.Model.ID
	}
	return body, nil
}

func (s *Server) fetchUpstreamModelIDs(channel Channel, upstreamKey string) ([]string, error) {
	if strings.TrimSpace(channel.BaseURL) == "" {
		return nil, fmt.Errorf("请先填写渠道 Base URL")
	}
	request, err := http.NewRequest(http.MethodGet, joinURL(channel.BaseURL, "models"), nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(upstreamKey) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(upstreamKey))
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(content))
		if message == "" {
			message = response.Status
		}
		return nil, fmt.Errorf("上游模型列表请求失败: %s", message)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, fmt.Errorf("上游模型列表不是有效 JSON")
	}
	ids := []string{}
	for _, item := range payload.Data {
		if strings.TrimSpace(item.ID) != "" {
			ids = append(ids, strings.TrimSpace(item.ID))
		}
	}
	for _, id := range payload.Models {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, strings.TrimSpace(id))
		}
	}
	return mergeStrings(nil, ids), nil
}

func (s *Server) streamOpenAICompatible(c *gin.Context, call GatewayCall) *ProviderError {
	request, providerErr := s.newOpenAICompatibleRequest(call, true)
	if providerErr != nil {
		return providerErr
	}

	response, err := s.httpClient.Do(request)
	if err != nil {
		return &ProviderError{Status: http.StatusBadGateway, Code: "upstream_unreachable", Message: err.Error(), Type: "api_error"}
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		content, _ := io.ReadAll(response.Body)
		return providerErrorFromUpstream(response.StatusCode, content)
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	_, _ = io.Copy(c.Writer, response.Body)
	c.Writer.Flush()
	return nil
}

func (s *Server) shouldUseCompatibleProvider(channel Channel) bool {
	if strings.EqualFold(s.providerMode, "compatible") {
		return true
	}
	return strings.TrimSpace(channel.BaseURL) != "" && (strings.TrimSpace(channel.UpstreamAPIKey) != "" || strings.TrimSpace(s.upstreamAPIKey) != "")
}

func (s *Server) newOpenAICompatibleRequest(call GatewayCall, stream bool) (*http.Request, *ProviderError) {
	upstreamKey, err := s.revealSecret(call.Channel.UpstreamAPIKey)
	if err != nil {
		return nil, &ProviderError{
			Status:  http.StatusBadGateway,
			Code:    "upstream_key_unavailable",
			Message: err.Error(),
			Type:    "api_error",
		}
	}
	upstreamKey = strings.TrimSpace(upstreamKey)
	if upstreamKey == "" {
		upstreamKey = s.upstreamAPIKey
	}
	if upstreamKey == "" {
		return nil, &ProviderError{
			Status:  http.StatusBadGateway,
			Code:    "upstream_not_configured",
			Message: "Channel upstreamApiKey or UPSTREAM_API_KEY is required when PROVIDER_MODE=compatible",
			Type:    "api_error",
		}
	}

	payload := gin.H{}
	for key, value := range call.Body.Payload {
		payload[key] = value
	}
	payload["model"] = call.Model.ID
	payload["stream"] = stream
	payload["messages"] = call.Body.Messages
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Failed to encode upstream request", Type: "invalid_request_error"}
	}

	endpoint := joinURL(call.Channel.BaseURL, "chat/completions")
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	request.Header.Set("Authorization", "Bearer "+upstreamKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", call.RequestID)
	return request, nil
}

func (s *Server) writeProviderStream(c *gin.Context, call GatewayCall) *ProviderError {
	if s.shouldUseCompatibleProvider(call.Channel) {
		return s.streamOpenAICompatible(c, call)
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	chunkID := newID("chatcmpl")
	chunks := []gin.H{
		{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": unixNow(),
			"model":   call.Model.ID,
			"choices": []gin.H{{
				"index":         0,
				"delta":         gin.H{"role": "assistant", "content": fmt.Sprintf("CatieAPI mock stream via %s.", call.Channel.Name)},
				"finish_reason": nil,
			}},
		},
		{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": unixNow(),
			"model":   call.Model.ID,
			"choices": []gin.H{{
				"index":         0,
				"delta":         gin.H{},
				"finish_reason": "stop",
			}},
		},
	}

	for _, chunk := range chunks {
		encoded, _ := json.Marshal(chunk)
		_, _ = c.Writer.WriteString("data: " + string(encoded) + "\n\n")
		c.Writer.Flush()
	}
	_, _ = c.Writer.WriteString("data: [DONE]\n\n")
	c.Writer.Flush()
	return nil
}

func (s *Server) openAIError(c *gin.Context, status int, code string, message string, errorType string, param *string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openAIErrorLocked(c, status, code, message, errorType, param)
}

func (s *Server) openAIErrorLocked(c *gin.Context, status int, code string, message string, errorType string, param *string) {
	requestID := requestIDFromContext(c)
	s.state.Logs = append(s.state.Logs, RequestLog{
		ID:        requestID,
		Status:    "failed",
		Cost:      0,
		LatencyMS: 0,
		ErrorCode: code,
		CreatedAt: now(),
	})
	s.saveStateLocked()

	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errorType,
			"param":   param,
			"code":    code,
		},
	})
}

func (s *Server) openAIErrorForCallLocked(c *gin.Context, status int, code string, message string, errorType string, param *string, userID string, keyPrefix string, modelID string, channelName string) {
	requestID := requestIDFromContext(c)
	log := RequestLog{
		ID:        requestID,
		Status:    "failed",
		Cost:      0,
		LatencyMS: 0,
		ErrorCode: code,
		CreatedAt: now(),
	}
	if userID != "" {
		log.UserID = &userID
	}
	if keyPrefix != "" {
		log.APIKeyPrefix = &keyPrefix
	}
	if modelID != "" {
		log.Model = &modelID
	}
	if channelName != "" {
		log.Channel = &channelName
	}
	s.state.Logs = append(s.state.Logs, log)
	s.saveStateLocked()

	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errorType,
			"param":   param,
			"code":    code,
		},
	})
}

func (s *Server) discordLoginEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.discordLoginEnabledLocked()
}

func (s *Server) discordLoginEnabledLocked() bool {
	return s.discordClientID != "" && s.discordClientSecret != ""
}

func (config DiscordRuntimeConfig) enabled() bool {
	return config.ClientID != "" && config.ClientSecret != "" && config.RedirectURI != ""
}

func (s *Server) discordRuntimeConfig(c *gin.Context) DiscordRuntimeConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	redirectURI := s.discordRedirectURI
	authSuccessURL := s.authSuccessURL
	if redirectURI == "" {
		redirectURI = defaultDiscordRedirectURI(c)
	}
	if authSuccessURL == "" {
		authSuccessURL = requestOrigin(c) + "/"
	}
	return DiscordRuntimeConfig{
		ClientID:       s.discordClientID,
		ClientSecret:   s.discordClientSecret,
		RedirectURI:    redirectURI,
		AllowedGuildID: s.discordAllowedGuildID,
		AllowedRoleID:  s.discordAllowedRoleID,
		OAuthBase:      s.discordOAuthBase,
		AuthSuccessURL: authSuccessURL,
		SessionTTL:     s.sessionTTL,
	}
}

func (s *Server) consumeAuthState(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiresAt, ok := s.authStates[state]
	if !ok {
		return false
	}
	delete(s.authStates, state)
	return time.Now().Before(expiresAt)
}

func (s *Server) exchangeDiscordCode(code string, config DiscordRuntimeConfig) (*DiscordTokenResponse, error) {
	values := url.Values{}
	values.Set("client_id", config.ClientID)
	values.Set("client_secret", config.ClientSecret)
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("redirect_uri", config.RedirectURI)

	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(config.OAuthBase, "/")+"/token", strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Discord token exchange failed: %s", strings.TrimSpace(string(content)))
	}

	var token DiscordTokenResponse
	if err := json.Unmarshal(content, &token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("Discord token exchange returned empty access token")
	}
	return &token, nil
}

func (s *Server) fetchDiscordUser(accessToken string) (*DiscordUser, error) {
	var user DiscordUser
	if err := s.discordGet(accessToken, "/users/@me", &user); err != nil {
		return nil, err
	}
	if user.ID == "" {
		return nil, fmt.Errorf("Discord user response missing id")
	}
	return &user, nil
}

func (s *Server) fetchDiscordGuildMember(accessToken string, guildID string) (*DiscordGuildMember, error) {
	var member DiscordGuildMember
	if err := s.discordGet(accessToken, "/users/@me/guilds/"+url.PathEscape(guildID)+"/member", &member); err != nil {
		return nil, err
	}
	return &member, nil
}

func (s *Server) discordGet(accessToken string, path string, target interface{}) error {
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(s.discordAPIBase, "/")+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")

	response, err := s.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Discord API request failed: %s", strings.TrimSpace(string(content)))
	}
	return json.Unmarshal(content, target)
}

func (s *Server) createSession(user *DiscordUser, guildID string, roleID string, ttl time.Duration) Session {
	username := user.GlobalName
	if username == "" {
		username = user.Username
	}
	session := Session{
		ID:        "sess_" + randomHex(24),
		Provider:  "discord",
		UserID:    user.ID,
		Username:  username,
		Avatar:    user.Avatar,
		GuildID:   guildID,
		RoleID:    roleID,
		Role:      "admin",
		CreatedAt: now(),
		ExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339Nano),
	}
	s.mu.Lock()
	s.sessions[session.ID] = session
	s.mu.Unlock()
	return session
}

func (s *Server) createAccountSession(account Account, displayName string, provider string) Session {
	session := Session{
		ID:        "sess_" + randomHex(24),
		Provider:  provider,
		UserID:    account.UserID,
		Username:  displayName,
		Role:      account.Role,
		CreatedAt: now(),
		ExpiresAt: time.Now().Add(s.sessionTTL).UTC().Format(time.RFC3339Nano),
	}
	s.mu.Lock()
	s.sessions[session.ID] = session
	s.mu.Unlock()
	return session
}

func (s *Server) setSessionCookie(c *gin.Context, session Session) {
	expiresAt, _ := time.Parse(time.RFC3339Nano, session.ExpiresAt)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "catie_session",
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") || c.Request.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
}

func (s *Server) sessionFromRequest(c *gin.Context) (Session, bool) {
	cookie, err := c.Cookie("catie_session")
	if err != nil || cookie == "" {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[cookie]
	if !ok {
		return Session{}, false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, session.ExpiresAt)
	if err != nil || time.Now().UTC().After(expiresAt) {
		delete(s.sessions, cookie)
		return Session{}, false
	}
	return session, true
}

func (s *Server) serveStatic(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return false
	}
	requestPath := c.Request.URL.Path
	if strings.HasPrefix(requestPath, "/api/") || strings.HasPrefix(requestPath, "/v1/") {
		return false
	}
	if s.staticDir == "" {
		return false
	}

	cleanPath := strings.TrimPrefix(filepath.Clean("/"+requestPath), string(filepath.Separator))
	target := filepath.Join(s.staticDir, cleanPath)
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		c.File(target)
		return true
	}

	indexPath := filepath.Join(s.staticDir, "index.html")
	if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
		c.File(indexPath)
		return true
	}
	return false
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func validateAccountInput(username string, password string, email string) string {
	if message := validateUsername(username); message != "" {
		return message
	}
	if len(password) < 8 || len(password) > 128 {
		return "密码长度必须在 8 到 128 个字符之间"
	}
	if email != "" {
		address, err := mail.ParseAddress(email)
		if err != nil || !strings.EqualFold(address.Address, email) {
			return "邮箱格式不正确"
		}
	}
	return ""
}

func validateUsername(username string) string {
	if len(username) < 3 || len(username) > 32 {
		return "账号长度必须在 3 到 32 个字符之间"
	}
	for _, character := range username {
		valid := character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-'
		if !valid {
			return "账号只能使用字母、数字、下划线和短横线"
		}
	}
	return ""
}

func cleanAliases(values []string) []string {
	seen := map[string]bool{}
	aliases := []string{}
	for _, value := range values {
		alias := strings.TrimSpace(value)
		if alias == "" {
			continue
		}
		lower := strings.ToLower(alias)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		aliases = append(aliases, alias)
	}
	return aliases
}

func mergeStrings(current []string, next []string) []string {
	seen := map[string]bool{}
	merged := []string{}
	for _, values := range [][]string{current, next} {
		for _, value := range values {
			item := strings.TrimSpace(value)
			if item == "" {
				continue
			}
			key := strings.ToLower(item)
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, item)
		}
	}
	return merged
}

func providerLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "google":
		return "Google"
	case "deepseek":
		return "DeepSeek"
	case "openrouter":
		return "OpenRouter"
	case "groq":
		return "Groq"
	case "siliconflow":
		return "SiliconFlow"
	case "moonshot":
		return "Moonshot"
	default:
		return "Custom"
	}
}

func normalizeRegistrationMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "email":
		return "email"
	case "discord":
		return "discord"
	default:
		return "username"
	}
}

func usernameFromEmail(email string) string {
	local := email
	if index := strings.Index(email, "@"); index > 0 {
		local = email[:index]
	}
	return sanitizeUsername(local, "user")
}

func usernameFromDiscord(user *DiscordUser) string {
	base := user.Username
	if base == "" {
		base = user.GlobalName
	}
	if base == "" {
		base = "discord"
	}
	return sanitizeUsername(base, "discord")
}

func sanitizeUsername(value string, fallback string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		valid := character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-'
		if valid {
			builder.WriteRune(character)
		} else if character == '.' || character == ' ' {
			builder.WriteRune('-')
		}
	}
	result := strings.Trim(builder.String(), "-_")
	if len(result) < 3 {
		result = fallback
	}
	if len(result) > 24 {
		result = result[:24]
	}
	return result
}

func invalidLogin(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{
			"message": "账号或密码不正确",
			"type":    "invalid_request_error",
			"code":    "invalid_credentials",
		},
	})
}

func publicAccount(account Account) gin.H {
	return gin.H{
		"id":            account.ID,
		"userId":        account.UserID,
		"username":      account.Username,
		"email":         account.Email,
		"discordUserId": account.DiscordUserID,
		"role":          account.Role,
		"status":        account.Status,
		"createdAt":     account.CreatedAt,
		"lastLoginAt":   account.LastLoginAt,
	}
}

func nullableSession(ok bool, session Session) interface{} {
	if !ok {
		return nil
	}
	return session
}

func digitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func requestOrigin(c *gin.Context) string {
	proto := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
	if proto == "" {
		proto = strings.TrimSpace(c.GetHeader("X-Forwarded-Scheme"))
	}
	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(c.GetHeader("Host"))
	}
	if host == "" {
		host = c.Request.Host
	}
	if proto == "" {
		if c.Request.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	if strings.Contains(proto, ",") {
		proto = strings.TrimSpace(strings.Split(proto, ",")[0])
	}
	if strings.Contains(host, ",") {
		host = strings.TrimSpace(strings.Split(host, ",")[0])
	}
	return proto + "://" + host
}

func defaultDiscordRedirectURI(c *gin.Context) string {
	return requestOrigin(c) + "/api/auth/discord/callback"
}

func writeOpenAIError(c *gin.Context, status int, code string, message string, errorType string, param *string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errorType,
			"param":   param,
			"code":    code,
		},
	})
}

func (s *Server) findUser(id string) *User {
	for i := range s.state.Users {
		if s.state.Users[i].ID == id {
			return &s.state.Users[i]
		}
	}
	return nil
}

func (s *Server) findAPIKeyByID(id string) *APIKey {
	for i := range s.state.APIKeys {
		if s.state.APIKeys[i].ID == id {
			return &s.state.APIKeys[i]
		}
	}
	return nil
}

func (s *Server) findChannel(id string) *Channel {
	for i := range s.state.Channels {
		if s.state.Channels[i].ID == id {
			return &s.state.Channels[i]
		}
	}
	return nil
}

func (s *Server) findModel(id string) *Model {
	for i := range s.state.Models {
		if s.state.Models[i].ID == id {
			return &s.state.Models[i]
		}
	}
	return nil
}

func (s *Server) findAccountByIdentifierLocked(identifier string) *Account {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	for i := range s.state.Accounts {
		account := &s.state.Accounts[i]
		if strings.ToLower(account.Username) == identifier || (account.Email != "" && strings.ToLower(account.Email) == identifier) {
			return account
		}
	}
	return nil
}

func (s *Server) findAccountByIDLocked(id string) *Account {
	for i := range s.state.Accounts {
		if s.state.Accounts[i].ID == id {
			return &s.state.Accounts[i]
		}
	}
	return nil
}

func (s *Server) findAccountByUserIDLocked(userID string) *Account {
	for i := range s.state.Accounts {
		if s.state.Accounts[i].UserID == userID {
			return &s.state.Accounts[i]
		}
	}
	return nil
}

func (s *Server) findAccountByDiscordIDLocked(discordUserID string) *Account {
	for i := range s.state.Accounts {
		if s.state.Accounts[i].DiscordUserID == discordUserID {
			return &s.state.Accounts[i]
		}
	}
	return nil
}

func uniqueUsernameLocked(s *Server, base string, fallback string) string {
	username := sanitizeUsername(base, fallback)
	for index := 0; index < 1000; index++ {
		candidate := username
		if index > 0 {
			suffix := "-" + strconv.Itoa(index+1)
			trimmed := username
			if len(trimmed)+len(suffix) > 32 {
				trimmed = trimmed[:32-len(suffix)]
			}
			candidate = trimmed + suffix
		}
		if s.findAccountByIdentifierLocked(candidate) == nil {
			return candidate
		}
	}
	return sanitizeUsername(fallback, "user") + "-" + randomHex(4)
}

func (s *Server) registrationEnabledLocked() bool {
	if !s.state.Settings.Auth.Managed {
		return true
	}
	return s.state.Settings.Auth.RegistrationEnabled
}

func (s *Server) registrationModeLocked() string {
	return normalizeRegistrationMode(s.state.Settings.Auth.RegistrationMode)
}

func (s *Server) defaultRegistrationBalanceLocked() float64 {
	return round4(s.state.Settings.Auth.DefaultBalance)
}

func (s *Server) appendInitialQuotaLocked(user *User) {
	if user == nil || user.Balance <= 0 {
		return
	}
	s.state.QuotaLedger = append(s.state.QuotaLedger, QuotaEntry{
		ID:        newID("quota"),
		UserID:    user.ID,
		Amount:    user.Balance,
		Reason:    "新用户初始额度",
		CreatedAt: now(),
	})
}

func (s *Server) findUserByAPIKeyLocked(secret string) *AuthContext {
	token := bearerToken(secret)
	if token == "" {
		return nil
	}
	for i := range s.state.APIKeys {
		key := &s.state.APIKeys[i]
		if key.Status != "active" || !keyMatchesSecret(key, token) {
			continue
		}
		user := s.findUser(key.UserID)
		if user == nil || user.Status == "disabled" {
			return nil
		}
		return &AuthContext{User: user, Key: key}
	}
	return nil
}

func apiTokenFromRequest(c *gin.Context) string {
	for _, value := range []string{
		c.GetHeader("Authorization"),
		c.GetHeader("X-API-Key"),
		c.GetHeader("X-Api-Key"),
		c.GetHeader("Api-Key"),
		c.Query("api_key"),
	} {
		if token := bearerToken(value); token != "" {
			return token
		}
	}
	return ""
}

func (s *Server) resolveModelLocked(input string) *Model {
	value := strings.TrimSpace(input)
	if value == "" {
		return nil
	}
	lower := strings.ToLower(value)
	for i := range s.state.Models {
		model := &s.state.Models[i]
		if strings.ToLower(model.ID) == lower || strings.ToLower(model.Name) == lower {
			return model
		}
		for _, alias := range model.Aliases {
			if strings.ToLower(alias) == lower {
				return model
			}
		}
	}
	return nil
}

func (s *Server) primaryChannelLocked(modelID string) *Channel {
	var candidates []*Channel
	for i := range s.state.Channels {
		channel := &s.state.Channels[i]
		if channel.Status == "disabled" {
			continue
		}
		for _, id := range channel.Models {
			if id == modelID {
				candidates = append(candidates, channel)
				break
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Priority < candidates[j].Priority
	})
	if len(candidates) == 0 {
		return nil
	}
	return candidates[0]
}

func (s *Server) checkRateLimitLocked(key *APIKey) bool {
	bucket := fmt.Sprintf("%s:%d", key.ID, time.Now().Unix()/60)
	current := s.rateLimitBuckets[bucket]
	if current >= s.requestLimitPerMinute {
		return false
	}
	s.rateLimitBuckets[bucket] = current + 1
	return true
}

func (s *Server) loadState() {
	if s.persistence == "postgres" {
		s.loadPostgresState()
		return
	}
	if s.persistence != "file" {
		return
	}
	content, err := os.ReadFile(s.dataFile)
	if err != nil {
		return
	}
	var stored AppState
	if err := json.Unmarshal(content, &stored); err != nil {
		return
	}
	if stored.Users != nil {
		s.state.Users = stored.Users
	}
	if stored.APIKeys != nil {
		s.state.APIKeys = stored.APIKeys
	}
	if stored.Channels != nil {
		s.state.Channels = stored.Channels
	}
	if stored.Models != nil {
		s.state.Models = stored.Models
	}
	if stored.QuotaLedger != nil {
		s.state.QuotaLedger = stored.QuotaLedger
	}
	if stored.Logs != nil {
		s.state.Logs = stored.Logs
	}
	if stored.Accounts != nil {
		s.state.Accounts = stored.Accounts
	}
	if stored.Settings.Discord.Managed || stored.Settings.Auth.Managed {
		s.state.Settings = stored.Settings
	}
	changed := false
	if s.migrateDemoSeedData() {
		changed = true
	}
	if s.migrateModelPricing() {
		changed = true
	}
	if s.normalizeStateCollections() {
		changed = true
	}
	if changed {
		s.saveStateLocked()
	}
}

func (s *Server) applyPersistedDiscordSettings() {
	settings := s.state.Settings.Discord
	if !settings.Managed {
		return
	}
	secret, err := s.revealSecret(settings.ClientSecret)
	if err != nil {
		fmt.Printf("CatieAPI could not decrypt Discord Client Secret: %v\n", err)
		secret = ""
	}
	s.discordClientID = settings.ClientID
	s.discordClientSecret = secret
	s.discordRedirectURI = settings.RedirectURI
	s.discordAllowedGuildID = settings.AllowedGuildID
	s.discordAllowedRoleID = settings.AllowedRoleID
	s.authSuccessURL = settings.AuthSuccessURL
	if settings.SessionTTLHours > 0 {
		s.sessionTTL = time.Duration(settings.SessionTTLHours) * time.Hour
	}
	if !settings.Enabled {
		s.discordClientID = ""
	}
}

func (s *Server) migrateDemoSeedData() bool {
	changed := false
	users := make([]User, 0, len(s.state.Users))
	for _, user := range s.state.Users {
		if isDemoSeedUser(user) {
			changed = true
			continue
		}
		if clearDemoSeedMetrics(&user) {
			changed = true
		}
		users = append(users, user)
	}
	s.state.Users = users

	keys := make([]APIKey, 0, len(s.state.APIKeys))
	for _, key := range s.state.APIKeys {
		if isDemoSeedAPIKey(key) {
			changed = true
			continue
		}
		keys = append(keys, key)
	}
	s.state.APIKeys = keys

	channels := make([]Channel, 0, len(s.state.Channels))
	for _, channel := range s.state.Channels {
		if isDemoSeedChannel(channel) {
			changed = true
			continue
		}
		channels = append(channels, channel)
	}
	s.state.Channels = channels

	models := make([]Model, 0, len(s.state.Models))
	for _, model := range s.state.Models {
		if isDemoSeedModel(model) {
			changed = true
			continue
		}
		models = append(models, model)
	}
	s.state.Models = models

	logs := make([]RequestLog, 0, len(s.state.Logs))
	for _, log := range s.state.Logs {
		if isDemoSeedRequestID(log.ID) {
			changed = true
			continue
		}
		logs = append(logs, log)
	}
	s.state.Logs = logs

	ledger := make([]QuotaEntry, 0, len(s.state.QuotaLedger))
	for _, entry := range s.state.QuotaLedger {
		if isDemoSeedRequestID(entry.RequestID) {
			changed = true
			continue
		}
		ledger = append(ledger, entry)
	}
	s.state.QuotaLedger = ledger

	if isDemoDiscordSettings(s.state.Settings.Discord) {
		s.state.Settings.Discord = DiscordSettings{}
		changed = true
	}
	return changed
}

func isDemoSeedUser(user User) bool {
	switch user.ID {
	case "usr_1001":
		return user.Name == "林可" && user.Email == "lin@example.com"
	case "usr_1002":
		return user.Name == "Mika" && user.Email == "mika@example.com"
	case "usr_1003":
		return user.Name == "测试账号" && user.Email == "trial@example.com"
	default:
		return false
	}
}

func clearDemoSeedMetrics(user *User) bool {
	if user.ID != "usr_1001" || user.Balance != 128.5 || user.RequestsToday != 42 || user.TotalRequests != 1380 {
		return false
	}
	user.Balance = 0
	user.RequestsToday = 0
	user.TotalRequests = 0
	if user.Note == "内部测试管理员" {
		user.Note = ""
	}
	return true
}

func isDemoSeedAPIKey(key APIKey) bool {
	switch key.ID {
	case "key_1001":
		return key.UserID == "usr_1001" && (key.Prefix == "cat_admin" || key.Prefix == "cat_sk_admin" || key.Name == "Dashboard Key")
	case "key_1002":
		return key.UserID == "usr_1002" && (key.Prefix == "cat_live" || key.Prefix == "cat_sk_live" || key.Name == "App Key")
	default:
		return false
	}
}

func isDemoSeedChannel(channel Channel) bool {
	switch channel.ID {
	case "chn_1001":
		return channel.Name == "OpenAI Compatible" && strings.Contains(channel.BaseURL, "api.openai.example")
	case "chn_1002":
		return channel.Name == "Backup Provider" && strings.Contains(channel.BaseURL, "gateway.example")
	default:
		return false
	}
}

func isDemoSeedModel(model Model) bool {
	switch model.ID {
	case "gpt-5.6":
		return model.Name == "GPT-5.6" && model.Vendor == "OpenAI"
	case "gpt-5.5":
		return model.Name == "GPT-5.5" && model.Vendor == "OpenAI"
	case "claude-fable-5":
		return model.Name == "Claude Fable 5" && model.Vendor == "Claude"
	case "gemini-3.1":
		return model.Name == "Gemini 3.1" && model.Vendor == "Google"
	case "deepseek-v4":
		return model.Name == "DeepSeek V4" && model.Vendor == "DeepSeek"
	default:
		return false
	}
}

func isDemoSeedRequestID(id string) bool {
	return id == "req_9001" || id == "req_9002"
}

func isDemoDiscordSettings(settings DiscordSettings) bool {
	if !settings.Managed || settings.ClientSecret != "" {
		return false
	}
	return strings.Contains(settings.RedirectURI, "localhost:8787") ||
		strings.Contains(settings.RedirectURI, "your-domain.example") ||
		strings.Contains(settings.AuthSuccessURL, "your-domain.example")
}

func (s *Server) migrateModelPricing() bool {
	changed := false
	for i := range s.state.Models {
		model := &s.state.Models[i]
		if !model.PricingConfigured && (model.InputPricePer1K != 0 || model.OutputPricePer1K != 0) {
			model.InputPricePer1K = 0
			model.OutputPricePer1K = 0
			changed = true
		}
	}
	return changed
}

func (s *Server) normalizeStateCollections() bool {
	changed := false
	for i := range s.state.Channels {
		if s.state.Channels[i].Models == nil {
			s.state.Channels[i].Models = []string{}
			changed = true
		}
	}
	for i := range s.state.Models {
		if s.state.Models[i].Aliases == nil {
			s.state.Models[i].Aliases = []string{}
			changed = true
		}
	}
	return changed
}

func (s *Server) saveStateLocked() {
	if s.persistence == "postgres" {
		s.savePostgresStateLocked()
		return
	}
	if s.persistence != "file" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.dataFile), 0755); err != nil {
		return
	}
	content, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.dataFile, append(content, '\n'), 0644)
}

func (s *Server) initStorage() {
	if s.persistence != "postgres" {
		return
	}
	if s.databaseURL == "" {
		panic("DATABASE_URL is required when PERSISTENCE=postgres")
	}
	db, err := sql.Open("pgx", s.databaseURL)
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(envInt("DATABASE_MAX_OPEN_CONNS", 10))
	db.SetMaxIdleConns(envInt("DATABASE_MAX_IDLE_CONNS", 5))
	db.SetConnMaxLifetime(time.Duration(envInt("DATABASE_CONN_MAX_LIFETIME_MINUTES", 30)) * time.Minute)
	if err := db.Ping(); err != nil {
		panic(err)
	}
	s.db = db
	s.ensurePostgresSchema()
}

func (s *Server) ensurePostgresSchema() {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS catie_state (
  id text PRIMARY KEY,
  data jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS catie_schema_migrations (
  version integer PRIMARY KEY,
  name text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO catie_schema_migrations (version, name)
VALUES (1, 'state_jsonb_snapshot')
ON CONFLICT (version) DO NOTHING;
`)
	if err != nil {
		panic(err)
	}
}

func (s *Server) loadPostgresState() {
	if s.db == nil {
		return
	}
	var content []byte
	err := s.db.QueryRow(`SELECT data FROM catie_state WHERE id = $1`, "default").Scan(&content)
	if err == sql.ErrNoRows {
		s.savePostgresStateLocked()
		return
	}
	if err != nil {
		panic(err)
	}
	var stored AppState
	if err := json.Unmarshal(content, &stored); err != nil {
		panic(err)
	}
	if stored.Users != nil {
		s.state.Users = stored.Users
	}
	if stored.APIKeys != nil {
		s.state.APIKeys = stored.APIKeys
	}
	if stored.Channels != nil {
		s.state.Channels = stored.Channels
	}
	if stored.Models != nil {
		s.state.Models = stored.Models
	}
	if stored.QuotaLedger != nil {
		s.state.QuotaLedger = stored.QuotaLedger
	}
	if stored.Logs != nil {
		s.state.Logs = stored.Logs
	}
	if stored.Accounts != nil {
		s.state.Accounts = stored.Accounts
	}
	if stored.Settings.Discord.Managed || stored.Settings.Auth.Managed {
		s.state.Settings = stored.Settings
	}
	changed := false
	if s.migrateDemoSeedData() {
		changed = true
	}
	if s.migrateModelPricing() {
		changed = true
	}
	if s.normalizeStateCollections() {
		changed = true
	}
	if changed {
		s.savePostgresStateLocked()
	}
}

func (s *Server) savePostgresStateLocked() {
	if s.db == nil {
		return
	}
	content, err := json.Marshal(s.state)
	if err != nil {
		return
	}
	_, err = s.db.Exec(`
INSERT INTO catie_state (id, data, updated_at)
VALUES ($1, $2::jsonb, now())
ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data, updated_at = now()
`, "default", string(content))
	if err != nil {
		panic(err)
	}
}

func defaultState() AppState {
	return AppState{
		Users:       []User{},
		APIKeys:     []APIKey{},
		Channels:    []Channel{},
		Models:      []Model{},
		QuotaLedger: []QuotaEntry{},
		Logs:        []RequestLog{},
		Accounts:    []Account{},
	}
}

func loadDotEnv(path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

func env(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func unixNow() int64 {
	return time.Now().Unix()
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixMilli(), randomHex(4))
}

func randomHex(length int) string {
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

func bearerToken(authHeader string) string {
	authHeader = strings.TrimSpace(authHeader)
	if authHeader == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return authHeader
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func keyMatchesSecret(key *APIKey, secret string) bool {
	if key.Hash != "" {
		return subtle.ConstantTimeCompare([]byte(key.Hash), []byte(hashSecret(secret))) == 1
	}
	return key.Prefix != "" && strings.HasPrefix(secret, key.Prefix)
}

func deriveSecretKey(secret string) []byte {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func (s *Server) protectSecret(secret string) (string, error) {
	if secret == "" || len(s.secretKey) == 0 || strings.HasPrefix(secret, "enc:v1:") {
		return secret, nil
	}
	block, err := aes.NewCipher(s.secretKey)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, []byte(secret), nil)
	return "enc:v1:" + base64.RawURLEncoding.EncodeToString(nonce) + ":" + base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func (s *Server) revealSecret(secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" || !strings.HasPrefix(secret, "enc:v1:") {
		return secret, nil
	}
	if len(s.secretKey) == 0 {
		return "", fmt.Errorf("SECRET_KEY is required to decrypt stored secret")
	}
	parts := strings.Split(secret, ":")
	if len(parts) != 4 {
		return "", fmt.Errorf("invalid encrypted secret format")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.secretKey)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func estimateTokens(messages []ChatMessage) int {
	characters := 0
	for _, message := range messages {
		characters += len([]rune(message.Role))
		switch content := message.Content.(type) {
		case string:
			characters += len([]rune(content))
		default:
			encoded, _ := json.Marshal(content)
			characters += len([]rune(string(encoded)))
		}
	}
	tokens := characters / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}

func calculateCallCost(model Model, response gin.H, messages []ChatMessage, stream bool) float64 {
	promptTokens := estimateTokens(messages)
	completionTokens := 18
	if !stream && response != nil {
		if usage, ok := response["usage"].(map[string]interface{}); ok {
			if value, ok := asInt(usage["prompt_tokens"]); ok {
				promptTokens = value
			}
			if value, ok := asInt(usage["completion_tokens"]); ok {
				completionTokens = value
			}
		}
		if usage, ok := response["usage"].(gin.H); ok {
			if value, ok := asInt(usage["prompt_tokens"]); ok {
				promptTokens = value
			}
			if value, ok := asInt(usage["completion_tokens"]); ok {
				completionTokens = value
			}
		}
	}

	inputRate, outputRate := modelRates(model)
	cost := (float64(promptTokens)/1000)*inputRate + (float64(completionTokens)/1000)*outputRate
	if cost > 0 && cost < 0.0001 {
		cost = 0.0001
	}
	return round4(cost)
}

func modelRates(model Model) (float64, float64) {
	if !model.PricingConfigured {
		return 0, 0
	}
	return model.InputPricePer1K, model.OutputPricePer1K
}

func completionPrompt(value interface{}) string {
	switch prompt := value.(type) {
	case string:
		return prompt
	case []interface{}:
		parts := []string{}
		for _, item := range prompt {
			parts = append(parts, completionPrompt(item))
		}
		return strings.Join(parts, "\n")
	case []string:
		return strings.Join(prompt, "\n")
	case nil:
		return ""
	default:
		encoded, _ := json.Marshal(prompt)
		return string(encoded)
	}
}

func responseInputMessages(payload map[string]interface{}) []ChatMessage {
	messages := []ChatMessage{}
	if instructions := completionPrompt(payload["instructions"]); strings.TrimSpace(instructions) != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: instructions})
	}
	input, ok := payload["input"]
	if !ok {
		if rawMessages, exists := payload["messages"]; exists {
			input = rawMessages
		}
	}
	switch value := input.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			messages = append(messages, ChatMessage{Role: "user", Content: value})
		}
	case []interface{}:
		for _, item := range value {
			if message, ok := responseInputMessage(item); ok {
				messages = append(messages, message)
			}
		}
	default:
		if text := completionPrompt(value); strings.TrimSpace(text) != "" {
			messages = append(messages, ChatMessage{Role: "user", Content: text})
		}
	}
	return messages
}

func responseInputMessage(value interface{}) (ChatMessage, bool) {
	item, ok := value.(map[string]interface{})
	if !ok {
		text := completionPrompt(value)
		return ChatMessage{Role: "user", Content: text}, strings.TrimSpace(text) != ""
	}
	role, _ := item["role"].(string)
	if role == "" {
		role = "user"
	}
	content, exists := item["content"]
	if !exists {
		content = item["input_text"]
	}
	if content == nil {
		return ChatMessage{}, false
	}
	return ChatMessage{Role: role, Content: content}, true
}

func responseFromChatCompletion(chat gin.H, model Model) gin.H {
	text := assistantTextFromChatCompletion(chat)
	id, _ := chat["id"].(string)
	if id == "" {
		id = newID("resp")
	}
	usage := chat["usage"]
	return gin.H{
		"id":                  id,
		"object":              "response",
		"created_at":          unixNow(),
		"model":               model.ID,
		"status":              "completed",
		"output_text":         text,
		"parallel_tool_calls": true,
		"output": []gin.H{
			{
				"id":      newID("msg"),
				"type":    "message",
				"status":  "completed",
				"role":    "assistant",
				"content": []gin.H{{"type": "output_text", "text": text}},
			},
		},
		"usage": usage,
	}
}

func assistantTextFromChatCompletion(chat gin.H) string {
	choices, ok := chat["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		if typedChoices, ok := chat["choices"].([]gin.H); ok && len(typedChoices) > 0 {
			return assistantTextFromChoice(typedChoices[0])
		}
		return ""
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return ""
	}
	return assistantTextFromChoice(choice)
}

func assistantTextFromChoice(choice map[string]interface{}) string {
	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		return completionPrompt(choice["text"])
	}
	return completionPrompt(message["content"])
}

func providerErrorFromUpstream(status int, content []byte) *ProviderError {
	code := "upstream_error"
	message := strings.TrimSpace(string(content))
	errorType := "api_error"
	var payload struct {
		Error struct {
			Code    interface{} `json:"code"`
			Message string      `json:"message"`
			Type    string      `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(content, &payload); err == nil {
		if payload.Error.Message != "" {
			message = payload.Error.Message
		}
		if payload.Error.Type != "" {
			errorType = payload.Error.Type
		}
		if parsedCode := completionPrompt(payload.Error.Code); strings.TrimSpace(parsedCode) != "" && parsedCode != "null" {
			code = "upstream_" + sanitizeErrorCode(parsedCode)
		}
	}
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = fmt.Sprintf("Upstream returned HTTP %d", status)
	}
	return &ProviderError{Status: status, Code: code, Message: message, Type: errorType}
}

func sanitizeErrorCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			builder.WriteRune(character)
			continue
		}
		if character == '_' || character == '-' || character == '.' {
			builder.WriteRune('_')
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "error"
	}
	return result
}

func joinURL(base string, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func requestIDFromContext(c *gin.Context) string {
	value, ok := c.Get("requestId")
	if !ok {
		return newID("req")
	}
	id, ok := value.(string)
	if !ok || id == "" {
		return newID("req")
	}
	return id
}

func toOpenAIModel(model Model) gin.H {
	return gin.H{
		"id":       model.ID,
		"object":   "model",
		"created":  1780000000,
		"owned_by": strings.ToLower(model.Vendor),
	}
}

func publicAPIKey(key APIKey) PublicAPIKey {
	return PublicAPIKey{
		ID:           key.ID,
		UserID:       key.UserID,
		Name:         key.Name,
		Prefix:       key.Prefix,
		Status:       key.Status,
		CreatedAt:    key.CreatedAt,
		LastUsedAt:   key.LastUsedAt,
		RequestCount: key.RequestCount,
	}
}

func publicChannel(channel Channel) PublicChannel {
	return PublicChannel{
		ID:             channel.ID,
		Name:           channel.Name,
		Provider:       channel.Provider,
		BaseURL:        channel.BaseURL,
		UpstreamKeySet: strings.TrimSpace(channel.UpstreamAPIKey) != "",
		Status:         channel.Status,
		Priority:       channel.Priority,
		Weight:         channel.Weight,
		Models:         append([]string{}, channel.Models...),
		LastCheckedAt:  channel.LastCheckedAt,
		LastError:      channel.LastError,
	}
}

func validationError(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"error": gin.H{
			"message": message,
			"type":    "invalid_request_error",
			"code":    "validation_error",
		},
	})
}

func allowedString(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func nullableString(ok bool, value string) interface{} {
	if !ok {
		return nil
	}
	return value
}

func stringPtr(value string) *string {
	return &value
}

func asFloat(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	default:
		return 0, false
	}
}

func asInt(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func stringSlice(values []interface{}) []string {
	result := []string{}
	for _, value := range values {
		if str, ok := value.(string); ok {
			result = append(result, str)
		}
	}
	return result
}

func round4(value float64) float64 {
	result, err := strconv.ParseFloat(fmt.Sprintf("%.4f", value), 64)
	if err != nil {
		return value
	}
	return result
}
