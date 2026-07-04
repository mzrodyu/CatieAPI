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
)

type AppState struct {
	Users       []User       `json:"users"`
	APIKeys     []APIKey     `json:"apiKeys"`
	Channels    []Channel    `json:"channels"`
	Models      []Model      `json:"models"`
	QuotaLedger []QuotaEntry `json:"quotaLedger"`
	Logs        []RequestLog `json:"logs"`
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
}

type Model struct {
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
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}

type AuthContext struct {
	User *User
	Key  *APIKey
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Stream   bool          `json:"stream"`
	Messages []ChatMessage `json:"messages"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
		corsOrigin:            env("CORS_ORIGIN", "*"),
		adminToken:            env("ADMIN_TOKEN", ""),
		secretKey:             deriveSecretKey(env("SECRET_KEY", "")),
		requestLimitPerMinute: envInt("REQUEST_LIMIT_PER_MINUTE", 60),
		providerMode:          env("PROVIDER_MODE", "mock"),
		upstreamAPIKey:        env("UPSTREAM_API_KEY", ""),
		upstreamTimeout:       time.Duration(envInt("UPSTREAM_TIMEOUT_SECONDS", 60)) * time.Second,
		httpClient:            &http.Client{Timeout: time.Duration(envInt("UPSTREAM_TIMEOUT_SECONDS", 60)) * time.Second},
		discordClientID:       env("DISCORD_CLIENT_ID", ""),
		discordClientSecret:   env("DISCORD_CLIENT_SECRET", ""),
		discordRedirectURI:    env("DISCORD_REDIRECT_URI", "http://localhost:8787/api/auth/discord/callback"),
		discordAllowedGuildID: env("DISCORD_ALLOWED_GUILD_ID", ""),
		discordAllowedRoleID:  env("DISCORD_ALLOWED_ROLE_ID", ""),
		discordOAuthBase:      env("DISCORD_OAUTH_BASE", "https://discord.com/api/oauth2"),
		discordAPIBase:        env("DISCORD_API_BASE", "https://discord.com/api/v10"),
		authSuccessURL:        env("AUTH_SUCCESS_URL", "http://localhost:5173/"),
		sessionTTL:            time.Duration(envInt("SESSION_TTL_HOURS", 168)) * time.Hour,
		rateLimitBuckets:      map[string]int{},
		idempotencyCache:      map[string]CachedResponse{},
		authStates:            map[string]time.Time{},
		sessions:              map[string]Session{},
	}
	s.initStorage()
	s.loadState()
	return s
}

func (s *Server) registerRoutes(router *gin.Engine) {
	api := router.Group("/api")
	api.GET("/health", s.health)
	api.GET("/config/status", s.configStatus)
	api.GET("/auth/discord/start", s.discordStart)
	api.GET("/auth/discord/callback", s.discordCallback)
	api.GET("/auth/session", s.currentSession)
	api.POST("/auth/logout", s.logout)

	admin := api.Group("")
	admin.Use(s.adminMiddleware())
	admin.GET("/overview", s.overview)
	admin.GET("/users", s.listUsers)
	admin.GET("/users/:id", s.getUser)
	admin.PATCH("/users/:id", s.updateUser)
	admin.POST("/users/:id/api-keys", s.createAPIKey)
	admin.PATCH("/api-keys/:id", s.updateAPIKey)
	admin.GET("/channels", s.listChannels)
	admin.PATCH("/channels/:id", s.updateChannel)
	admin.GET("/models", s.listModels)
	admin.PATCH("/models/:id", s.updateModel)
	admin.GET("/logs", s.listLogs)
	admin.GET("/quota-ledger", s.quotaLedger)

	router.GET("/v1/models", s.openAIModels)
	router.GET("/v1/models/:id", s.openAIModel)
	router.POST("/v1/chat/completions", s.chatCompletions)
	router.GET("/models", s.openAIModels)
	router.GET("/models/:id", s.openAIModel)
	router.POST("/chat/completions", s.chatCompletions)

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
		c.Header("Access-Control-Allow-Origin", s.corsOrigin)
		if s.corsOrigin != "*" {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-ID")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (s *Server) adminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.adminToken == "" {
			c.Next()
			return
		}
		token := bearerToken(c.GetHeader("Authorization"))
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) != 1 {
			if _, ok := s.sessionFromRequest(c); ok {
				c.Next()
				return
			}
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "Invalid admin token",
					"type":    "invalid_request_error",
					"code":    "invalid_admin_token",
				},
			})
			c.Abort()
			return
		}
		c.Next()
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
		"discordLoginEnabled":     s.discordLoginEnabled(),
		"discordGuildGate":        s.discordAllowedGuildID != "",
		"discordRoleGate":         s.discordAllowedRoleID != "",
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
			"discordLogin":    s.discordLoginEnabled(),
		},
	})
}

func (s *Server) discordStart(c *gin.Context) {
	if !s.discordLoginEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Discord login is not configured"}})
		return
	}
	if s.discordAllowedRoleID != "" && s.discordAllowedGuildID == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "DISCORD_ALLOWED_GUILD_ID is required when DISCORD_ALLOWED_ROLE_ID is set"}})
		return
	}
	state := randomHex(16)
	expiresAt := time.Now().Add(10 * time.Minute)
	s.mu.Lock()
	s.authStates[state] = expiresAt
	s.mu.Unlock()

	values := url.Values{}
	values.Set("client_id", s.discordClientID)
	values.Set("redirect_uri", s.discordRedirectURI)
	values.Set("response_type", "code")
	values.Set("scope", "identify guilds.members.read")
	values.Set("state", state)
	c.Redirect(http.StatusFound, strings.TrimRight(s.discordOAuthBase, "/")+"/authorize?"+values.Encode())
}

func (s *Server) discordCallback(c *gin.Context) {
	if !s.discordLoginEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Discord login is not configured"}})
		return
	}
	if s.discordAllowedRoleID != "" && s.discordAllowedGuildID == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "DISCORD_ALLOWED_GUILD_ID is required when DISCORD_ALLOWED_ROLE_ID is set"}})
		return
	}
	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" || !s.consumeAuthState(state) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid Discord OAuth state"}})
		return
	}

	token, err := s.exchangeDiscordCode(code)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	user, err := s.fetchDiscordUser(token.AccessToken)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	roleID := ""
	if s.discordAllowedGuildID != "" {
		member, err := s.fetchDiscordGuildMember(token.AccessToken, s.discordAllowedGuildID)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Discord server membership required"}})
			return
		}
		if s.discordAllowedRoleID != "" && !containsString(member.Roles, s.discordAllowedRoleID) {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Discord role required"}})
			return
		}
		roleID = s.discordAllowedRoleID
	}

	session := s.createSession(user, s.discordAllowedGuildID, roleID)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "catie_session",
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(s.sessionTTL),
	})
	c.Redirect(http.StatusFound, s.authSuccessURL)
}

func (s *Server) currentSession(c *gin.Context) {
	session, ok := s.sessionFromRequest(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"authenticated": false})
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

	successRate := 100
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
		user.Status = value
	}
	if value, ok := patch["role"].(string); ok {
		if !allowedString(value, "admin", "user") {
			validationError(c, "Invalid user role")
			return
		}
		user.Role = value
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

func (s *Server) listChannels(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channels := []PublicChannel{}
	for _, channel := range s.state.Channels {
		channels = append(channels, publicChannel(channel))
	}
	c.JSON(http.StatusOK, gin.H{"channels": channels})
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
	s.saveStateLocked()

	c.JSON(http.StatusOK, gin.H{"channel": publicChannel(*channel)})
}

func (s *Server) listModels(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"models": s.state.Models})
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
	if value, ok := asFloat(patch["inputPricePer1K"]); ok {
		if value < 0 {
			validationError(c, "Input price must be greater than or equal to 0")
			return
		}
		model.InputPricePer1K = round4(value)
	}
	if value, ok := asFloat(patch["outputPricePer1K"]); ok {
		if value < 0 {
			validationError(c, "Output price must be greater than or equal to 0")
			return
		}
		model.OutputPricePer1K = round4(value)
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
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body", "invalid_request_error", nil)
		return
	}

	s.mu.Lock()

	auth := s.findUserByAPIKeyLocked(c.GetHeader("Authorization"))
	if auth == nil {
		s.openAIErrorLocked(c, http.StatusUnauthorized, "invalid_api_key", "Invalid CatieAPI key", "invalid_request_error", nil)
		s.mu.Unlock()
		return
	}
	if auth.User.Balance <= 0 || auth.User.Status == "limited" {
		s.openAIErrorLocked(c, http.StatusPaymentRequired, "insufficient_quota", "Insufficient quota", "billing_error", nil)
		s.mu.Unlock()
		return
	}
	if !s.checkRateLimitLocked(auth.Key) {
		s.openAIErrorLocked(c, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded", "rate_limit_error", nil)
		s.mu.Unlock()
		return
	}
	if body.Messages == nil {
		s.openAIErrorLocked(c, http.StatusBadRequest, "invalid_messages", "messages must be an array", "invalid_request_error", stringPtr("messages"))
		s.mu.Unlock()
		return
	}

	model := s.resolveModelLocked(body.Model)
	if model == nil || model.Status != "available" {
		name := body.Model
		if name == "" {
			name = "gpt-5.6"
		}
		s.openAIErrorLocked(c, http.StatusBadRequest, "model_not_available", "No available model: "+name, "invalid_request_error", stringPtr("model"))
		s.mu.Unlock()
		return
	}

	channel := s.primaryChannelLocked(model.ID)
	if channel == nil {
		s.openAIErrorLocked(c, http.StatusBadRequest, "model_not_available", "No available channel for model: "+model.ID, "invalid_request_error", stringPtr("model"))
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
	s.mu.Lock()
	s.recordSuccessfulCallLocked(authUserID, authKeyID, authKeyPrefix, modelCopy.ID, channelCopy.Name, requestID, cost, startedAt)
	if idempotencyKey != "" {
		s.idempotencyCache[idempotencyKey] = CachedResponse{Status: http.StatusOK, Body: responseBody, CreatedAt: now()}
	}
	s.mu.Unlock()
	c.JSON(http.StatusOK, responseBody)
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
	if strings.EqualFold(s.providerMode, "compatible") {
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
		message := strings.TrimSpace(string(content))
		if message == "" {
			message = response.Status
		}
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_error", Message: message, Type: "api_error"}
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
		message := strings.TrimSpace(string(content))
		if message == "" {
			message = response.Status
		}
		return &ProviderError{Status: http.StatusBadGateway, Code: "upstream_error", Message: message, Type: "api_error"}
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	_, _ = io.Copy(c.Writer, response.Body)
	c.Writer.Flush()
	return nil
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

	payload := gin.H{
		"model":    call.Model.ID,
		"messages": call.Body.Messages,
		"stream":   stream,
	}
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
	if strings.EqualFold(s.providerMode, "compatible") {
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

func (s *Server) discordLoginEnabled() bool {
	return s.discordClientID != "" && s.discordClientSecret != "" && s.discordRedirectURI != ""
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

func (s *Server) exchangeDiscordCode(code string) (*DiscordTokenResponse, error) {
	values := url.Values{}
	values.Set("client_id", s.discordClientID)
	values.Set("client_secret", s.discordClientSecret)
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("redirect_uri", s.discordRedirectURI)

	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(s.discordOAuthBase, "/")+"/token", strings.NewReader(values.Encode()))
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

func (s *Server) createSession(user *DiscordUser, guildID string, roleID string) Session {
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
		CreatedAt: now(),
		ExpiresAt: time.Now().Add(s.sessionTTL).UTC().Format(time.RFC3339Nano),
	}
	s.mu.Lock()
	s.sessions[session.ID] = session
	s.mu.Unlock()
	return session
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

func (s *Server) findUserByAPIKeyLocked(authHeader string) *AuthContext {
	token := bearerToken(authHeader)
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

func (s *Server) resolveModelLocked(input string) *Model {
	value := strings.TrimSpace(input)
	if value == "" {
		value = "gpt-5.6"
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
	changed := false
	if s.migrateSeedKeys() {
		changed = true
	}
	if s.migrateModelPricing() {
		changed = true
	}
	if changed {
		s.saveStateLocked()
	}
}

func (s *Server) migrateSeedKeys() bool {
	changed := false
	for i := range s.state.APIKeys {
		key := &s.state.APIKeys[i]
		switch key.ID {
		case "key_1001":
			if key.Prefix == "cat_sk_admin" || key.Hash == hashSecret("cat_sk_admin_test") {
				key.Prefix = "cat_admin"
				key.Hash = hashSecret("cat_admin_test")
				changed = true
			}
		case "key_1002":
			if key.Prefix == "cat_sk_live" || key.Hash == hashSecret("cat_sk_live_test") {
				key.Prefix = "cat_live"
				key.Hash = hashSecret("cat_live_test")
				changed = true
			}
		}
	}
	return changed
}

func (s *Server) migrateModelPricing() bool {
	changed := false
	for i := range s.state.Models {
		model := &s.state.Models[i]
		if model.InputPricePer1K == 0 && model.OutputPricePer1K == 0 {
			inputRate, outputRate := modelRates(*model)
			model.InputPricePer1K = inputRate
			model.OutputPricePer1K = outputRate
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
	changed := false
	if s.migrateSeedKeys() {
		changed = true
	}
	if s.migrateModelPricing() {
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
		Users: []User{
			{ID: "usr_1001", Name: "林可", Email: "lin@example.com", Role: "admin", Status: "active", Balance: 128.5, RequestsToday: 42, TotalRequests: 1380, CreatedAt: "2026-07-01T10:00:00.000Z", LastLoginAt: "2026-07-04T01:10:00.000Z", Note: "内部测试管理员"},
			{ID: "usr_1002", Name: "Mika", Email: "mika@example.com", Role: "user", Status: "active", Balance: 36.2, RequestsToday: 18, TotalRequests: 526, CreatedAt: "2026-07-02T08:30:00.000Z", LastLoginAt: "2026-07-03T21:14:00.000Z", Note: "普通用户"},
			{ID: "usr_1003", Name: "测试账号", Email: "trial@example.com", Role: "user", Status: "limited", Balance: 2.4, RequestsToday: 5, TotalRequests: 80, CreatedAt: "2026-07-03T12:20:00.000Z", LastLoginAt: "2026-07-03T23:48:00.000Z", Note: "额度偏低"},
		},
		APIKeys: []APIKey{
			{ID: "key_1001", UserID: "usr_1001", Name: "Dashboard Key", Prefix: "cat_admin", Hash: hashSecret("cat_admin_test"), Status: "active", CreatedAt: "2026-07-01T10:30:00.000Z", LastUsedAt: "2026-07-04T01:20:00.000Z", RequestCount: 910},
			{ID: "key_1002", UserID: "usr_1002", Name: "App Key", Prefix: "cat_live", Hash: hashSecret("cat_live_test"), Status: "active", CreatedAt: "2026-07-02T09:12:00.000Z", LastUsedAt: "2026-07-03T21:28:00.000Z", RequestCount: 526},
		},
		Channels: []Channel{
			{ID: "chn_1001", Name: "OpenAI Compatible", Provider: "openai", BaseURL: "https://api.openai.example/v1", Status: "healthy", Priority: 1, Weight: 100, Models: []string{"gpt-5.6", "gpt-5.5"}},
			{ID: "chn_1002", Name: "Backup Provider", Provider: "compatible", BaseURL: "https://gateway.example/v1", Status: "standby", Priority: 2, Weight: 20, Models: []string{"claude-fable-5", "gemini-3.1", "deepseek-v4"}},
		},
		Models: []Model{
			{ID: "gpt-5.6", Name: "GPT-5.6", Vendor: "OpenAI", Aliases: []string{"安全区", "gpt"}, Category: "通用", Description: "主力通用模型，适合复杂对话、工具调用和综合任务。", Price: "高", InputPricePer1K: 0.03, OutputPricePer1K: 0.06, Context: "长上下文", Status: "available", Recommended: true},
			{ID: "gpt-5.5", Name: "GPT-5.5", Vendor: "OpenAI", Aliases: []string{"安全区", "gpt"}, Category: "通用", Description: "均衡通用模型，适合日常调用和产品默认模型。", Price: "中", InputPricePer1K: 0.01, OutputPricePer1K: 0.02, Context: "长上下文", Status: "available", Recommended: false},
			{ID: "claude-fable-5", Name: "Claude Fable 5", Vendor: "Claude", Aliases: []string{"肥波5", "f5", "克", "小克"}, Category: "写作", Description: "适合长文、写作、总结和稳健的对话任务。", Price: "中", InputPricePer1K: 0.01, OutputPricePer1K: 0.02, Context: "长上下文", Status: "available", Recommended: true},
			{ID: "gemini-3.1", Name: "Gemini 3.1", Vendor: "Google", Aliases: []string{"哈基米", "基米"}, Category: "多模态", Description: "适合多模态理解、长文本整理和通用任务。", Price: "中", InputPricePer1K: 0.01, OutputPricePer1K: 0.02, Context: "超长上下文", Status: "available", Recommended: false},
			{ID: "deepseek-v4", Name: "DeepSeek V4", Vendor: "DeepSeek", Aliases: []string{"ds", "deepseek", "鲸鱼"}, Category: "推理", Description: "适合代码、推理和高性价比文本任务。", Price: "低", InputPricePer1K: 0.002, OutputPricePer1K: 0.004, Context: "长上下文", Status: "available", Recommended: true},
		},
		QuotaLedger: []QuotaEntry{},
		Logs: []RequestLog{
			{ID: "req_9001", UserID: stringPtr("usr_1002"), APIKeyPrefix: stringPtr("cat_live"), Model: stringPtr("gpt-5.6"), Channel: stringPtr("OpenAI Compatible"), Status: "success", Cost: 0.04, LatencyMS: 820, CreatedAt: "2026-07-04T01:22:00.000Z"},
			{ID: "req_9002", UserID: stringPtr("usr_1003"), APIKeyPrefix: stringPtr("cat_trial"), Model: stringPtr("deepseek-v4"), Channel: stringPtr("Backup Provider"), Status: "failed", Cost: 0, LatencyMS: 1200, ErrorCode: "upstream_timeout", CreatedAt: "2026-07-04T01:25:00.000Z"},
		},
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
		return "", fmt.Errorf("SECRET_KEY is required to decrypt channel upstream key")
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
		characters += len([]rune(message.Content))
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
	inputRate := model.InputPricePer1K
	outputRate := model.OutputPricePer1K
	if inputRate > 0 || outputRate > 0 {
		return inputRate, outputRate
	}
	switch model.Price {
	case "高":
		return 0.03, 0.06
	case "低":
		return 0.002, 0.004
	default:
		return 0.01, 0.02
	}
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
