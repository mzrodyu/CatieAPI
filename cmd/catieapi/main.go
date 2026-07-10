package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
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
	"math"
	"mime/multipart"
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

const (
	defaultUpstreamTimeoutSeconds = 600
	codexDirectImageTimeout       = 45 * time.Second
	openAIUsageLimitRetryAfter    = 5 * time.Hour
	defaultOpenAIBaseURL          = "https://api.openai.com/v1"
	defaultChatGPTAPIBaseURL      = "https://chatgpt.com/backend-api"
	chatGPTCodexCLIProfile        = "codex_cli_rs"
	chatGPTCodexTUIProfile        = "codex-tui"
	chatGPTCodexVSCodeProfile     = "codex_vscode"
	chatGPTCodexCLIUserAgent      = "codex_cli_rs/0.125.0 (Ubuntu 22.4.0; x86_64) xterm-256color"
	chatGPTCodexTUIUserAgent      = "codex-tui/0.135.0 (Mac OS 26.5.0; arm64) iTerm.app/3.6.10 (codex-tui; 0.135.0)"
	chatGPTCodexVSCodeUserAgent   = "codex_vscode/0.125.0"
	chatGPTCodexVersion           = "0.125.0"
	chatGPTCodexTUIVersion        = "0.135.0"
	chatGPTCodexImageModel        = "gpt-5.4-mini"
	chatGPTCodexImageInstructions = "You are an image art director for the image_generation tool. Before calling the tool, internally turn the user's request into a precise visual brief: subject, composition, style, lighting, color, materials, camera/framing, and negative constraints when useful. Preserve any explicit user wording, characters, brands, reference-image intent, aspect ratio, and requested text exactly. Do not add visible text, logos, watermarks, UI labels, or poster typography unless the user explicitly asks for them."
	openAIAuthClientID            = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIOAuthRedirectURI        = "http://localhost:1455/auth/callback"
	openAIOAuthScope              = "openid profile email offline_access"
)

// These are injected by a release build with -ldflags. Local builds retain
// clear values so the admin page can distinguish a rebuilt binary from a
// deployed release.
var (
	buildVersion = "dev"
	buildCommit  = "local"
	buildTime    = ""
)

var imageJSONKeepaliveInterval = 8 * time.Second

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
	Discord     DiscordSettings     `json:"discord,omitempty"`
	Auth        AuthSettings        `json:"auth,omitempty"`
	Maintenance MaintenanceSettings `json:"maintenance,omitempty"`
}

type MaintenanceSettings struct {
	Managed          bool `json:"managed,omitempty"`
	LogRetentionDays int  `json:"logRetentionDays"`
	MaxLogs          int  `json:"maxLogs"`
	MaxQuotaEntries  int  `json:"maxQuotaEntries"`
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
	ID                 string   `json:"id"`
	UserID             string   `json:"userId"`
	Name               string   `json:"name"`
	Prefix             string   `json:"prefix"`
	Hash               string   `json:"hash,omitempty"`
	Status             string   `json:"status"`
	CreatedAt          string   `json:"createdAt"`
	LastUsedAt         string   `json:"lastUsedAt"`
	RequestCount       int      `json:"requestCount"`
	AllowedModels      []string `json:"allowedModels"`
	ExpiresAt          string   `json:"expiresAt,omitempty"`
	RateLimitPerMinute int      `json:"rateLimitPerMinute,omitempty"`
}

type PublicAPIKey struct {
	ID                 string   `json:"id"`
	UserID             string   `json:"userId"`
	Name               string   `json:"name"`
	Prefix             string   `json:"prefix"`
	Status             string   `json:"status"`
	CreatedAt          string   `json:"createdAt"`
	LastUsedAt         string   `json:"lastUsedAt"`
	RequestCount       int      `json:"requestCount"`
	AllowedModels      []string `json:"allowedModels"`
	ExpiresAt          string   `json:"expiresAt,omitempty"`
	RateLimitPerMinute int      `json:"rateLimitPerMinute,omitempty"`
}

type Channel struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Provider          string          `json:"provider"`
	BaseURL           string          `json:"baseUrl"`
	UpstreamAPIKey    string          `json:"upstreamApiKey,omitempty"`
	OpenAIAccounts    []OpenAIAccount `json:"openaiAccounts,omitempty"`
	Status            string          `json:"status"`
	StreamMode        string          `json:"streamMode"`
	Priority          int             `json:"priority"`
	Weight            int             `json:"weight"`
	Models            []string        `json:"models"`
	InputPricePer1K   float64         `json:"inputPricePer1K"`
	OutputPricePer1K  float64         `json:"outputPricePer1K"`
	PricingConfigured bool            `json:"pricingConfigured"`
	WebEndpoint       bool            `json:"webEndpoint,omitempty"`
	LastCheckedAt     string          `json:"lastCheckedAt,omitempty"`
	LastError         string          `json:"lastError,omitempty"`
}

type PublicChannel struct {
	ID                 string                `json:"id"`
	Name               string                `json:"name"`
	Provider           string                `json:"provider"`
	BaseURL            string                `json:"baseUrl"`
	UpstreamKeySet     bool                  `json:"upstreamKeySet"`
	OpenAIAccountCount int                   `json:"openaiAccountCount"`
	OpenAIAccounts     []PublicOpenAIAccount `json:"openaiAccounts,omitempty"`
	Status             string                `json:"status"`
	StreamMode         string                `json:"streamMode"`
	Priority           int                   `json:"priority"`
	Weight             int                   `json:"weight"`
	Models             []string              `json:"models"`
	InputPricePer1K    float64               `json:"inputPricePer1K"`
	OutputPricePer1K   float64               `json:"outputPricePer1K"`
	PricingConfigured  bool                  `json:"pricingConfigured"`
	WebEndpoint        bool                  `json:"webEndpoint"`
	LastCheckedAt      string                `json:"lastCheckedAt"`
	LastError          string                `json:"lastError"`
}

type OpenAIAccount struct {
	ID            string             `json:"id"`
	Name          string             `json:"name,omitempty"`
	Email         string             `json:"email,omitempty"`
	AccessToken   string             `json:"accessToken,omitempty"`
	RefreshToken  string             `json:"refreshToken,omitempty"`
	SessionToken  string             `json:"sessionToken,omitempty"`
	IDToken       string             `json:"idToken,omitempty"`
	AccountID     string             `json:"accountId,omitempty"`
	UserID        string             `json:"userId,omitempty"`
	ExpiresAt     string             `json:"expiresAt,omitempty"`
	LastRefresh   string             `json:"lastRefresh,omitempty"`
	PlanType      string             `json:"planType,omitempty"`
	Source        string             `json:"source,omitempty"`
	ClientProfile string             `json:"clientProfile,omitempty"`
	ImportedAt    string             `json:"importedAt,omitempty"`
	Status        string             `json:"status,omitempty"`
	LastCheckedAt string             `json:"lastCheckedAt,omitempty"`
	LastError     string             `json:"lastError,omitempty"`
	LastErrorCode string             `json:"lastErrorCode,omitempty"`
	LastUsedAt    string             `json:"lastUsedAt,omitempty"`
	RequestCount  int                `json:"requestCount,omitempty"`
	QuotaLimits   []OpenAIQuotaLimit `json:"quotaLimits,omitempty"`
}

type PublicOpenAIAccount struct {
	ID              string             `json:"id"`
	Name            string             `json:"name,omitempty"`
	Email           string             `json:"email,omitempty"`
	AccountID       string             `json:"accountId,omitempty"`
	UserID          string             `json:"userId,omitempty"`
	ExpiresAt       string             `json:"expiresAt,omitempty"`
	LastRefresh     string             `json:"lastRefresh,omitempty"`
	PlanType        string             `json:"planType,omitempty"`
	Source          string             `json:"source,omitempty"`
	ClientProfile   string             `json:"clientProfile,omitempty"`
	ImportedAt      string             `json:"importedAt,omitempty"`
	Status          string             `json:"status,omitempty"`
	LastCheckedAt   string             `json:"lastCheckedAt,omitempty"`
	LastError       string             `json:"lastError,omitempty"`
	LastErrorCode   string             `json:"lastErrorCode,omitempty"`
	LastUsedAt      string             `json:"lastUsedAt,omitempty"`
	RequestCount    int                `json:"requestCount,omitempty"`
	QuotaLimits     []OpenAIQuotaLimit `json:"quotaLimits,omitempty"`
	HasRefreshToken bool               `json:"hasRefreshToken"`
	HasSessionToken bool               `json:"hasSessionToken"`
	CredentialMode  string             `json:"credentialMode"`
}

type OpenAIQuotaLimit struct {
	Name             string  `json:"name"`
	Label            string  `json:"label"`
	Window           string  `json:"window,omitempty"`
	Limit            float64 `json:"limit,omitempty"`
	Used             float64 `json:"used,omitempty"`
	Remaining        float64 `json:"remaining,omitempty"`
	PercentRemaining float64 `json:"percentRemaining,omitempty"`
	ResetAt          string  `json:"resetAt,omitempty"`
}

type ImportedOpenAIAccount struct {
	Name          string
	Email         string
	AccessToken   string
	RefreshToken  string
	SessionToken  string
	IDToken       string
	AccountID     string
	UserID        string
	ExpiresAt     string
	LastRefresh   string
	PlanType      string
	Source        string
	ClientProfile string
}

type OpenAIAccountCheckResult struct {
	Status       string
	Message      string
	ErrorCode    string
	QuotaLimits  []OpenAIQuotaLimit
	PlanType     string
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresAt    string
	LastRefresh  string
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
	Account      *string `json:"account,omitempty"`
	Status       string  `json:"status"`
	Cost         float64 `json:"cost"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	Attempts     int     `json:"attempts"`
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
	openAIRefreshMu       sync.Mutex
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
	webHTTPClient         *http.Client
	chatGPTAPIBase        string
	openAIAuthBase        string
	discordClientID       string
	discordClientSecret   string
	discordRedirectURI    string
	discordAllowedGuildID string
	discordAllowedRoleID  string
	discordOAuthBase      string
	discordAPIBase        string
	authSuccessURL        string
	sessionTTL            time.Duration
	accountHealthInterval time.Duration
	rateLimitBuckets      map[string]int
	idempotencyCache      map[string]CachedResponse
	authStates            map[string]time.Time
	sessions              map[string]Session
	openAIOAuthFlows      map[string]openAIOAuthFlow
	requestAccounts       map[string]string
}

// openAIOAuthFlow tracks one in-progress ChatGPT OAuth (PKCE) authorization so
// the callback can verify state and exchange the code with the right verifier.
type openAIOAuthFlow struct {
	CodeVerifier string
	CreatedAt    time.Time
}

type CachedResponse struct {
	Status    int         `json:"status"`
	Body      interface{} `json:"body"`
	CreatedAt string      `json:"createdAt"`
}

type BackupEnvelope struct {
	Version    int      `json:"version"`
	ExportedAt string   `json:"exportedAt"`
	State      AppState `json:"state"`
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

type ImageRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Payload map[string]interface{} `json:"-"`
}

// EmbeddingRequest intentionally keeps input untyped: the OpenAI API accepts a
// string, token array, or a batch of either, and compatible upstreams expect it
// to be forwarded without the gateway narrowing that shape.
type EmbeddingRequest struct {
	Model   string                 `json:"model"`
	Input   interface{}            `json:"input"`
	Payload map[string]interface{} `json:"-"`
}

type AudioSpeechRequest struct {
	Model   string                 `json:"model"`
	Input   string                 `json:"input"`
	Payload map[string]interface{} `json:"-"`
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

func (request *ImageRequest) UnmarshalJSON(data []byte) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	var typed struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(data, &typed); err != nil {
		return err
	}
	request.Model = typed.Model
	request.Prompt = typed.Prompt
	request.Payload = payload
	return nil
}

func (request *EmbeddingRequest) UnmarshalJSON(data []byte) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	request.Payload = payload
	if model, ok := payload["model"].(string); ok {
		request.Model = model
	}
	request.Input = payload["input"]
	return nil
}

func (request *AudioSpeechRequest) UnmarshalJSON(data []byte) error {
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	request.Payload = payload
	request.Model, _ = payload["model"].(string)
	request.Input, _ = payload["input"].(string)
	return nil
}

type GatewayCall struct {
	RequestID string
	Model     Model
	Channel   Channel
	Body      ChatRequest
}

type ImageGatewayCall struct {
	RequestID string
	Model     Model
	Channel   Channel
	Body      ImageRequest
	Operation string
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

// fetchChatGPTAccessTokenViaSessionCookie exchanges a next-auth session cookie
// for the current ChatGPT access token by calling the upstream session endpoint
// exactly as the browser would. The session cookie is long-lived, so this keeps
// web-login accounts alive without an OAuth refresh token.
func fetchChatGPTAccessTokenViaSessionCookie(sessionToken string) (accessToken string, expiresAt string, err error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	for _, cookie := range chatGPTSessionCookieCandidates(sessionToken) {
		req, requestErr := http.NewRequest(http.MethodGet, "https://chatgpt.com/api/auth/session", nil)
		if requestErr != nil {
			return "", "", requestErr
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.AddCookie(&cookie)
		res, requestErr := client.Do(req)
		if requestErr != nil {
			lastErr = requestErr
			continue
		}
		content, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		_ = res.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			lastErr = fmt.Errorf("session endpoint returned HTTP %d", res.StatusCode)
			continue
		}
		var body struct {
			AccessToken string `json:"accessToken"`
			Expires     string `json:"expires"`
		}
		if json.Unmarshal(content, &body) == nil && strings.TrimSpace(body.AccessToken) != "" {
			return strings.TrimSpace(body.AccessToken), strings.TrimSpace(body.Expires), nil
		}
		lastErr = fmt.Errorf("session endpoint returned no accessToken")
	}
	return "", "", lastErr
}

func chatGPTSessionCookieCandidates(value string) []http.Cookie {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "Cookie:"))
	for _, part := range strings.Split(value, ";") {
		name, token, found := strings.Cut(strings.TrimSpace(part), "=")
		if found && (name == "__Secure-next-auth.session-token" || name == "__Secure-authjs.session-token") && strings.TrimSpace(token) != "" {
			return []http.Cookie{{Name: name, Value: strings.TrimSpace(token)}}
		}
	}
	return []http.Cookie{
		{Name: "__Secure-next-auth.session-token", Value: value},
		{Name: "__Secure-authjs.session-token", Value: value},
	}
}

// persistRefreshedPoolAccount writes refreshed tokens back into the matching
// pool account (found by id) and saves state, re-encrypting secrets at rest.
func (s *Server) persistRefreshedPoolAccount(accountID string, refreshed OpenAIRefreshResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ci := range s.state.Channels {
		accounts := s.state.Channels[ci].OpenAIAccounts
		for ai := range accounts {
			if accounts[ai].ID != accountID {
				continue
			}
			if protected, err := s.protectSecret(refreshed.AccessToken); err == nil {
				accounts[ai].AccessToken = protected
			}
			if strings.TrimSpace(refreshed.RefreshToken) != "" {
				if protected, err := s.protectSecret(refreshed.RefreshToken); err == nil {
					accounts[ai].RefreshToken = protected
				}
			}
			if strings.TrimSpace(refreshed.ExpiresAt) != "" {
				accounts[ai].ExpiresAt = refreshed.ExpiresAt
			}
			accounts[ai].LastRefresh = now()
			s.saveStateLocked()
			return
		}
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
		upstreamTimeout:       time.Duration(envInt("UPSTREAM_TIMEOUT_SECONDS", defaultUpstreamTimeoutSeconds)) * time.Second,
		httpClient:            &http.Client{Timeout: time.Duration(envInt("UPSTREAM_TIMEOUT_SECONDS", defaultUpstreamTimeoutSeconds)) * time.Second},
		chatGPTAPIBase:        env("CHATGPT_API_BASE", defaultChatGPTAPIBaseURL),
		openAIAuthBase:        env("OPENAI_AUTH_BASE", "https://auth.openai.com"),
		discordClientID:       env("DISCORD_CLIENT_ID", ""),
		discordClientSecret:   env("DISCORD_CLIENT_SECRET", ""),
		discordRedirectURI:    env("DISCORD_REDIRECT_URI", ""),
		discordAllowedGuildID: env("DISCORD_ALLOWED_GUILD_ID", ""),
		discordAllowedRoleID:  env("DISCORD_ALLOWED_ROLE_ID", ""),
		discordOAuthBase:      env("DISCORD_OAUTH_BASE", "https://discord.com/api/oauth2"),
		discordAPIBase:        env("DISCORD_API_BASE", "https://discord.com/api/v10"),
		authSuccessURL:        env("AUTH_SUCCESS_URL", ""),
		sessionTTL:            time.Duration(envInt("SESSION_TTL_HOURS", 168)) * time.Hour,
		accountHealthInterval: 15 * time.Minute,
		rateLimitBuckets:      map[string]int{},
		idempotencyCache:      map[string]CachedResponse{},
		authStates:            map[string]time.Time{},
		sessions:              map[string]Session{},
		openAIOAuthFlows:      map[string]openAIOAuthFlow{},
		requestAccounts:       map[string]string{},
	}
	s.webHTTPClient = newChatGPTWebHTTPClient(s.upstreamTimeout)
	s.initStorage()
	s.loadState()
	s.mu.Lock()
	if s.pruneOperationalHistoryLocked() {
		s.saveStateLocked()
	}
	s.mu.Unlock()
	s.applyPersistedDiscordSettings()
	if s.accountHealthInterval > 0 {
		go s.runAccountHealthChecks()
	}
	return s
}

func (s *Server) runAccountHealthChecks() {
	s.checkOpenAIAccountsInBackground()
	ticker := time.NewTicker(s.accountHealthInterval)
	defer ticker.Stop()
	for range ticker.C {
		s.checkOpenAIAccountsInBackground()
	}
}

// checkOpenAIAccountsInBackground intentionally reuses the same credential
// validation path as the UI batch check, but never changes an enabled channel
// to disabled merely because every account is temporarily unavailable.
func (s *Server) checkOpenAIAccountsInBackground() {
	s.mu.Lock()
	channels := append([]Channel{}, s.state.Channels...)
	for i := range channels {
		channels[i].OpenAIAccounts = append([]OpenAIAccount{}, channels[i].OpenAIAccounts...)
	}
	s.mu.Unlock()

	type healthCheckResult struct {
		channelID string
		accountID string
		result    OpenAIAccountCheckResult
	}
	jobs := 0
	for _, channel := range channels {
		jobs += len(channel.OpenAIAccounts)
	}
	results := make(chan healthCheckResult, jobs)
	semaphore := make(chan struct{}, 3)
	var workers sync.WaitGroup
	for _, channel := range channels {
		for _, account := range channel.OpenAIAccounts {
			workers.Add(1)
			go func(channel Channel, account OpenAIAccount) {
				defer workers.Done()
				semaphore <- struct{}{}
				result := s.checkOpenAIAccount(account, channel, false)
				<-semaphore
				results <- healthCheckResult{channelID: channel.ID, accountID: account.ID, result: result}
			}(channel, account)
		}
	}
	go func() {
		workers.Wait()
		close(results)
	}()

	changed := false
	healthyByChannel := map[string]int{}
	for checked := range results {
		if checked.result.Status == "healthy" {
			healthyByChannel[checked.channelID]++
		}
		s.mu.Lock()
		live := s.findChannel(checked.channelID)
		if live != nil {
			for index := range live.OpenAIAccounts {
				if live.OpenAIAccounts[index].ID != checked.accountID {
					continue
				}
				live.OpenAIAccounts[index].Status = checked.result.Status
				live.OpenAIAccounts[index].LastCheckedAt = now()
				live.OpenAIAccounts[index].LastError = truncateString(checked.result.Message, 500)
				live.OpenAIAccounts[index].LastErrorCode = checked.result.ErrorCode
				if checked.result.AccessToken != "" {
					live.OpenAIAccounts[index].AccessToken = checked.result.AccessToken
				}
				if checked.result.RefreshToken != "" {
					live.OpenAIAccounts[index].RefreshToken = checked.result.RefreshToken
				}
				if checked.result.IDToken != "" {
					live.OpenAIAccounts[index].IDToken = checked.result.IDToken
				}
				if checked.result.ExpiresAt != "" {
					live.OpenAIAccounts[index].ExpiresAt = checked.result.ExpiresAt
				}
				if checked.result.LastRefresh != "" {
					live.OpenAIAccounts[index].LastRefresh = checked.result.LastRefresh
				}
				if checked.result.PlanType != "" {
					live.OpenAIAccounts[index].PlanType = checked.result.PlanType
				}
				if checked.result.QuotaLimits != nil {
					live.OpenAIAccounts[index].QuotaLimits = checked.result.QuotaLimits
				}
				changed = true
				break
			}
		}
		s.mu.Unlock()
	}
	for _, channel := range channels {
		s.mu.Lock()
		live := s.findChannel(channel.ID)
		if live != nil && len(channel.OpenAIAccounts) > 0 {
			live.LastCheckedAt = now()
			if live.Status != "disabled" && healthyByChannel[channel.ID] == 0 {
				live.Status, live.LastError = "standby", "账号池暂时没有可用账号"
			} else if live.Status != "disabled" {
				live.Status, live.LastError = "healthy", ""
			}
			changed = true
		}
		s.mu.Unlock()
	}
	if changed {
		s.mu.Lock()
		s.saveStateLocked()
		s.mu.Unlock()
	}
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
	admin.POST("/channels/:id/import-openai-accounts", s.importOpenAIAccounts)
	admin.POST("/channels/:id/openai-oauth/start", s.startOpenAIOAuth)
	admin.POST("/channels/:id/openai-oauth/complete", s.completeOpenAIOAuth)
	admin.POST("/channels/:id/openai-accounts/check", s.checkOpenAIAccounts)
	admin.POST("/channels/:id/openai-accounts/deduplicate", s.deduplicateOpenAIAccounts)
	admin.DELETE("/channels/:id/openai-accounts/:accountId", s.deleteOpenAIAccount)
	admin.PATCH("/channels/:id", s.updateChannel)
	admin.DELETE("/channels/:id", s.deleteChannel)
	admin.POST("/channels/:id/check", s.checkChannel)
	admin.POST("/channels/:id/sync-models", s.syncChannelModels)
	admin.GET("/models", s.listModels)
	admin.POST("/models", s.createModel)
	admin.PATCH("/models/:id", s.updateModel)
	admin.DELETE("/models/:id", s.deleteModel)
	admin.GET("/logs", s.listLogs)
	admin.GET("/logs/:id", s.getLog)
	admin.GET("/quota-ledger", s.quotaLedger)
	admin.GET("/settings/discord", s.getDiscordSettings)
	admin.PATCH("/settings/discord", s.updateDiscordSettings)
	admin.GET("/settings/auth", s.getAuthSettings)
	admin.PATCH("/settings/auth", s.updateAuthSettings)
	admin.GET("/settings/maintenance", s.getMaintenanceSettings)
	admin.PATCH("/settings/maintenance", s.updateMaintenanceSettings)
	admin.GET("/backup", s.exportBackup)
	admin.POST("/restore", s.restoreBackup)

	router.GET("/v1/models", s.openAIModels)
	router.GET("/v1/models/:id", s.openAIModel)
	router.POST("/v1/chat/completions", s.chatCompletions)
	router.POST("/v1/completions", s.completions)
	router.POST("/v1/responses", s.responses)
	router.POST("/v1/embeddings", s.embeddings)
	router.POST("/v1/audio/speech", s.audioSpeech)
	router.POST("/v1/moderations", s.moderations)
	router.POST("/v1/images/generations", s.imageGenerations)
	router.POST("/v1/images/edits", s.imageEdits)
	router.GET("/models", s.openAIModels)
	router.GET("/models/:id", s.openAIModel)
	router.POST("/chat/completions", s.chatCompletions)
	router.POST("/completions", s.completions)
	router.POST("/responses", s.responses)
	router.POST("/embeddings", s.embeddings)
	router.POST("/audio/speech", s.audioSpeech)
	router.POST("/moderations", s.moderations)
	router.POST("/images/generations", s.imageGenerations)
	router.POST("/images/edits", s.imageEdits)

	router.NoRoute(func(c *gin.Context) {
		if normalizedPath := normalizeOpenAIRequestPath(c.Request.URL.Path); normalizedPath != c.Request.URL.Path {
			c.Request.URL.Path = normalizedPath
			c.Request.URL.RawPath = ""
			router.HandleContext(c)
			return
		}
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
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
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
		"version":      buildVersion,
		"commit":       buildCommit,
		"buildTime":    buildTime,
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
		Username            string   `json:"username"`
		Password            string   `json:"password"`
		DisplayName         string   `json:"displayName"`
		Email               string   `json:"email"`
		DiscordUserID       string   `json:"discordUserId"`
		RegistrationEnabled bool     `json:"registrationEnabled"`
		RegistrationMode    string   `json:"registrationMode"`
		DefaultBalance      *float64 `json:"defaultBalance"`
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
	registrationMode := normalizeRegistrationMode(body.RegistrationMode)
	defaultBalance := 0.0
	if body.DefaultBalance != nil {
		if *body.DefaultBalance < 0 || *body.DefaultBalance > 1_000_000_000 {
			validationError(c, "新用户初始额度必须在 0 到 1000000000 之间")
			return
		}
		defaultBalance = round4(*body.DefaultBalance)
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
	s.state.Settings.Auth = AuthSettings{
		Managed:             true,
		RegistrationEnabled: body.RegistrationEnabled,
		RegistrationMode:    registrationMode,
		DefaultBalance:      defaultBalance,
	}
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

func (s *Server) getMaintenanceSettings(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"maintenance": s.maintenanceSettingsLocked()})
}

func (s *Server) updateMaintenanceSettings(c *gin.Context) {
	var body MaintenanceSettings
	if err := c.ShouldBindJSON(&body); err != nil {
		validationError(c, "无效的维护设置")
		return
	}
	if body.LogRetentionDays < 1 || body.LogRetentionDays > 3650 {
		validationError(c, "日志保留天数必须在 1 到 3650 之间")
		return
	}
	if body.MaxLogs < 100 || body.MaxLogs > 1_000_000 {
		validationError(c, "日志最大条数必须在 100 到 1000000 之间")
		return
	}
	if body.MaxQuotaEntries < 100 || body.MaxQuotaEntries > 2_000_000 {
		validationError(c, "额度流水最大条数必须在 100 到 2000000 之间")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	body.Managed = true
	s.state.Settings.Maintenance = body
	s.pruneOperationalHistoryLocked()
	s.saveStateLocked()
	c.JSON(http.StatusOK, gin.H{"maintenance": s.maintenanceSettingsLocked()})
}

func (s *Server) exportBackup(c *gin.Context) {
	s.mu.Lock()
	envelope := BackupEnvelope{
		Version:    1,
		ExportedAt: now(),
		State:      s.state,
	}
	content, err := json.MarshalIndent(envelope, "", "  ")
	s.mu.Unlock()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "备份生成失败"}})
		return
	}
	filename := "catieapi-backup-" + time.Now().UTC().Format("20060102-150405") + ".json"
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Data(http.StatusOK, "application/json; charset=utf-8", append(content, '\n'))
}

func (s *Server) restoreBackup(c *gin.Context) {
	content, err := io.ReadAll(io.LimitReader(c.Request.Body, 32*1024*1024+1))
	if err != nil {
		validationError(c, "备份文件读取失败")
		return
	}
	if len(content) > 32*1024*1024 {
		validationError(c, "备份文件不能超过 32 MB")
		return
	}
	var envelope BackupEnvelope
	if err := json.Unmarshal(content, &envelope); err != nil {
		validationError(c, "备份文件不是有效 JSON")
		return
	}
	if envelope.Version != 1 {
		validationError(c, "不支持的备份版本")
		return
	}
	if err := s.validateBackupState(envelope.State); err != nil {
		validationError(c, err.Error())
		return
	}

	s.mu.Lock()
	s.state = envelope.State
	s.normalizeStateCollections()
	s.pruneOperationalHistoryLocked()
	s.rateLimitBuckets = map[string]int{}
	s.idempotencyCache = map[string]CachedResponse{}
	s.sessions = map[string]Session{}
	s.saveStateLocked()
	s.mu.Unlock()
	s.applyPersistedDiscordSettings()
	c.JSON(http.StatusOK, gin.H{
		"restored": true,
		"users":    len(envelope.State.Users),
		"channels": len(envelope.State.Channels),
		"models":   len(envelope.State.Models),
	})
}

func (s *Server) validateBackupState(state AppState) error {
	if state.Users == nil || state.APIKeys == nil || state.Channels == nil || state.Models == nil || state.Logs == nil {
		return fmt.Errorf("备份缺少必要的数据集合")
	}
	for _, channel := range state.Channels {
		if strings.TrimSpace(channel.ID) == "" || strings.TrimSpace(channel.Name) == "" {
			return fmt.Errorf("备份包含无效渠道")
		}
		if strings.TrimSpace(channel.UpstreamAPIKey) != "" {
			if _, err := s.revealSecret(channel.UpstreamAPIKey); err != nil {
				return fmt.Errorf("渠道密钥无法解密，请使用导出备份时的 SECRET_KEY")
			}
		}
		for _, account := range channel.OpenAIAccounts {
			for _, secret := range []string{account.AccessToken, account.RefreshToken, account.SessionToken, account.IDToken} {
				if strings.TrimSpace(secret) == "" {
					continue
				}
				if _, err := s.revealSecret(secret); err != nil {
					return fmt.Errorf("OpenAI 账号凭证无法解密，请使用导出备份时的 SECRET_KEY")
				}
			}
		}
	}
	if secret := state.Settings.Discord.ClientSecret; strings.TrimSpace(secret) != "" {
		if _, err := s.revealSecret(secret); err != nil {
			return fmt.Errorf("Discord 密钥无法解密，请使用导出备份时的 SECRET_KEY")
		}
	}
	return nil
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
		Name               string   `json:"name"`
		AllowedModels      []string `json:"allowedModels"`
		ExpiresAt          string   `json:"expiresAt"`
		RateLimitPerMinute int      `json:"rateLimitPerMinute"`
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
	allowedModels, err := s.normalizeAllowedModelsLocked(body.AllowedModels)
	if err != nil {
		validationError(c, err.Error())
		return
	}
	expiresAt, err := normalizeAPIKeyExpiresAt(body.ExpiresAt)
	if err != nil {
		validationError(c, err.Error())
		return
	}
	if body.RateLimitPerMinute < 0 {
		validationError(c, "Key rate limit must be greater than or equal to 0")
		return
	}
	secret := "cat_" + randomHex(24)
	key := APIKey{
		ID:                 newID("key"),
		UserID:             user.ID,
		Name:               body.Name,
		Prefix:             secret[:12],
		Hash:               hashSecret(secret),
		Status:             "active",
		CreatedAt:          now(),
		AllowedModels:      allowedModels,
		ExpiresAt:          expiresAt,
		RateLimitPerMinute: body.RateLimitPerMinute,
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
	todayInputTokens := 0
	todayOutputTokens := 0
	todayCost := 0.0
	timezoneOffset, err := strconv.Atoi(c.DefaultQuery("timezoneOffset", "0"))
	if err != nil || timezoneOffset < -840 || timezoneOffset > 840 {
		timezoneOffset = 0
	}
	clientLocation := time.FixedZone("dashboard", -timezoneOffset*60)
	clientToday := time.Now().In(clientLocation).Format("2006-01-02")
	for _, user := range s.state.Users {
		if user.Status == "active" {
			activeUsers++
		}
		totalBalance += user.Balance
	}
	for _, log := range s.state.Logs {
		createdAt, err := time.Parse(time.RFC3339Nano, log.CreatedAt)
		if err != nil || createdAt.In(clientLocation).Format("2006-01-02") != clientToday {
			continue
		}
		requestsToday++
		todayInputTokens += log.InputTokens
		todayOutputTokens += log.OutputTokens
		todayCost += log.Cost
		if log.Status == "success" {
			successLogs++
		}
	}

	successRate := 0
	if requestsToday > 0 {
		successRate = int(math.Round(float64(successLogs) / float64(requestsToday) * 100))
	}

	c.JSON(http.StatusOK, gin.H{
		"activeUsers":       activeUsers,
		"channels":          len(s.state.Channels),
		"requestsToday":     requestsToday,
		"totalBalance":      round4(totalBalance),
		"successRate":       successRate,
		"todayInputTokens":  todayInputTokens,
		"todayOutputTokens": todayOutputTokens,
		"todayCost":         round4(todayCost),
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
		Name               string   `json:"name"`
		AllowedModels      []string `json:"allowedModels"`
		ExpiresAt          string   `json:"expiresAt"`
		RateLimitPerMinute int      `json:"rateLimitPerMinute"`
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
	allowedModels, err := s.normalizeAllowedModelsLocked(body.AllowedModels)
	if err != nil {
		validationError(c, err.Error())
		return
	}
	expiresAt, err := normalizeAPIKeyExpiresAt(body.ExpiresAt)
	if err != nil {
		validationError(c, err.Error())
		return
	}
	if body.RateLimitPerMinute < 0 {
		validationError(c, "Key rate limit must be greater than or equal to 0")
		return
	}

	secret := "cat_" + randomHex(24)
	prefix := secret[:18]
	key := APIKey{
		ID:                 newID("key"),
		UserID:             user.ID,
		Name:               body.Name,
		Prefix:             prefix,
		Hash:               hashSecret(secret),
		Status:             "active",
		CreatedAt:          now(),
		LastUsedAt:         "",
		RequestCount:       0,
		AllowedModels:      allowedModels,
		ExpiresAt:          expiresAt,
		RateLimitPerMinute: body.RateLimitPerMinute,
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
	if value, ok := patch["allowedModels"]; ok {
		models, ok := stringSliceFromPatch(value)
		if !ok {
			validationError(c, "allowedModels must be an array")
			return
		}
		allowedModels, err := s.normalizeAllowedModelsLocked(models)
		if err != nil {
			validationError(c, err.Error())
			return
		}
		key.AllowedModels = allowedModels
	}
	if value, ok := patch["expiresAt"].(string); ok {
		expiresAt, err := normalizeAPIKeyExpiresAt(value)
		if err != nil {
			validationError(c, err.Error())
			return
		}
		key.ExpiresAt = expiresAt
	}
	if value, ok := patch["rateLimitPerMinute"]; ok {
		limit, ok := intFromPatch(value)
		if !ok || limit < 0 {
			validationError(c, "rateLimitPerMinute must be greater than or equal to 0")
			return
		}
		key.RateLimitPerMinute = limit
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
		Name             string   `json:"name"`
		Provider         string   `json:"provider"`
		BaseURL          string   `json:"baseUrl"`
		UpstreamAPIKey   string   `json:"upstreamApiKey"`
		StreamMode       string   `json:"streamMode"`
		Priority         int      `json:"priority"`
		Weight           int      `json:"weight"`
		Models           []string `json:"models"`
		InputPricePer1K  float64  `json:"inputPricePer1K"`
		OutputPricePer1K float64  `json:"outputPricePer1K"`
	}
	_ = c.ShouldBindJSON(&body)
	body.Name = strings.TrimSpace(body.Name)
	body.Provider = strings.TrimSpace(body.Provider)
	body.BaseURL = strings.TrimSpace(body.BaseURL)
	body.StreamMode = normalizeStreamMode(body.StreamMode)
	if body.StreamMode == "" {
		validationError(c, "Invalid stream mode")
		return
	}
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
	if body.InputPricePer1K < 0 || body.OutputPricePer1K < 0 {
		validationError(c, "Channel price must be greater than or equal to 0")
		return
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
		ID:                newID("chn"),
		Name:              body.Name,
		Provider:          body.Provider,
		BaseURL:           strings.TrimRight(body.BaseURL, "/"),
		UpstreamAPIKey:    protectedKey,
		Status:            "disabled",
		StreamMode:        body.StreamMode,
		Priority:          body.Priority,
		Weight:            body.Weight,
		Models:            append([]string{}, body.Models...),
		InputPricePer1K:   round4(body.InputPricePer1K),
		OutputPricePer1K:  round4(body.OutputPricePer1K),
		PricingConfigured: body.InputPricePer1K > 0 || body.OutputPricePer1K > 0,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, modelID := range channel.Models {
		s.ensureChannelModelLocked(modelID, channel.Provider, "渠道", "Imported channel model")
	}
	s.state.Channels = append(s.state.Channels, channel)
	s.saveStateLocked()
	c.JSON(http.StatusCreated, gin.H{"channel": publicChannel(channel)})
}

func (s *Server) importOpenAIAccounts(c *gin.Context) {
	accounts, models, invalid, err := parseOpenAIAccountImportRequest(c)
	if err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_import", err.Error(), "invalid_request_error", nil)
		return
	}
	if len(accounts) == 0 {
		validationError(c, "No CPA or Sub2API accounts found in import file")
		return
	}

	importedAccounts := []PublicOpenAIAccount{}
	updated := 0
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := s.findChannel(c.Param("id"))
	if channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	s.ensureCodexChannelLocked(channel)
	for _, modelID := range models {
		s.ensureImportedModelLocked(modelID)
		if !containsString(channel.Models, modelID) {
			channel.Models = append(channel.Models, modelID)
		}
	}
	for _, account := range accounts {
		if strings.TrimSpace(account.AccessToken) == "" && strings.TrimSpace(account.SessionToken) != "" {
			account.AccessToken, account.ExpiresAt, _ = fetchChatGPTAccessTokenViaSessionCookie(account.SessionToken)
		}
		if strings.TrimSpace(account.AccessToken) == "" {
			invalid++
			continue
		}
		// Plain access-token exports often omit user metadata. Recover the
		// stable account identity from JWT claims so a later re-import updates
		// that account instead of creating a duplicate pool entry.
		if account.Email == "" || account.Name == "" {
			email, name := jwtEmailName(account.AccessToken)
			account.Email = firstNonEmptyString(account.Email, email)
			account.Name = firstNonEmptyString(account.Name, name)
		}
		if account.AccountID == "" || account.PlanType == "" {
			accountID, planType := openAIClaimsAccountInfo(account.AccessToken)
			account.AccountID = firstNonEmptyString(account.AccountID, accountID)
			account.PlanType = firstNonEmptyString(account.PlanType, planType)
		}
		protectedKey, err := s.protectSecret(strings.TrimSpace(account.AccessToken))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to protect imported access token"}})
			return
		}
		protectedRefresh := ""
		if strings.TrimSpace(account.RefreshToken) != "" {
			var err error
			protectedRefresh, err = s.protectSecret(strings.TrimSpace(account.RefreshToken))
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to protect imported refresh token"}})
				return
			}
		}
		protectedSession := ""
		if strings.TrimSpace(account.SessionToken) != "" {
			var err error
			protectedSession, err = s.protectSecret(strings.TrimSpace(account.SessionToken))
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to protect session token"}})
				return
			}
		}
		protectedID := ""
		if strings.TrimSpace(account.IDToken) != "" {
			var err error
			protectedID, err = s.protectSecret(strings.TrimSpace(account.IDToken))
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to protect imported id token"}})
				return
			}
		}
		stored := OpenAIAccount{
			ID:            newID("oaiacc"),
			Name:          account.Name,
			Email:         account.Email,
			AccessToken:   protectedKey,
			RefreshToken:  protectedRefresh,
			SessionToken:  protectedSession,
			IDToken:       protectedID,
			AccountID:     account.AccountID,
			UserID:        account.UserID,
			ExpiresAt:     account.ExpiresAt,
			LastRefresh:   account.LastRefresh,
			PlanType:      account.PlanType,
			Source:        account.Source,
			ClientProfile: codexClientProfileForImport(account.Source, account.ClientProfile),
			ImportedAt:    time.Now().UTC().Format(time.RFC3339),
			Status:        "unchecked",
		}
		if existingIndex := importedOpenAIAccountIndex(channel.OpenAIAccounts, account); existingIndex >= 0 {
			// A fresh export for the same account replaces credentials but keeps
			// the account's usage history and stable ID intact.
			stored.ID = channel.OpenAIAccounts[existingIndex].ID
			stored.LastUsedAt = channel.OpenAIAccounts[existingIndex].LastUsedAt
			stored.RequestCount = channel.OpenAIAccounts[existingIndex].RequestCount
			channel.OpenAIAccounts[existingIndex] = stored
			updated++
		} else {
			channel.OpenAIAccounts = append(channel.OpenAIAccounts, stored)
		}
		importedAccounts = append(importedAccounts, publicOpenAIAccount(stored))
	}
	if len(importedAccounts) == 0 {
		validationError(c, "Imported accounts do not contain access_token")
		return
	}
	s.saveStateLocked()
	c.JSON(http.StatusCreated, gin.H{
		"imported": len(importedAccounts),
		"created":  len(importedAccounts) - updated,
		"updated":  updated,
		"skipped":  invalid,
		"accounts": importedAccounts,
		"channel":  publicChannel(*channel),
	})
}

func importedOpenAIAccountIndex(accounts []OpenAIAccount, incoming ImportedOpenAIAccount) int {
	accountID := strings.TrimSpace(incoming.AccountID)
	userID := strings.TrimSpace(incoming.UserID)
	email := strings.ToLower(strings.TrimSpace(incoming.Email))
	for index, account := range accounts {
		if accountID != "" && strings.EqualFold(strings.TrimSpace(account.AccountID), accountID) {
			return index
		}
		if userID != "" && strings.EqualFold(strings.TrimSpace(account.UserID), userID) {
			return index
		}
		if email != "" && strings.EqualFold(strings.TrimSpace(account.Email), email) {
			return index
		}
	}
	return -1
}

// startOpenAIOAuth begins a ChatGPT OAuth (PKCE) authorization for a channel. It
// generates a PKCE verifier/challenge and state, stores the verifier server-side
// and returns the authorize URL for the admin to open in a browser. Accounts
// obtained this way include a refresh token for Codex-compatible accounts.
func (s *Server) startOpenAIOAuth(c *gin.Context) {
	s.mu.Lock()
	channel := s.findChannel(c.Param("id"))
	if channel == nil {
		s.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	s.mu.Unlock()

	verifier, challenge, err := newPKCEPair()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create PKCE challenge"}})
		return
	}
	state := randomHex(24)

	s.mu.Lock()
	s.pruneOpenAIOAuthFlowsLocked()
	s.openAIOAuthFlows[state] = openAIOAuthFlow{CodeVerifier: verifier, CreatedAt: time.Now().UTC()}
	s.mu.Unlock()

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", openAIAuthClientID)
	params.Set("redirect_uri", openAIOAuthRedirectURI)
	params.Set("scope", openAIOAuthScope)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("id_token_add_organizations", "true")
	params.Set("codex_cli_simplified_flow", "true")
	params.Set("state", state)
	authorizeURL := joinURL(s.openAIAuthBase, "oauth/authorize") + "?" + params.Encode()

	c.JSON(http.StatusOK, gin.H{
		"authorizeUrl": authorizeURL,
		"state":        state,
		"redirectUri":  openAIOAuthRedirectURI,
	})
}

// completeOpenAIOAuth finishes the OAuth flow. The admin pastes the full callback
// URL (or just the code+state); we verify the state, exchange the code with the
// stored PKCE verifier, and store the resulting account (with refresh token) in
// the channel's pool.
func (s *Server) completeOpenAIOAuth(c *gin.Context) {
	var body struct {
		CallbackURL string `json:"callbackUrl"`
		Code        string `json:"code"`
		State       string `json:"state"`
	}
	_ = c.ShouldBindJSON(&body)

	code := strings.TrimSpace(body.Code)
	state := strings.TrimSpace(body.State)
	// Accept a pasted callback URL and pull code/state out of it.
	if raw := strings.TrimSpace(body.CallbackURL); raw != "" {
		if parsed, err := url.Parse(raw); err == nil {
			query := parsed.Query()
			if v := strings.TrimSpace(query.Get("code")); v != "" {
				code = v
			}
			if v := strings.TrimSpace(query.Get("state")); v != "" {
				state = v
			}
		}
	}
	if code == "" {
		validationError(c, "缺少授权 code，请粘贴完整回调地址")
		return
	}

	s.mu.Lock()
	channel := s.findChannel(c.Param("id"))
	if channel == nil {
		s.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	flow, ok := s.openAIOAuthFlows[state]
	if ok {
		delete(s.openAIOAuthFlows, state)
	}
	s.mu.Unlock()
	if !ok {
		validationError(c, "授权状态已失效，请重新发起授权")
		return
	}

	tokens, err := s.exchangeOpenAIOAuthCode(code, flow.CodeVerifier)
	if err != nil {
		s.openAIError(c, http.StatusBadGateway, "oauth_exchange_failed", err.Error(), "api_error", nil)
		return
	}

	email, name := jwtEmailName(firstNonEmptyString(tokens.IDToken, tokens.AccessToken))
	accountID, planType := openAIClaimsAccountInfo(tokens.IDToken)

	s.mu.Lock()
	defer s.mu.Unlock()
	channel = s.findChannel(c.Param("id"))
	if channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	s.ensureCodexChannelLocked(channel)

	protectedAccess, err := s.protectSecret(tokens.AccessToken)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to protect access token"}})
		return
	}
	protectedRefresh := ""
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		if protectedRefresh, err = s.protectSecret(tokens.RefreshToken); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to protect refresh token"}})
			return
		}
	}
	protectedID := ""
	if strings.TrimSpace(tokens.IDToken) != "" {
		if protectedID, err = s.protectSecret(tokens.IDToken); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to protect id token"}})
			return
		}
	}
	stored := OpenAIAccount{
		ID:            newID("oaiacc"),
		Name:          firstNonEmptyString(name, email),
		Email:         email,
		AccessToken:   protectedAccess,
		RefreshToken:  protectedRefresh,
		IDToken:       protectedID,
		AccountID:     accountID,
		ExpiresAt:     tokens.ExpiresAt,
		LastRefresh:   now(),
		PlanType:      planType,
		Source:        "oauth",
		ClientProfile: codexClientProfileForImport("oauth", ""),
		ImportedAt:    time.Now().UTC().Format(time.RFC3339),
		Status:        "unchecked",
	}
	channel.OpenAIAccounts = append(channel.OpenAIAccounts, stored)
	s.saveStateLocked()

	c.JSON(http.StatusCreated, gin.H{
		"account": publicOpenAIAccount(stored),
		"channel": publicChannel(*channel),
	})
}

// exchangeOpenAIOAuthCode exchanges an authorization code + PKCE verifier for
// ChatGPT tokens.
func (s *Server) exchangeOpenAIOAuthCode(code, verifier string) (OpenAIRefreshResult, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", openAIAuthClientID)
	form.Set("code", code)
	form.Set("redirect_uri", openAIOAuthRedirectURI)
	form.Set("code_verifier", verifier)
	request, err := http.NewRequest(http.MethodPost, joinURL(s.openAIAuthBase, "oauth/token"), strings.NewReader(form.Encode()))
	if err != nil {
		return OpenAIRefreshResult{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return OpenAIRefreshResult{}, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return OpenAIRefreshResult{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		providerErr := providerErrorFromUpstream(response.StatusCode, content)
		return OpenAIRefreshResult{}, fmt.Errorf("%s", providerErr.Message)
	}
	var bodyResponse struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
		ExpiresAt    string `json:"expires_at"`
	}
	if err := json.Unmarshal(content, &bodyResponse); err != nil {
		return OpenAIRefreshResult{}, err
	}
	if strings.TrimSpace(bodyResponse.AccessToken) == "" {
		return OpenAIRefreshResult{}, fmt.Errorf("授权响应缺少 access_token")
	}
	result := OpenAIRefreshResult{
		AccessToken:  strings.TrimSpace(bodyResponse.AccessToken),
		RefreshToken: strings.TrimSpace(bodyResponse.RefreshToken),
		IDToken:      strings.TrimSpace(bodyResponse.IDToken),
		ExpiresAt:    strings.TrimSpace(bodyResponse.ExpiresAt),
	}
	if result.ExpiresAt == "" && bodyResponse.ExpiresIn > 0 {
		result.ExpiresAt = time.Now().Add(time.Duration(bodyResponse.ExpiresIn) * time.Second).UTC().Format(time.RFC3339Nano)
	}
	return result, nil
}

// pruneOpenAIOAuthFlowsLocked drops OAuth flows older than 15 minutes. Callers
// must hold s.mu.
func (s *Server) pruneOpenAIOAuthFlowsLocked() {
	cutoff := time.Now().Add(-15 * time.Minute)
	for state, flow := range s.openAIOAuthFlows {
		if flow.CreatedAt.Before(cutoff) {
			delete(s.openAIOAuthFlows, state)
		}
	}
}

// jwtEmailName best-effort extracts email and display name from a JWT payload
// without verifying the signature (used only for labeling the account).
func jwtEmailName(token string) (email string, name string) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	email, _ = claims["email"].(string)
	if n, ok := claims["name"].(string); ok {
		name = n
	} else if n, ok := claims["nickname"].(string); ok {
		name = n
	}
	return email, name
}

// newPKCEPair returns a PKCE code verifier and its S256 challenge.
func newPKCEPair() (verifier string, challenge string, err error) {
	buffer := make([]byte, 32)
	if _, err = rand.Read(buffer); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buffer)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// openAIClaimsAccountInfo best-effort extracts the ChatGPT account id and plan
// type from an id_token's auth claims.
func openAIClaimsAccountInfo(idToken string) (accountID string, planType string) {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) < 2 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	auth, _ := claims["https://api.openai.com/auth"].(map[string]interface{})
	if auth == nil {
		return "", ""
	}
	accountID = firstNonEmptyString(
		stringFromMapAnyKeys(auth, "chatgpt_account_id", "account_id"),
	)
	planType = stringFromMapAnyKeys(auth, "chatgpt_plan_type", "plan_type")
	return accountID, planType
}

func (s *Server) checkOpenAIAccounts(c *gin.Context) {
	var body struct {
		OnlyInvalid bool   `json:"onlyInvalid"`
		AccountID   string `json:"accountId"`
	}
	_ = c.ShouldBindJSON(&body)
	s.mu.Lock()
	channel := s.findChannel(c.Param("id"))
	if channel == nil {
		s.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	channelCopy := *channel
	channelCopy.OpenAIAccounts = append([]OpenAIAccount{}, channel.OpenAIAccounts...)
	s.mu.Unlock()

	checked := 0
	healthy := 0
	failed := 0
	results := []PublicOpenAIAccount{}
	for _, account := range channelCopy.OpenAIAccounts {
		if strings.TrimSpace(body.AccountID) != "" && account.ID != strings.TrimSpace(body.AccountID) {
			continue
		}
		if body.OnlyInvalid && account.Status != "invalid" {
			continue
		}
		checked++
		result := s.checkOpenAIAccount(account, channelCopy, true)
		status := result.Status
		errorMessage := result.Message
		if status == "healthy" {
			healthy++
		} else if status == "invalid" {
			failed++
		}
		nowValue := now()
		s.mu.Lock()
		liveChannel := s.findChannel(channelCopy.ID)
		var publicAccount PublicOpenAIAccount
		if liveChannel != nil {
			for index := range liveChannel.OpenAIAccounts {
				if liveChannel.OpenAIAccounts[index].ID != account.ID {
					continue
				}
				liveChannel.OpenAIAccounts[index].Status = status
				liveChannel.OpenAIAccounts[index].LastCheckedAt = nowValue
				liveChannel.OpenAIAccounts[index].LastError = truncateString(errorMessage, 500)
				liveChannel.OpenAIAccounts[index].LastErrorCode = result.ErrorCode
				if result.AccessToken != "" {
					liveChannel.OpenAIAccounts[index].AccessToken = result.AccessToken
				}
				if result.RefreshToken != "" {
					liveChannel.OpenAIAccounts[index].RefreshToken = result.RefreshToken
				}
				if result.IDToken != "" {
					liveChannel.OpenAIAccounts[index].IDToken = result.IDToken
				}
				if result.ExpiresAt != "" {
					liveChannel.OpenAIAccounts[index].ExpiresAt = result.ExpiresAt
				}
				if result.LastRefresh != "" {
					liveChannel.OpenAIAccounts[index].LastRefresh = result.LastRefresh
				}
				if result.PlanType != "" {
					liveChannel.OpenAIAccounts[index].PlanType = result.PlanType
				}
				if result.QuotaLimits != nil {
					liveChannel.OpenAIAccounts[index].QuotaLimits = result.QuotaLimits
				}
				publicAccount = publicOpenAIAccount(liveChannel.OpenAIAccounts[index])
				break
			}
		}
		s.mu.Unlock()
		if publicAccount.ID != "" {
			results = append(results, publicAccount)
		}
	}

	s.mu.Lock()
	channel = s.findChannel(channelCopy.ID)
	if channel == nil {
		s.mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	if healthy > 0 {
		s.ensureCodexChannelLocked(channel)
		channel.Status = "healthy"
		channel.LastCheckedAt = now()
		channel.LastError = ""
	}
	s.saveStateLocked()
	public := publicChannel(*channel)
	s.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"checked":  checked,
		"healthy":  healthy,
		"failed":   failed,
		"accounts": results,
		"channel":  public,
	})
}

func (s *Server) deduplicateOpenAIAccounts(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := s.findChannel(c.Param("id"))
	if channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	unique := make([]OpenAIAccount, 0, len(channel.OpenAIAccounts))
	seen := map[string]int{}
	removed := 0
	for _, account := range channel.OpenAIAccounts {
		key := openAIAccountIdentityKey(account)
		if key == "" {
			unique = append(unique, account)
			continue
		}
		if index, exists := seen[key]; exists {
			unique[index] = mergeOpenAIAccountDuplicates(unique[index], account)
			removed++
			continue
		}
		seen[key] = len(unique)
		unique = append(unique, account)
	}
	if removed > 0 {
		channel.OpenAIAccounts = unique
		s.saveStateLocked()
	}
	c.JSON(http.StatusOK, gin.H{"removed": removed, "channel": publicChannel(*channel)})
}

func openAIAccountIdentityKey(account OpenAIAccount) string {
	if value := strings.ToLower(strings.TrimSpace(account.AccountID)); value != "" {
		return "account:" + value
	}
	if value := strings.ToLower(strings.TrimSpace(account.UserID)); value != "" {
		return "user:" + value
	}
	if value := strings.ToLower(strings.TrimSpace(account.Email)); value != "" {
		return "email:" + value
	}
	return ""
}

func mergeOpenAIAccountDuplicates(primary OpenAIAccount, replacement OpenAIAccount) OpenAIAccount {
	// Keep the oldest stable ID so UI references remain valid, but prefer the
	// newest imported credential set and retain accumulated request history.
	if replacement.ImportedAt >= primary.ImportedAt {
		replacement.ID = primary.ID
		replacement.RequestCount += primary.RequestCount
		if replacement.LastUsedAt < primary.LastUsedAt {
			replacement.LastUsedAt = primary.LastUsedAt
		}
		return replacement
	}
	primary.RequestCount += replacement.RequestCount
	if primary.LastUsedAt < replacement.LastUsedAt {
		primary.LastUsedAt = replacement.LastUsedAt
	}
	return primary
}

func (s *Server) deleteOpenAIAccount(c *gin.Context) {
	channelID := c.Param("id")
	accountID := c.Param("accountId")
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := s.findChannel(channelID)
	if channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Channel not found"}})
		return
	}
	for index := range channel.OpenAIAccounts {
		if channel.OpenAIAccounts[index].ID != accountID {
			continue
		}
		channel.OpenAIAccounts = append(channel.OpenAIAccounts[:index], channel.OpenAIAccounts[index+1:]...)
		if len(channel.OpenAIAccounts) == 0 && isCodexChannel(*channel) {
			channel.Status = "disabled"
			channel.LastError = "No OpenAI accounts remain in this channel"
			channel.LastCheckedAt = now()
		}
		s.saveStateLocked()
		c.JSON(http.StatusOK, gin.H{
			"deleted": true,
			"channel": publicChannel(*channel),
		})
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "OpenAI account not found"}})
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
	if value, ok := patch["streamMode"].(string); ok {
		streamMode := normalizeStreamMode(value)
		if streamMode == "" {
			validationError(c, "Invalid stream mode")
			return
		}
		channel.StreamMode = streamMode
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
	if value, ok := patch["webEndpoint"].(bool); ok {
		channel.WebEndpoint = value
	}
	if value, ok := patch["models"].([]interface{}); ok {
		channel.Models = stringSlice(value)
	}
	for _, modelID := range channel.Models {
		s.ensureChannelModelLocked(modelID, channel.Provider, "渠道", "Imported channel model")
	}
	removedModels := s.pruneUnreferencedImportedModelsLocked()
	pricingChanged := false
	if value, ok := asFloat(patch["inputPricePer1K"]); ok {
		if value < 0 {
			validationError(c, "Input price must be greater than or equal to 0")
			return
		}
		channel.InputPricePer1K = round4(value)
		pricingChanged = true
	}
	if value, ok := asFloat(patch["outputPricePer1K"]); ok {
		if value < 0 {
			validationError(c, "Output price must be greater than or equal to 0")
			return
		}
		channel.OutputPricePer1K = round4(value)
		pricingChanged = true
	}
	if pricingChanged {
		channel.PricingConfigured = channel.InputPricePer1K > 0 || channel.OutputPricePer1K > 0
	}
	if channel.Status != "disabled" && strings.TrimSpace(channel.BaseURL) == "" {
		validationError(c, "Base URL is required before enabling a channel")
		return
	}
	s.saveStateLocked()

	c.JSON(http.StatusOK, gin.H{"channel": publicChannel(*channel), "removedModels": removedModels})
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
		removedModels := s.pruneUnreferencedImportedModelsLocked()
		s.saveStateLocked()
		c.JSON(http.StatusOK, gin.H{"deleted": true, "removedModels": removedModels})
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
	upstreamKey := ""
	if !isCodexChannel(channelCopy) {
		var err error
		upstreamKey, err = s.channelUpstreamKey(channelCopy)
		if err != nil {
			s.mu.Unlock()
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}
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
	if isCodexChannel(*channel) {
		s.ensureCodexChannelLocked(channel)
	}
	channel.Models = mergeStrings(nil, modelIDs)
	if isCodexChannel(*channel) {
		s.ensureCodexChannelLocked(channel)
	}
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
	removedModels := s.pruneUnreferencedImportedModelsLocked()
	s.saveStateLocked()
	c.JSON(http.StatusOK, gin.H{"channel": publicChannel(*channel), "models": channel.Models, "addedModels": added, "removedModels": removedModels})
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
	upstreamKey := ""
	if !isCodexChannel(channelCopy) {
		var err error
		upstreamKey, err = s.channelUpstreamKey(channelCopy)
		if err != nil {
			s.mu.Unlock()
			c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}
	}
	s.mu.Unlock()

	modelIDs, err := s.fetchUpstreamModelIDs(channelCopy, upstreamKey)
	if err == nil && isCodexChannel(channelCopy) && len(activeOpenAIAccounts(channelCopy.OpenAIAccounts)) == 0 {
		err = fmt.Errorf("请先导入可用 Codex OAuth 账号")
	}

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
	if isCodexChannel(*channel) {
		s.ensureCodexChannelLocked(channel)
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

func (s *Server) deleteModel(c *gin.Context) {
	modelID := strings.TrimSpace(c.Param("id"))
	if modelID == "" {
		validationError(c, "模型 ID 不能为空")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	index := -1
	for i, model := range s.state.Models {
		if model.ID == modelID {
			index = i
			break
		}
	}
	if index < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Model not found"}})
		return
	}

	s.state.Models = append(s.state.Models[:index], s.state.Models[index+1:]...)
	for i := range s.state.Channels {
		s.state.Channels[i].Models = removeString(s.state.Channels[i].Models, modelID)
	}
	for i := range s.state.APIKeys {
		s.state.APIKeys[i].AllowedModels = removeString(s.state.APIKeys[i].AllowedModels, modelID)
	}
	s.saveStateLocked()

	c.JSON(http.StatusOK, gin.H{"deleted": true, "modelId": modelID})
}

func (s *Server) openAIModels(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	auth, ok := s.openAIAPIKeyAuthLocked(c)
	if !ok {
		return
	}
	data := []gin.H{}
	for _, model := range s.state.Models {
		if model.Status == "available" && (auth == nil || apiKeyAllowsModel(auth.Key, model.ID)) {
			data = append(data, toOpenAIModel(model))
		}
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

func (s *Server) openAIModel(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	auth, ok := s.openAIAPIKeyAuthLocked(c)
	if !ok {
		return
	}
	model := s.resolveModelLocked(c.Param("id"))
	if model == nil || model.Status != "available" {
		writeOpenAIError(c, http.StatusNotFound, "model_not_found", "Model not found: "+c.Param("id"), "invalid_request_error", stringPtr("model"))
		return
	}
	if auth != nil && !apiKeyAllowsModel(auth.Key, model.ID) {
		writeOpenAIError(c, http.StatusNotFound, "model_not_found", "Model not found: "+c.Param("id"), "invalid_request_error", stringPtr("model"))
		return
	}
	c.JSON(http.StatusOK, toOpenAIModel(*model))
}

func (s *Server) listLogs(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := strings.TrimSpace(c.Query("status"))
	query := strings.ToLower(strings.TrimSpace(c.Query("q")))
	logs := make([]RequestLog, 0, len(s.state.Logs))
	for _, log := range s.state.Logs {
		if status != "" && status != "all" && log.Status != status {
			continue
		}
		searchable := strings.ToLower(strings.Join([]string{
			log.ID,
			stringValue(log.UserID),
			stringValue(log.APIKeyPrefix),
			stringValue(log.Model),
			stringValue(log.Channel),
			log.Status,
			log.ErrorCode,
		}, " "))
		if query != "" && !strings.Contains(searchable, query) {
			continue
		}
		logs = append(logs, log)
	}
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].CreatedAt > logs[j].CreatedAt
	})
	total := len(logs)
	page := queryInt(c, "page", 1, 1, 1_000_000)
	pageSize := queryInt(c, "pageSize", 25, 1, 100)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	c.JSON(http.StatusOK, gin.H{
		"logs":     logs[start:end],
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

func (s *Server) getLog(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		validationError(c, "请求 ID 不能为空")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, log := range s.state.Logs {
		if log.ID == id {
			c.JSON(http.StatusOK, gin.H{"log": log})
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Log not found"}})
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
	startedAt := time.Now()
	var body EmbeddingRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body: "+err.Error(), "invalid_request_error", nil)
		return
	}
	if body.Input == nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_input", "input is required", "invalid_request_error", stringPtr("input"))
		return
	}

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
	model := s.resolveModelLocked(body.Model)
	if model == nil || model.Status != "available" || !apiKeyAllowsModel(auth.Key, model.ID) {
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "model_not_available", "No available model: "+body.Model, "invalid_request_error", stringPtr("model"), auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		return
	}
	channels := s.channelCandidatesLocked(model.ID)
	if len(channels) == 0 {
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "model_not_available", "No available channel for model: "+model.ID, "invalid_request_error", stringPtr("model"), auth.User.ID, auth.Key.Prefix, model.ID, "")
		s.mu.Unlock()
		return
	}
	userID, keyID, keyPrefix, modelID := auth.User.ID, auth.Key.ID, auth.Key.Prefix, model.ID
	requestID := newID("req")
	s.mu.Unlock()

	var responseBody gin.H
	var providerErr *ProviderError
	var selected Channel
	attempts := 0
	for index, channel := range channels {
		selected = channel
		attempts++
		if !s.shouldUseCompatibleProvider(channel) || len(channel.OpenAIAccounts) > 0 {
			providerErr = &ProviderError{Status: http.StatusNotImplemented, Code: "embedding_not_supported", Message: "Selected channel does not expose an embeddings endpoint", Type: "invalid_request_error"}
		} else {
			responseBody, providerErr = s.callOpenAICompatibleEmbeddings(channel, requestID, modelID, body.Payload)
		}
		if providerErr == nil {
			s.updateChannelRuntimeHealth(channel.ID, true, "")
			break
		}
		if shouldMarkChannelUnhealthy(providerErr) {
			s.updateChannelRuntimeHealth(channel.ID, false, providerErr.Message)
		}
		if (providerErr.Code != "upstream_not_configured" && !retryableProviderError(providerErr)) || index == len(channels)-1 {
			break
		}
	}
	if providerErr != nil {
		s.recordFailedCall(userID, keyPrefix, modelID, selected.Name, requestID, providerErr.Code, attempts, startedAt)
		writeOpenAIError(c, providerErr.Status, providerErr.Code, providerErr.Message, providerErr.Type, stringPtr("model"))
		return
	}
	s.recordSuccessfulCall(userID, keyID, keyPrefix, modelID, selected.Name, requestID, 0, 0, 0, attempts, startedAt)
	c.JSON(http.StatusOK, responseBody)
}

func (s *Server) audioSpeech(c *gin.Context) {
	startedAt := time.Now()
	var body AudioSpeechRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body: "+err.Error(), "invalid_request_error", nil)
		return
	}
	if strings.TrimSpace(body.Input) == "" {
		s.openAIError(c, http.StatusBadRequest, "invalid_input", "input is required", "invalid_request_error", stringPtr("input"))
		return
	}
	s.mu.Lock()
	auth := s.findUserByAPIKeyLocked(apiTokenFromRequest(c))
	if auth == nil {
		s.openAIErrorForCallLocked(c, http.StatusUnauthorized, "invalid_api_key", "Invalid CatieAPI key", "invalid_request_error", nil, "", "", body.Model, "")
		s.mu.Unlock()
		return
	}
	if !s.checkRateLimitLocked(auth.Key) {
		s.openAIErrorForCallLocked(c, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded", "rate_limit_error", nil, auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		return
	}
	model := s.resolveModelLocked(body.Model)
	if model == nil || model.Status != "available" || !apiKeyAllowsModel(auth.Key, model.ID) {
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "model_not_available", "No available model: "+body.Model, "invalid_request_error", stringPtr("model"), auth.User.ID, auth.Key.Prefix, body.Model, "")
		s.mu.Unlock()
		return
	}
	channels := s.channelCandidatesLocked(model.ID)
	if len(channels) == 0 {
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "model_not_available", "No available channel for model: "+model.ID, "invalid_request_error", stringPtr("model"), auth.User.ID, auth.Key.Prefix, model.ID, "")
		s.mu.Unlock()
		return
	}
	userID, keyID, prefix, modelID, requestID := auth.User.ID, auth.Key.ID, auth.Key.Prefix, model.ID, newID("req")
	s.mu.Unlock()
	var content []byte
	var contentType string
	var providerErr *ProviderError
	var selected Channel
	attempts := 0
	for index, channel := range channels {
		selected = channel
		attempts++
		if !s.shouldUseCompatibleProvider(channel) || len(channel.OpenAIAccounts) > 0 {
			providerErr = &ProviderError{Status: http.StatusNotImplemented, Code: "audio_not_supported", Message: "Selected channel does not expose audio speech", Type: "invalid_request_error"}
		} else {
			content, contentType, providerErr = s.callOpenAICompatibleAudioSpeech(channel, requestID, modelID, body.Payload)
		}
		if providerErr == nil {
			s.updateChannelRuntimeHealth(channel.ID, true, "")
			break
		}
		if shouldMarkChannelUnhealthy(providerErr) {
			s.updateChannelRuntimeHealth(channel.ID, false, providerErr.Message)
		}
		if providerErr.Code != "upstream_not_configured" && !retryableProviderError(providerErr) || index == len(channels)-1 {
			break
		}
	}
	if providerErr != nil {
		s.recordFailedCall(userID, prefix, modelID, selected.Name, requestID, providerErr.Code, attempts, startedAt)
		writeOpenAIError(c, providerErr.Status, providerErr.Code, providerErr.Message, providerErr.Type, stringPtr("model"))
		return
	}
	s.recordSuccessfulCall(userID, keyID, prefix, modelID, selected.Name, requestID, 0, 0, 0, attempts, startedAt)
	c.Header("X-Request-ID", requestID)
	c.Data(http.StatusOK, contentType, content)
}

func (s *Server) moderations(c *gin.Context) {
	startedAt := time.Now()
	var payload map[string]interface{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Invalid JSON body: "+err.Error(), "invalid_request_error", nil)
		return
	}
	if payload["input"] == nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_input", "input is required", "invalid_request_error", stringPtr("input"))
		return
	}
	requestedModel, _ := payload["model"].(string)
	if strings.TrimSpace(requestedModel) == "" {
		requestedModel = "omni-moderation-latest"
	}
	s.mu.Lock()
	auth := s.findUserByAPIKeyLocked(apiTokenFromRequest(c))
	if auth == nil {
		s.openAIErrorForCallLocked(c, http.StatusUnauthorized, "invalid_api_key", "Invalid CatieAPI key", "invalid_request_error", nil, "", "", requestedModel, "")
		s.mu.Unlock()
		return
	}
	if !s.checkRateLimitLocked(auth.Key) {
		s.openAIErrorForCallLocked(c, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded", "rate_limit_error", nil, auth.User.ID, auth.Key.Prefix, requestedModel, "")
		s.mu.Unlock()
		return
	}
	channels := make([]Channel, 0)
	for _, channel := range s.state.Channels {
		if channel.Status != "disabled" && s.shouldUseCompatibleProvider(channel) && len(channel.OpenAIAccounts) == 0 {
			channels = append(channels, channel)
		}
	}
	if len(channels) == 0 {
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "moderation_not_available", "No compatible channel is available for moderations", "invalid_request_error", nil, auth.User.ID, auth.Key.Prefix, requestedModel, "")
		s.mu.Unlock()
		return
	}
	userID, keyID, prefix, requestID := auth.User.ID, auth.Key.ID, auth.Key.Prefix, newID("req")
	s.mu.Unlock()
	var result gin.H
	var providerErr *ProviderError
	var selected Channel
	attempts := 0
	for index, channel := range channels {
		selected = channel
		attempts++
		result, providerErr = s.callOpenAICompatibleJSON(channel, requestID, "moderations", payload)
		if providerErr == nil {
			s.updateChannelRuntimeHealth(channel.ID, true, "")
			break
		}
		if shouldMarkChannelUnhealthy(providerErr) {
			s.updateChannelRuntimeHealth(channel.ID, false, providerErr.Message)
		}
		if !retryableProviderError(providerErr) || index == len(channels)-1 {
			break
		}
	}
	if providerErr != nil {
		s.recordFailedCall(userID, prefix, requestedModel, selected.Name, requestID, providerErr.Code, attempts, startedAt)
		writeOpenAIError(c, providerErr.Status, providerErr.Code, providerErr.Message, providerErr.Type, nil)
		return
	}
	s.recordSuccessfulCall(userID, keyID, prefix, requestedModel, selected.Name, requestID, 0, 0, 0, attempts, startedAt)
	c.JSON(http.StatusOK, result)
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

func (s *Server) imageGenerations(c *gin.Context) {
	startedAt := time.Now()
	idempotencyKey := c.GetHeader("Idempotency-Key")

	s.mu.Lock()
	if idempotencyKey != "" {
		if cached, ok := s.idempotencyCache[idempotencyKey]; ok {
			s.mu.Unlock()
			c.JSON(cached.Status, cached.Body)
			return
		}
	}
	s.mu.Unlock()

	var body ImageRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		s.openAIError(c, http.StatusBadRequest, "invalid_json", "Request body must be valid JSON", "invalid_request_error", nil)
		return
	}
	s.handleImageGeneration(c, body, startedAt, idempotencyKey, "generate")
}

func (s *Server) imageEdits(c *gin.Context) {
	startedAt := time.Now()
	idempotencyKey := c.GetHeader("Idempotency-Key")

	s.mu.Lock()
	if idempotencyKey != "" {
		if cached, ok := s.idempotencyCache[idempotencyKey]; ok {
			s.mu.Unlock()
			c.JSON(cached.Status, cached.Body)
			return
		}
	}
	s.mu.Unlock()

	body, providerErr := imageEditRequestFromContext(c)
	if providerErr != nil {
		writeOpenAIError(c, providerErr.Status, providerErr.Code, providerErr.Message, providerErr.Type, nil)
		return
	}
	s.handleImageGeneration(c, body, startedAt, idempotencyKey, "edit")
}

func startImageJSONKeepalive(c *gin.Context) func() {
	// Set proxy/cache headers before the first body write. If these are added
	// only inside the ticker, reverse proxies may already have chosen buffering
	// or timeout behavior for the response.
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Connection", "keep-alive")
	done := make(chan struct{})
	stopped := make(chan struct{})
	requestDone := c.Request.Context().Done()
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(imageJSONKeepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-requestDone:
				return
			case <-ticker.C:
				if _, err := c.Writer.Write([]byte(" \n")); err != nil {
					return
				}
				c.Writer.Flush()
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

func imageEditRequestFromContext(c *gin.Context) (ImageRequest, *ProviderError) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.GetHeader("Content-Type"))), "multipart/form-data") {
		return imageEditMultipartRequestFromContext(c)
	}
	var body ImageRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		return ImageRequest{}, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_json", Message: "Request body must be valid JSON", Type: "invalid_request_error"}
	}
	if body.Payload == nil {
		body.Payload = map[string]interface{}{}
	}
	if len(imageURLsFromPayload(body.Payload)) == 0 {
		return ImageRequest{}, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_image", Message: "image input is required", Type: "invalid_request_error"}
	}
	return body, nil
}

func imageEditMultipartRequestFromContext(c *gin.Context) (ImageRequest, *ProviderError) {
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		return ImageRequest{}, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_multipart", Message: "Invalid multipart body: " + err.Error(), Type: "invalid_request_error"}
	}
	form := c.Request.MultipartForm
	if form == nil {
		return ImageRequest{}, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_multipart", Message: "Multipart body is required", Type: "invalid_request_error"}
	}

	payload := map[string]interface{}{}
	for key, values := range form.Value {
		if len(values) == 1 {
			payload[key] = values[0]
			continue
		}
		items := make([]interface{}, 0, len(values))
		for _, value := range values {
			items = append(items, value)
		}
		payload[key] = items
	}

	images := []interface{}{}
	for field, files := range form.File {
		if field != "image" && !strings.HasPrefix(field, "image[") {
			continue
		}
		for _, fileHeader := range files {
			dataURL, err := multipartImageFileToDataURL(fileHeader)
			if err != nil {
				return ImageRequest{}, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_image", Message: err.Error(), Type: "invalid_request_error"}
			}
			images = append(images, gin.H{"image_url": dataURL})
		}
	}
	if len(images) == 0 {
		return ImageRequest{}, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_image", Message: "image input is required", Type: "invalid_request_error"}
	}
	payload["images"] = images

	if files := form.File["mask"]; len(files) > 0 {
		dataURL, err := multipartImageFileToDataURL(files[0])
		if err != nil {
			return ImageRequest{}, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_image", Message: err.Error(), Type: "invalid_request_error"}
		}
		payload["mask"] = gin.H{"image_url": dataURL}
	}

	return ImageRequest{
		Model:   strings.TrimSpace(multipartFormFirstValue(form, "model")),
		Prompt:  strings.TrimSpace(multipartFormFirstValue(form, "prompt")),
		Payload: payload,
	}, nil
}

func multipartFormFirstValue(form *multipart.Form, key string) string {
	if form == nil {
		return ""
	}
	values := form.Value[key]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func multipartImageFileToDataURL(fileHeader *multipart.FileHeader) (string, error) {
	if fileHeader == nil {
		return "", fmt.Errorf("image file is required")
	}
	file, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("open image file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 20<<20))
	if err != nil {
		return "", fmt.Errorf("read image file: %w", err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("image file is empty")
	}
	contentType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
	if contentType == "" || strings.EqualFold(contentType, "application/octet-stream") {
		contentType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return "", fmt.Errorf("image file must be an image")
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func (s *Server) handleImageGeneration(c *gin.Context, body ImageRequest, startedAt time.Time, idempotencyKey string, operation string) {
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
	if strings.TrimSpace(body.Prompt) == "" {
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "invalid_prompt", "prompt is required", "invalid_request_error", stringPtr("prompt"), auth.User.ID, auth.Key.Prefix, body.Model, "")
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
	if !apiKeyAllowsModel(auth.Key, model.ID) {
		s.openAIErrorForCallLocked(c, http.StatusForbidden, "model_not_allowed", "API key is not allowed to use model: "+model.ID, "invalid_request_error", stringPtr("model"), auth.User.ID, auth.Key.Prefix, model.ID, "")
		s.mu.Unlock()
		return
	}
	channels := s.channelCandidatesLocked(model.ID)
	if len(channels) == 0 {
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "model_not_available", "No available channel for model: "+model.ID, "invalid_request_error", stringPtr("model"), auth.User.ID, auth.Key.Prefix, model.ID, "")
		s.mu.Unlock()
		return
	}
	billingModel := modelWithChannelPricing(*model, channels[0])
	if billingModel.PricingConfigured && auth.User.Balance <= 0 {
		s.openAIErrorForCallLocked(c, http.StatusPaymentRequired, "insufficient_quota", "Insufficient quota", "billing_error", nil, auth.User.ID, auth.Key.Prefix, model.ID, channels[0].Name)
		s.mu.Unlock()
		return
	}

	authUserID := auth.User.ID
	authKeyID := auth.Key.ID
	authKeyPrefix := auth.Key.Prefix
	modelCopy := *model
	requestID := newID("req")
	s.mu.Unlock()

	var responseBody gin.H
	var providerErr *ProviderError
	var selectedChannel Channel
	attempts := 0
	stopKeepalive := startImageJSONKeepalive(c)
	for index, channel := range channels {
		call := ImageGatewayCall{RequestID: requestID, Model: modelCopy, Channel: channel, Body: body, Operation: operation}
		attempts++
		responseBody, providerErr = s.callImageProvider(call)
		if providerErr == nil {
			selectedChannel = channel
			s.updateChannelRuntimeHealth(channel.ID, true, "")
			break
		}
		selectedChannel = channel
		if shouldMarkChannelUnhealthy(providerErr) {
			s.updateChannelRuntimeHealth(channel.ID, false, providerErr.Message)
		}
		if !retryableProviderError(providerErr) || index == len(channels)-1 {
			break
		}
	}
	stopKeepalive()
	if providerErr != nil {
		s.recordFailedCall(authUserID, authKeyPrefix, modelCopy.ID, selectedChannel.Name, requestID, providerErr.Code, attempts, startedAt)
		writeOpenAIError(c, providerErr.Status, providerErr.Code, providerErr.Message, providerErr.Type, stringPtr("model"))
		return
	}

	billingModel = modelWithChannelPricing(modelCopy, selectedChannel)
	cost := estimateImageGenerationCost(billingModel, body)
	s.mu.Lock()
	s.recordSuccessfulImageCallLocked(authUserID, authKeyID, authKeyPrefix, modelCopy.ID, selectedChannel.Name, requestID, cost, estimateImagePromptTokens(body.Prompt), attempts, startedAt)
	if idempotencyKey != "" {
		s.idempotencyCache[idempotencyKey] = CachedResponse{Status: http.StatusOK, Body: responseBody, CreatedAt: now()}
	}
	s.mu.Unlock()
	c.JSON(http.StatusOK, responseBody)
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
	if !apiKeyAllowsModel(auth.Key, model.ID) {
		s.openAIErrorForCallLocked(c, http.StatusForbidden, "model_not_allowed", "API key is not allowed to use model: "+model.ID, "invalid_request_error", stringPtr("model"), auth.User.ID, auth.Key.Prefix, model.ID, "")
		s.mu.Unlock()
		return
	}
	channels := s.channelCandidatesLocked(model.ID)
	if len(channels) == 0 {
		s.openAIErrorForCallLocked(c, http.StatusBadRequest, "model_not_available", "No available channel for model: "+model.ID, "invalid_request_error", stringPtr("model"), auth.User.ID, auth.Key.Prefix, model.ID, "")
		s.mu.Unlock()
		return
	}
	billingModel := modelWithChannelPricing(*model, channels[0])
	if billingModel.PricingConfigured && auth.User.Balance <= 0 {
		s.openAIErrorForCallLocked(c, http.StatusPaymentRequired, "insufficient_quota", "Insufficient quota", "billing_error", nil, auth.User.ID, auth.Key.Prefix, model.ID, channels[0].Name)
		s.mu.Unlock()
		return
	}

	authUserID := auth.User.ID
	authKeyID := auth.Key.ID
	authKeyPrefix := auth.Key.Prefix
	modelCopy := *model
	requestID := newID("req")
	s.mu.Unlock()

	if body.Stream {
		var selectedChannel Channel
		var providerErr *ProviderError
		var responseBody gin.H
		attempts := 0
		streamMode := ""
		for index, channel := range channels {
			selectedChannel = channel
			call := GatewayCall{RequestID: requestID, Model: modelCopy, Channel: channel, Body: body}
			streamMode = normalizeStreamMode(channel.StreamMode)
			if streamMode == "disabled" {
				providerErr = &ProviderError{Status: http.StatusBadRequest, Code: "stream_not_supported", Message: "Channel streaming is disabled", Type: "invalid_request_error"}
			} else if streamMode == "fake" {
				attempts++
				nonStreamCall := call
				nonStreamCall.Body.Stream = false
				if nonStreamCall.Body.Payload != nil {
					payload := gin.H{}
					for key, value := range nonStreamCall.Body.Payload {
						payload[key] = value
					}
					payload["stream"] = false
					nonStreamCall.Body.Payload = payload
				}
				responseBody, providerErr = s.callProvider(nonStreamCall)
				if providerErr == nil {
					s.writeFakeProviderStream(c, call, responseBody)
				}
			} else {
				attempts++
				providerErr = s.writeProviderStream(c, call)
				if shouldFallbackStreamToNonStream(providerErr, channel) && !s.shouldUseCompatibleProvider(channel) {
					attempts++
					nonStreamCall := call
					nonStreamCall.Body.Stream = false
					if nonStreamCall.Body.Payload != nil {
						payload := gin.H{}
						for key, value := range nonStreamCall.Body.Payload {
							payload[key] = value
						}
						payload["stream"] = false
						nonStreamCall.Body.Payload = payload
					}
					responseBody, providerErr = s.callProvider(nonStreamCall)
					if providerErr == nil {
						streamMode = "fake"
						s.writeFakeProviderStream(c, call, responseBody)
					}
				}
			}
			if providerErr == nil {
				selectedChannel = channel
				s.updateChannelRuntimeHealth(channel.ID, true, "")
				break
			}
			if shouldMarkChannelUnhealthy(providerErr) {
				s.updateChannelRuntimeHealth(channel.ID, false, providerErr.Message)
			}
			if !retryableProviderError(providerErr) || index == len(channels)-1 {
				break
			}
		}
		if providerErr != nil {
			s.recordFailedCall(authUserID, authKeyPrefix, modelCopy.ID, selectedChannel.Name, requestID, providerErr.Code, attempts, startedAt)
			writeOpenAIStreamError(c, providerErr, stringPtr("model"))
			return
		}
		billingModel = modelWithChannelPricing(modelCopy, selectedChannel)
		inputTokens, outputTokens := callTokenUsage(responseBody, body.Messages, streamMode != "fake")
		streamCost := calculateCallCost(billingModel, responseBody, body.Messages, streamMode != "fake")
		s.recordSuccessfulCall(authUserID, authKeyID, authKeyPrefix, modelCopy.ID, selectedChannel.Name, requestID, streamCost, inputTokens, outputTokens, attempts, startedAt)
		return
	}

	var responseBody gin.H
	var providerErr *ProviderError
	var selectedChannel Channel
	attempts := 0
	for index, channel := range channels {
		call := GatewayCall{RequestID: requestID, Model: modelCopy, Channel: channel, Body: body}
		attempts++
		responseBody, providerErr = s.callProvider(call)
		if providerErr == nil {
			selectedChannel = channel
			s.updateChannelRuntimeHealth(channel.ID, true, "")
			break
		}
		selectedChannel = channel
		if shouldMarkChannelUnhealthy(providerErr) {
			s.updateChannelRuntimeHealth(channel.ID, false, providerErr.Message)
		}
		if !retryableProviderError(providerErr) || index == len(channels)-1 {
			break
		}
	}
	if providerErr != nil {
		s.recordFailedCall(authUserID, authKeyPrefix, modelCopy.ID, selectedChannel.Name, requestID, providerErr.Code, attempts, startedAt)
		writeOpenAIError(c, providerErr.Status, providerErr.Code, providerErr.Message, providerErr.Type, stringPtr("model"))
		return
	}

	billingModel = modelWithChannelPricing(modelCopy, selectedChannel)
	inputTokens, outputTokens := callTokenUsage(responseBody, body.Messages, false)
	cost := calculateCallCost(billingModel, responseBody, body.Messages, false)
	outputBody := interface{}(responseBody)
	if transform != nil {
		outputBody = transform(responseBody, modelCopy)
	}
	s.mu.Lock()
	s.recordSuccessfulCallLocked(authUserID, authKeyID, authKeyPrefix, modelCopy.ID, selectedChannel.Name, requestID, cost, inputTokens, outputTokens, attempts, startedAt)
	if idempotencyKey != "" {
		s.idempotencyCache[idempotencyKey] = CachedResponse{Status: http.StatusOK, Body: outputBody, CreatedAt: now()}
	}
	s.mu.Unlock()
	c.JSON(http.StatusOK, outputBody)
}

func (s *Server) recordSuccessfulCall(userID string, keyID string, keyPrefix string, modelID string, channelName string, requestID string, cost float64, inputTokens int, outputTokens int, attempts int, startedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordSuccessfulCallLocked(userID, keyID, keyPrefix, modelID, channelName, requestID, cost, inputTokens, outputTokens, attempts, startedAt)
}

func (s *Server) recordSuccessfulCallLocked(userID string, keyID string, keyPrefix string, modelID string, channelName string, requestID string, cost float64, inputTokens int, outputTokens int, attempts int, startedAt time.Time) {
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

	account := s.requestAccounts[requestID]
	delete(s.requestAccounts, requestID)
	log := RequestLog{
		ID:           requestID,
		UserID:       &userID,
		APIKeyPrefix: &keyPrefix,
		Model:        &modelID,
		Channel:      &channelName,
		Status:       "success",
		Cost:         cost,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Attempts:     attempts,
		LatencyMS:    time.Since(startedAt).Milliseconds(),
		CreatedAt:    now(),
	}
	if account != "" {
		log.Account = &account
	}
	s.state.Logs = append(s.state.Logs, log)
	s.saveStateLocked()
}

func (s *Server) recordSuccessfulImageCallLocked(userID string, keyID string, keyPrefix string, modelID string, channelName string, requestID string, cost float64, promptTokens int, attempts int, startedAt time.Time) {
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
		Reason:    "images.generation",
		CreatedAt: now(),
	})

	account := s.requestAccounts[requestID]
	delete(s.requestAccounts, requestID)
	log := RequestLog{
		ID:           requestID,
		UserID:       &userID,
		APIKeyPrefix: &keyPrefix,
		Model:        &modelID,
		Channel:      &channelName,
		Status:       "success",
		Cost:         cost,
		InputTokens:  promptTokens,
		OutputTokens: 0,
		Attempts:     attempts,
		LatencyMS:    time.Since(startedAt).Milliseconds(),
		CreatedAt:    now(),
	}
	if account != "" {
		log.Account = &account
	}
	s.state.Logs = append(s.state.Logs, log)
	s.saveStateLocked()
}

func (s *Server) recordFailedCall(userID string, keyPrefix string, modelID string, channelName string, requestID string, errorCode string, attempts int, startedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.requestAccounts[requestID]
	delete(s.requestAccounts, requestID)
	log := RequestLog{
		ID:           requestID,
		UserID:       &userID,
		APIKeyPrefix: &keyPrefix,
		Model:        &modelID,
		Channel:      &channelName,
		Status:       "failed",
		Cost:         0,
		Attempts:     attempts,
		LatencyMS:    time.Since(startedAt).Milliseconds(),
		ErrorCode:    errorCode,
		CreatedAt:    now(),
	}
	if account != "" {
		log.Account = &account
	}
	s.state.Logs = append(s.state.Logs, log)
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

func (s *Server) callImageProvider(call ImageGatewayCall) (gin.H, *ProviderError) {
	if s.shouldUseCompatibleProvider(call.Channel) {
		return s.callOpenAICompatibleImage(call)
	}
	return gin.H{
		"created": unixNow(),
		"data": []gin.H{
			{
				"b64_json": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=",
			},
		},
	}, nil
}

func (s *Server) callOpenAICompatible(call GatewayCall) (gin.H, *ProviderError) {
	if len(activeOpenAIAccounts(call.Channel.OpenAIAccounts)) > 0 {
		return s.callChatGPTCodex(call)
	}

	endpoint := openAICompatibleChatEndpoint(call.Channel.BaseURL)
	request, providerErr := s.newOpenAICompatibleRequest(call, false, endpoint)
	if providerErr != nil {
		return nil, providerErr
	}

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_unreachable", Message: err.Error(), Type: "api_error"}
	}

	content, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
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

func (s *Server) callOpenAICompatibleEmbeddings(channel Channel, requestID string, modelID string, payload map[string]interface{}) (gin.H, *ProviderError) {
	upstreamKey, err := s.channelUpstreamKey(channel)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_key_unavailable", Message: err.Error(), Type: "api_error"}
	}
	if strings.TrimSpace(upstreamKey) == "" {
		upstreamKey = s.upstreamAPIKey
	}
	if strings.TrimSpace(upstreamKey) == "" {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_not_configured", Message: "Channel upstreamApiKey or UPSTREAM_API_KEY is required for embeddings", Type: "api_error"}
	}
	body := gin.H{}
	for key, value := range payload {
		if value != nil {
			body[key] = value
		}
	}
	body["model"] = modelID
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Failed to encode embedding request", Type: "invalid_request_error"}
	}
	request, err := http.NewRequest(http.MethodPost, openAICompatibleEmbeddingsEndpoint(channel.BaseURL), bytes.NewReader(encoded))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	request.Header.Set("Authorization", "Bearer "+upstreamKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", requestID)
	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_unreachable", Message: err.Error(), Type: "api_error"}
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Message: err.Error(), Type: "api_error"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, providerErrorFromUpstream(response.StatusCode, content)
	}
	var result gin.H
	if err := json.Unmarshal(content, &result); err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_invalid_json", Message: "Upstream returned invalid JSON", Type: "api_error"}
	}
	return result, nil
}

func (s *Server) callOpenAICompatibleAudioSpeech(channel Channel, requestID, modelID string, payload map[string]interface{}) ([]byte, string, *ProviderError) {
	key, err := s.channelUpstreamKey(channel)
	if err != nil {
		return nil, "", &ProviderError{Status: http.StatusBadGateway, Code: "upstream_key_unavailable", Message: err.Error(), Type: "api_error"}
	}
	if strings.TrimSpace(key) == "" {
		key = s.upstreamAPIKey
	}
	if strings.TrimSpace(key) == "" {
		return nil, "", &ProviderError{Status: http.StatusBadGateway, Code: "upstream_not_configured", Message: "Channel upstreamApiKey or UPSTREAM_API_KEY is required for audio speech", Type: "api_error"}
	}
	body := gin.H{}
	for k, v := range payload {
		if v != nil {
			body[k] = v
		}
	}
	body["model"] = modelID
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, "", &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Failed to encode audio request", Type: "invalid_request_error"}
	}
	req, err := http.NewRequest(http.MethodPost, openAICompatibleAudioSpeechEndpoint(channel.BaseURL), bytes.NewReader(encoded))
	if err != nil {
		return nil, "", &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", requestID)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", &ProviderError{Status: http.StatusBadGateway, Code: "upstream_unreachable", Message: err.Error(), Type: "api_error"}
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Message: err.Error(), Type: "api_error"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", providerErrorFromUpstream(resp.StatusCode, content)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "audio/mpeg"
	}
	return content, contentType, nil
}

func (s *Server) callOpenAICompatibleJSON(channel Channel, requestID, operation string, payload map[string]interface{}) (gin.H, *ProviderError) {
	key, err := s.channelUpstreamKey(channel)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_key_unavailable", Message: err.Error(), Type: "api_error"}
	}
	if strings.TrimSpace(key) == "" {
		key = s.upstreamAPIKey
	}
	if strings.TrimSpace(key) == "" {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_not_configured", Message: "Channel upstreamApiKey or UPSTREAM_API_KEY is required", Type: "api_error"}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Failed to encode request", Type: "invalid_request_error"}
	}
	req, err := http.NewRequest(http.MethodPost, openAICompatibleOperationEndpoint(channel.BaseURL, operation), bytes.NewReader(encoded))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", requestID)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_unreachable", Message: err.Error(), Type: "api_error"}
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Message: err.Error(), Type: "api_error"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, providerErrorFromUpstream(resp.StatusCode, content)
	}
	var result gin.H
	if err := json.Unmarshal(content, &result); err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_invalid_json", Message: "Upstream returned invalid JSON", Type: "api_error"}
	}
	return result, nil
}

func (s *Server) callOpenAICompatibleImage(call ImageGatewayCall) (gin.H, *ProviderError) {
	if isChatGPTWebImageChannel(call.Channel) {
		return s.callChatGPTWebImage(call)
	}
	if len(call.Channel.OpenAIAccounts) > 0 {
		var lastErr *ProviderError
		accounts := activeOpenAIAccounts(call.Channel.OpenAIAccounts)
		if len(accounts) == 0 {
			return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_not_configured", Message: "No active OpenAI accounts are available for image generation", Type: "api_error"}
		}
		attemptedAccounts := 0
		invalidatedAccounts := 0
		usageLimitedAccounts := 0
		for _, account := range accounts {
			attemptedAccounts++
			upstreamKey, err := s.resolveOpenAIAccountAccessToken(account)
			if err != nil {
				lastErr = &ProviderError{Status: http.StatusBadGateway, Code: "upstream_key_unavailable", Message: err.Error(), Type: "api_error"}
				continue
			}
			body, providerErr := s.callChatGPTCodexImageWithAccount(call, account, upstreamKey)
			if providerErr == nil {
				s.markOpenAIAccountUsed(call.Channel.ID, account.ID, call.RequestID)
				return body, nil
			}
			lastErr = providerErr
			if isUsageLimitProviderError(providerErr) {
				usageLimitedAccounts++
				s.markOpenAIAccountUsageLimited(call.Channel.ID, account.ID, providerErr.Message)
				continue
			}
			if shouldInvalidateOpenAIAccountForImage(providerErr) {
				invalidatedAccounts++
				s.markOpenAIAccountInvalid(call.Channel.ID, account.ID, providerErr.Message)
				continue
			}
			if retryableProviderError(providerErr) {
				continue
			}
			return nil, providerErr
		}
		if attemptedAccounts > 0 && invalidatedAccounts+usageLimitedAccounts == attemptedAccounts {
			if usageLimitedAccounts > 0 {
				return nil, openAIAccountsUsageLimitedError("image generation")
			}
			return nil, &ProviderError{
				Status:  http.StatusBadGateway,
				Code:    "upstream_accounts_unavailable",
				Message: "All active OpenAI accounts for image generation are invalid, expired, missing image scope, or billing-disabled. Re-import or sign in accounts again, then run batch check.",
				Type:    "api_error",
			}
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_not_configured", Message: "No active OpenAI accounts are available for image generation", Type: "api_error"}
	}

	request, providerErr := s.newOpenAICompatibleImageRequest(call)
	if providerErr != nil {
		return nil, providerErr
	}
	return s.doOpenAICompatibleImageRequest(call, request)
}

func (s *Server) callOpenAICompatibleImageWithKey(call ImageGatewayCall, upstreamKey string) (gin.H, *ProviderError) {
	request, providerErr := s.newOpenAICompatibleImageRequestWithKey(call, upstreamKey)
	if providerErr != nil {
		return nil, providerErr
	}
	return s.doOpenAICompatibleImageRequest(call, request)
}

func (s *Server) doOpenAICompatibleImageRequest(call ImageGatewayCall, request *http.Request) (gin.H, *ProviderError) {
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
	return body, nil
}

type chatGPTCodexParseResult struct {
	Text          string
	CompletedText string
	Images        []gin.H
	Usage         gin.H
	Created       int64
	Error         *ProviderError
	imageSeen     map[string]bool
}

func (s *Server) callChatGPTCodex(call GatewayCall) (gin.H, *ProviderError) {
	var lastErr *ProviderError
	accounts := activeOpenAIAccounts(call.Channel.OpenAIAccounts)
	if len(accounts) == 0 {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_not_configured", Message: "No active OpenAI accounts are available for chat completions", Type: "api_error"}
	}
	attemptedAccounts := 0
	invalidatedAccounts := 0
	usageLimitedAccounts := 0
	for _, account := range accounts {
		attemptedAccounts++
		accessToken, err := s.resolveOpenAIAccountAccessToken(account)
		if err != nil {
			lastErr = &ProviderError{Status: http.StatusBadGateway, Code: "upstream_key_unavailable", Message: err.Error(), Type: "api_error"}
			continue
		}
		body, providerErr := s.callChatGPTCodexWithAccount(call, account, accessToken)
		if providerErr == nil {
			s.markOpenAIAccountUsed(call.Channel.ID, account.ID, call.RequestID)
			return body, nil
		}
		lastErr = providerErr
		if isUsageLimitProviderError(providerErr) {
			usageLimitedAccounts++
			s.markOpenAIAccountUsageLimited(call.Channel.ID, account.ID, providerErr.Message)
			continue
		}
		if shouldInvalidateOpenAIAccountForChat(providerErr) {
			invalidatedAccounts++
			s.markOpenAIAccountInvalid(call.Channel.ID, account.ID, providerErr.Message)
			continue
		}
		return nil, providerErr
	}
	if attemptedAccounts > 0 && invalidatedAccounts+usageLimitedAccounts == attemptedAccounts {
		if usageLimitedAccounts > 0 {
			return nil, openAIAccountsUsageLimitedError("chat completions")
		}
		return nil, &ProviderError{
			Status:  http.StatusBadGateway,
			Code:    "upstream_accounts_unavailable",
			Message: "All active OpenAI accounts for chat completions are invalid or expired. Re-import or sign in accounts again, then run batch check.",
			Type:    "api_error",
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_not_configured", Message: "No active OpenAI accounts are available for chat completions", Type: "api_error"}
}

func (s *Server) callChatGPTCodexWithAccount(call GatewayCall, account OpenAIAccount, accessToken string) (gin.H, *ProviderError) {
	if call.Channel.WebEndpoint {
		return s.callChatGPTWebConversation(call, account, accessToken)
	}
	encoded, providerErr := buildChatGPTCodexChatPayload(call, true)
	if providerErr != nil {
		return nil, providerErr
	}
	request, providerErr := s.newChatGPTCodexRequest(encoded, accessToken, account)
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

	parsed := parseChatGPTCodexResponse(content)
	return chatCompletionFromCodexResult(call, parsed), nil
}

func (s *Server) streamChatGPTCodex(c *gin.Context, call GatewayCall) *ProviderError {
	var lastErr *ProviderError
	accounts := activeOpenAIAccounts(call.Channel.OpenAIAccounts)
	if len(accounts) == 0 {
		return &ProviderError{Status: http.StatusBadGateway, Code: "upstream_not_configured", Message: "No active OpenAI accounts are available for chat completions", Type: "api_error"}
	}
	attemptedAccounts := 0
	invalidatedAccounts := 0
	usageLimitedAccounts := 0
	for _, account := range accounts {
		attemptedAccounts++
		accessToken, err := s.resolveOpenAIAccountAccessToken(account)
		if err != nil {
			lastErr = &ProviderError{Status: http.StatusBadGateway, Code: "upstream_key_unavailable", Message: err.Error(), Type: "api_error"}
			continue
		}
		providerErr := s.streamChatGPTCodexWithAccount(c, call, account, accessToken)
		if providerErr == nil {
			s.markOpenAIAccountUsed(call.Channel.ID, account.ID, call.RequestID)
			return nil
		}
		lastErr = providerErr
		if isUsageLimitProviderError(providerErr) {
			usageLimitedAccounts++
			s.markOpenAIAccountUsageLimited(call.Channel.ID, account.ID, providerErr.Message)
			continue
		}
		if shouldInvalidateOpenAIAccountForChat(providerErr) {
			invalidatedAccounts++
			s.markOpenAIAccountInvalid(call.Channel.ID, account.ID, providerErr.Message)
			continue
		}
		return providerErr
	}
	if attemptedAccounts > 0 && invalidatedAccounts+usageLimitedAccounts == attemptedAccounts {
		if usageLimitedAccounts > 0 {
			return openAIAccountsUsageLimitedError("chat completions")
		}
		return &ProviderError{
			Status:  http.StatusBadGateway,
			Code:    "upstream_accounts_unavailable",
			Message: "All active OpenAI accounts for chat completions are invalid or expired. Re-import or sign in accounts again, then run batch check.",
			Type:    "api_error",
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return &ProviderError{Status: http.StatusBadGateway, Code: "upstream_not_configured", Message: "No active OpenAI accounts are available for chat completions", Type: "api_error"}
}

func (s *Server) streamChatGPTCodexWithAccount(c *gin.Context, call GatewayCall, account OpenAIAccount, accessToken string) *ProviderError {
	if call.Channel.WebEndpoint {
		return s.streamChatGPTWebConversation(c, call, account, accessToken)
	}
	encoded, providerErr := buildChatGPTCodexChatPayload(call, true)
	if providerErr != nil {
		return providerErr
	}
	request, providerErr := s.newChatGPTCodexRequest(encoded, accessToken, account)
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

	chunkID := newID("chatcmpl")
	created := unixNow()
	sentRole := false
	completed := false
	dataLines := []string{}
	flushEvent := func() {
		if len(dataLines) == 0 || completed {
			dataLines = nil
			return
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = nil
		if data == "" || data == "[DONE]" {
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return
		}
		if delta := codexOutputTextDelta(payload); delta != "" {
			deltaPayload := gin.H{"content": delta}
			if !sentRole {
				deltaPayload["role"] = "assistant"
				sentRole = true
			}
			writeSSEChunk(c, gin.H{
				"id":      chunkID,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   call.Model.ID,
				"choices": []gin.H{{"index": 0, "delta": deltaPayload, "finish_reason": nil}},
			})
		}
		if strings.EqualFold(completionPrompt(payload["type"]), "response.completed") {
			if !sentRole {
				finalText := strings.TrimSpace(codexCompletedText(payload))
				if finalText != "" {
					writeSSEChunk(c, gin.H{
						"id":      chunkID,
						"object":  "chat.completion.chunk",
						"created": created,
						"model":   call.Model.ID,
						"choices": []gin.H{{"index": 0, "delta": gin.H{"role": "assistant", "content": finalText}, "finish_reason": nil}},
					})
					sentRole = true
				}
			}
			writeSSEChunk(c, gin.H{
				"id":      chunkID,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   call.Model.ID,
				"choices": []gin.H{{"index": 0, "delta": gin.H{}, "finish_reason": "stop"}},
			})
			_, _ = c.Writer.WriteString("data: [DONE]\n\n")
			c.Writer.Flush()
			completed = true
		}
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10<<20)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			flushEvent()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flushEvent()
	if err := scanner.Err(); err != nil {
		return &ProviderError{Status: http.StatusBadGateway, Code: "upstream_read_error", Message: err.Error(), Type: "api_error"}
	}
	if !completed {
		writeSSEChunk(c, gin.H{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   call.Model.ID,
			"choices": []gin.H{{"index": 0, "delta": gin.H{}, "finish_reason": "stop"}},
		})
		_, _ = c.Writer.WriteString("data: [DONE]\n\n")
		c.Writer.Flush()
	}
	return nil
}

func (s *Server) callChatGPTCodexImageWithAccount(call ImageGatewayCall, account OpenAIAccount, accessToken string) (gin.H, *ProviderError) {
	var lastErr *ProviderError
	for _, mainModel := range chatGPTCodexImageMainModelsForAccount(account) {
		body, providerErr := s.callChatGPTCodexImageResponsesWithAccount(call, account, accessToken, mainModel)
		if providerErr == nil {
			return body, nil
		}
		lastErr = providerErr
		shouldRetryModel := shouldRetryChatGPTCodexImageMainModel(providerErr)
		if isCodexProxyImportSource(account.Source) &&
			(providerErr.Status == http.StatusUnauthorized || providerErr.Status == http.StatusForbidden) {
			shouldRetryModel = true
		}
		if !shouldRetryModel {
			return nil, providerErr
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_not_configured", Message: "No Codex image model is configured", Type: "api_error"}
}

func (s *Server) callChatGPTCodexImageResponsesWithAccount(call ImageGatewayCall, account OpenAIAccount, accessToken string, mainModel string) (gin.H, *ProviderError) {
	encoded, providerErr := buildChatGPTCodexImagePayload(call, mainModel)
	if providerErr != nil {
		return nil, providerErr
	}
	request, providerErr := s.newChatGPTCodexRequest(encoded, accessToken, account)
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

	parsed := parseChatGPTCodexResponse(content)
	if parsed.Error != nil && len(parsed.Images) == 0 {
		return nil, parsed.Error
	}
	if len(parsed.Images) == 0 {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_invalid_json", Message: "Upstream returned no image data", Type: "api_error"}
	}
	created := parsed.Created
	if created <= 0 {
		created = unixNow()
	}
	return gin.H{"created": created, "data": parsed.Images}, nil
}

func chatGPTCodexImageMainModels() []string {
	return []string{chatGPTCodexImageModel, "gpt-5.5", "gpt-5.4"}
}

func chatGPTCodexImageMainModelsForAccount(account OpenAIAccount) []string {
	if isCodexProxyImportSource(account.Source) {
		// ChatGPT auth-session accounts use the Responses image tool. The
		// subscription transport accepts the full Codex models here; the image
		// model remains gpt-image-2 in the tool payload.
		return []string{"gpt-5.4", "gpt-5.5", chatGPTCodexImageModel}
	}
	return chatGPTCodexImageMainModels()
}

func shouldRetryChatGPTCodexImageMainModel(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	if providerErr.Status == http.StatusGatewayTimeout || providerErr.Code == "upstream_timeout" {
		return true
	}
	return allowedString(providerErr.Code,
		"upstream_unreachable",
		"upstream_read_error",
		"upstream_invalid_json",
	)
}

func (s *Server) callChatGPTCodexDirectImageWithAccount(call ImageGatewayCall, account OpenAIAccount, accessToken string, model string) (gin.H, *ProviderError) {
	encoded, providerErr := buildChatGPTCodexDirectImagePayload(call, model)
	if providerErr != nil {
		return nil, providerErr
	}
	request, providerErr := s.newChatGPTCodexDirectImageRequest(encoded, accessToken, account)
	if providerErr != nil {
		return nil, providerErr
	}
	ctx, cancel := context.WithTimeout(request.Context(), codexDirectImageTimeout)
	defer cancel()
	request = request.WithContext(ctx)
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
	if !chatGPTCodexDirectImageHasData(body) {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_no_image_output", Message: "Upstream returned no image data", Type: "api_error"}
	}
	return body, nil
}

func shouldFallbackChatGPTCodexDirectImage(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	if isUsageLimitProviderError(providerErr) {
		return true
	}
	if allowedString(providerErr.Code,
		"upstream_no_image_output",
		"upstream_invalid_json",
		"upstream_unreachable",
		"upstream_read_error",
		"upstream_timeout",
	) {
		return true
	}
	return providerErr.Status == http.StatusGatewayTimeout || (providerErr.Status >= http.StatusInternalServerError && strings.HasPrefix(providerErr.Code, "upstream_"))
}

func chatGPTCodexDirectImageHasData(body gin.H) bool {
	if directImageMapHasData(body) {
		return true
	}
	switch data := body["data"].(type) {
	case []interface{}:
		for _, item := range data {
			if directImageItemHasData(item) {
				return true
			}
		}
	case []gin.H:
		for _, item := range data {
			if directImageMapHasData(item) {
				return true
			}
		}
	case []map[string]interface{}:
		for _, item := range data {
			if directImageMapHasData(item) {
				return true
			}
		}
	}
	return false
}

func directImageItemHasData(item interface{}) bool {
	switch typed := item.(type) {
	case map[string]interface{}:
		return directImageMapHasData(typed)
	case gin.H:
		return directImageMapHasData(typed)
	default:
		return false
	}
}

func directImageMapHasData(item map[string]interface{}) bool {
	return strings.TrimSpace(completionPrompt(item["b64_json"])) != "" || strings.TrimSpace(completionPrompt(item["url"])) != ""
}

func (s *Server) newChatGPTCodexRequest(encoded []byte, accessToken string, account OpenAIAccount) (*http.Request, *ProviderError) {
	request, err := http.NewRequest(http.MethodPost, s.chatGPTCodexResponsesURL(), bytes.NewReader(encoded))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Connection", "Keep-Alive")
	setChatGPTCodexClientHeaders(request, account)
	if accountID := strings.TrimSpace(account.AccountID); accountID != "" {
		setHeaderPreserveCase(request.Header, "Chatgpt-Account-Id", accountID)
	}
	return request, nil
}

func (s *Server) newChatGPTCodexDirectImageRequest(encoded []byte, accessToken string, account OpenAIAccount) (*http.Request, *ProviderError) {
	request, err := http.NewRequest(http.MethodPost, s.chatGPTCodexImagesGenerationsURL(), bytes.NewReader(encoded))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Connection", "Keep-Alive")
	setChatGPTCodexClientHeaders(request, account)
	if accountID := strings.TrimSpace(account.AccountID); accountID != "" {
		setHeaderPreserveCase(request.Header, "Chatgpt-Account-Id", accountID)
	}
	return request, nil
}

func (s *Server) chatGPTCodexResponsesURL() string {
	return joinURL(s.chatGPTAPIBase, "codex/responses")
}

func (s *Server) chatGPTCodexImagesGenerationsURL() string {
	return joinURL(s.chatGPTAPIBase, "codex/images/generations")
}

type chatGPTCodexClientProfile struct {
	Name       string
	Originator string
	UserAgent  string
	Version    string
}

func setChatGPTCodexClientHeaders(request *http.Request, account OpenAIAccount) {
	profile := chatGPTCodexClientProfileForAccount(account)
	setHeaderPreserveCase(request.Header, "Originator", profile.Originator)
	setHeaderPreserveCase(request.Header, "User-Agent", profile.UserAgent)
	setHeaderPreserveCase(request.Header, "Version", profile.Version)
	if profile.Name == chatGPTCodexTUIProfile || strings.Contains(profile.UserAgent, "Mac OS") {
		setHeaderPreserveCase(request.Header, "Session_id", randomUUID())
	}
}

func setHeaderPreserveCase(headers http.Header, key string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	headers.Del(key)
	headers[key] = []string{value}
}

func chatGPTCodexClientProfileForAccount(account OpenAIAccount) chatGPTCodexClientProfile {
	name := codexClientProfileForImport(account.Source, account.ClientProfile)
	switch name {
	case chatGPTCodexTUIProfile:
		return chatGPTCodexClientProfile{
			Name:       chatGPTCodexTUIProfile,
			Originator: chatGPTCodexTUIProfile,
			UserAgent:  chatGPTCodexTUIUserAgent,
			Version:    chatGPTCodexTUIVersion,
		}
	case chatGPTCodexVSCodeProfile:
		return chatGPTCodexClientProfile{
			Name:       chatGPTCodexVSCodeProfile,
			Originator: chatGPTCodexVSCodeProfile,
			UserAgent:  chatGPTCodexVSCodeUserAgent,
			Version:    chatGPTCodexVersion,
		}
	default:
		return chatGPTCodexClientProfile{
			Name:       chatGPTCodexTUIProfile,
			Originator: chatGPTCodexTUIProfile,
			UserAgent:  chatGPTCodexTUIUserAgent,
			Version:    chatGPTCodexTUIVersion,
		}
	}
}

func codexClientProfileForImport(source string, explicit string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if profile := normalizeCodexClientProfile(explicit); profile != "" {
		if isCodexProxyImportSource(source) && profile == chatGPTCodexVSCodeProfile {
			return chatGPTCodexTUIProfile
		}
		return profile
	}
	if isCodexProxyImportSource(source) {
		return chatGPTCodexTUIProfile
	}
	return chatGPTCodexTUIProfile
}

func isCodexProxyImportSource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "cpa", "sub2api", "sub2", "sub":
		return true
	default:
		return false
	}
}

func normalizeCodexClientProfile(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tui", "codex-tui", "codex_tui", "codex tui":
		return chatGPTCodexTUIProfile
	case "vscode", "codex_vscode", "vs-code", "vs_code":
		return chatGPTCodexVSCodeProfile
	case "cli", "codex_cli", "codex_cli_rs", "codex-cli-rs":
		return chatGPTCodexCLIProfile
	default:
		return ""
	}
}

func buildChatGPTCodexChatPayload(call GatewayCall, stream bool) ([]byte, *ProviderError) {
	instructions, input := chatMessagesToCodexInput(call.Body.Messages)
	payload := gin.H{
		"model":               call.Model.ID,
		"stream":              stream,
		"store":               false,
		"parallel_tool_calls": true,
		"input":               input,
	}
	if strings.TrimSpace(instructions) != "" {
		payload["instructions"] = instructions
	}
	for key, value := range call.Body.Payload {
		switch key {
		case "model", "messages", "stream":
			continue
		case "temperature", "top_p", "reasoning", "tools", "tool_choice", "metadata", "service_tier", "include", "previous_response_id":
			payload[key] = value
		case "max_output_tokens":
			payload[key] = value
		case "max_tokens":
			payload["max_output_tokens"] = value
		case "store":
			payload[key] = value
		case "parallel_tool_calls":
			payload[key] = value
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Failed to encode upstream request", Type: "invalid_request_error"}
	}
	return encoded, nil
}

func buildChatGPTCodexImagePayload(call ImageGatewayCall, mainModel string) ([]byte, *ProviderError) {
	mainModel = strings.TrimSpace(mainModel)
	if mainModel == "" {
		mainModel = chatGPTCodexImageModel
	}
	operation := normalizeImageOperation(call.Operation)
	tool := gin.H{
		"type":          "image_generation",
		"action":        operation,
		"model":         call.Model.ID,
		"output_format": "png",
	}
	for _, key := range []string{"size", "quality", "background", "output_format", "moderation", "style", "input_fidelity"} {
		if value, ok := call.Body.Payload[key]; ok && strings.TrimSpace(completionPrompt(value)) != "" {
			tool[key] = value
		}
	}
	for _, key := range []string{"output_compression", "partial_images"} {
		if value, ok := call.Body.Payload[key]; ok {
			tool[key] = value
		}
	}
	content := []gin.H{{"type": "input_text", "text": call.Body.Prompt}}
	if operation == "edit" {
		images := imageURLsFromPayload(call.Body.Payload)
		if len(images) == 0 {
			return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_image", Message: "image input is required", Type: "invalid_request_error"}
		}
		for _, imageURL := range images {
			content = append(content, gin.H{"type": "input_image", "image_url": imageURL})
		}
		if mask := imageURLFromValue(call.Body.Payload["mask"]); mask != "" {
			tool["input_image_mask"] = gin.H{"image_url": mask}
		}
	}
	payload := gin.H{
		"instructions":        chatGPTCodexImageInstructions,
		"model":               mainModel,
		"stream":              true,
		"store":               false,
		"parallel_tool_calls": true,
		"reasoning":           gin.H{"effort": "medium", "summary": "auto"},
		"include":             []string{"reasoning.encrypted_content"},
		"tool_choice":         gin.H{"type": "image_generation"},
		"tools":               []gin.H{tool},
		"input":               []gin.H{{"type": "message", "role": "user", "content": content}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Failed to encode upstream request", Type: "invalid_request_error"}
	}
	return encoded, nil
}

func normalizeImageOperation(operation string) string {
	if strings.EqualFold(strings.TrimSpace(operation), "edit") {
		return "edit"
	}
	return "generate"
}

func imageURLsFromPayload(payload map[string]interface{}) []string {
	if payload == nil {
		return nil
	}
	urls := []string{}
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		urls = append(urls, value)
	}
	collectImageURLs(payload["images"], add)
	collectImageURLs(payload["image"], add)
	return urls
}

func collectImageURLs(value interface{}, add func(string)) {
	switch typed := value.(type) {
	case nil:
		return
	case string:
		add(typed)
	case []interface{}:
		for _, item := range typed {
			collectImageURLs(item, add)
		}
	case []string:
		for _, item := range typed {
			add(item)
		}
	case []gin.H:
		for _, item := range typed {
			collectImageURLs(item, add)
		}
	case []map[string]interface{}:
		for _, item := range typed {
			collectImageURLs(item, add)
		}
	case gin.H:
		add(imageURLFromGinH(typed))
	case map[string]interface{}:
		add(imageURLFromMap(typed))
	case map[string]string:
		add(firstNonEmptyString(typed["image_url"], typed["url"], typed["b64_json"]))
	}
}

func imageURLFromValue(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case gin.H:
		return imageURLFromGinH(typed)
	case map[string]interface{}:
		return imageURLFromMap(typed)
	case map[string]string:
		return strings.TrimSpace(firstNonEmptyString(typed["image_url"], typed["url"], typed["b64_json"]))
	default:
		return ""
	}
}

func imageURLFromGinH(value gin.H) string {
	raw := map[string]interface{}{}
	for key, item := range value {
		raw[key] = item
	}
	return imageURLFromMap(raw)
}

func imageURLFromMap(value map[string]interface{}) string {
	for _, key := range []string{"image_url", "url", "b64_json"} {
		raw, ok := value[key]
		if !ok {
			continue
		}
		if text := imageURLFromValue(raw); text != "" {
			if key == "b64_json" && !strings.HasPrefix(strings.ToLower(text), "data:") {
				return "data:image/png;base64," + text
			}
			return text
		}
		if nested, ok := raw.(map[string]interface{}); ok {
			if text := imageURLFromMap(nested); text != "" {
				return text
			}
		}
	}
	return ""
}

func buildChatGPTCodexDirectImagePayload(call ImageGatewayCall, model string) ([]byte, *ProviderError) {
	payload := gin.H{}
	for key, value := range call.Body.Payload {
		if strings.EqualFold(strings.TrimSpace(key), "stream") {
			continue
		}
		payload[key] = value
	}
	payload["model"] = model
	if prompt := strings.TrimSpace(call.Body.Prompt); prompt != "" {
		payload["prompt"] = prompt
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Failed to encode upstream request", Type: "invalid_request_error"}
	}
	return encoded, nil
}

func chatGPTCodexDirectImageModel(call ImageGatewayCall) string {
	for _, model := range []string{call.Body.Model, call.Model.ID} {
		base := chatGPTCodexImageBaseModel(model)
		switch base {
		case "gpt-image-2", "gpt-image-1.5":
			return base
		}
	}
	return ""
}

func chatGPTCodexImageBaseModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if index := strings.LastIndex(model, "/"); index >= 0 && index < len(model)-1 {
		model = strings.TrimSpace(model[index+1:])
	}
	return model
}

func chatMessagesToCodexInput(messages []ChatMessage) (string, []gin.H) {
	instructions := []string{}
	input := []gin.H{}
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "" {
			role = "user"
		}
		if role == "system" || role == "developer" {
			if text := strings.TrimSpace(messageContentText(message.Content)); text != "" {
				instructions = append(instructions, text)
			}
			continue
		}
		if role != "assistant" && role != "user" {
			role = "user"
		}
		input = append(input, gin.H{
			"role":    role,
			"content": codexContentBlocks(message.Content, role),
		})
	}
	if len(input) == 0 {
		input = append(input, gin.H{"role": "user", "content": []gin.H{{"type": "input_text", "text": ""}}})
	}
	return strings.Join(instructions, "\n\n"), input
}

func codexContentBlocks(content interface{}, role string) []gin.H {
	textType := "input_text"
	if role == "assistant" {
		textType = "output_text"
	}
	switch typed := content.(type) {
	case []interface{}:
		blocks := []gin.H{}
		for _, item := range typed {
			part, ok := item.(map[string]interface{})
			if !ok {
				if text := strings.TrimSpace(completionPrompt(item)); text != "" {
					blocks = append(blocks, gin.H{"type": textType, "text": text})
				}
				continue
			}
			partType := strings.ToLower(strings.TrimSpace(completionPrompt(part["type"])))
			switch partType {
			case "text", "input_text", "output_text":
				if text := strings.TrimSpace(completionPrompt(part["text"])); text != "" {
					blocks = append(blocks, gin.H{"type": textType, "text": text})
				}
			case "image_url":
				imageURL := completionPrompt(part["image_url"])
				if nested, ok := part["image_url"].(map[string]interface{}); ok {
					imageURL = completionPrompt(nested["url"])
				}
				if strings.TrimSpace(imageURL) != "" {
					blocks = append(blocks, gin.H{"type": "input_image", "image_url": imageURL})
				}
			case "input_image":
				imageURL := completionPrompt(part["image_url"])
				if strings.TrimSpace(imageURL) != "" {
					blocks = append(blocks, gin.H{"type": "input_image", "image_url": imageURL})
				}
			default:
				if text := strings.TrimSpace(completionPrompt(part)); text != "" {
					blocks = append(blocks, gin.H{"type": textType, "text": text})
				}
			}
		}
		if len(blocks) > 0 {
			return blocks
		}
	}
	if text := messageContentText(content); strings.TrimSpace(text) != "" {
		return []gin.H{{"type": textType, "text": text}}
	}
	return []gin.H{{"type": textType, "text": ""}}
}

func messageContentText(content interface{}) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []interface{}:
		parts := []string{}
		for _, item := range typed {
			if part, ok := item.(map[string]interface{}); ok {
				if text := strings.TrimSpace(completionPrompt(part["text"])); text != "" {
					parts = append(parts, text)
					continue
				}
			}
			if text := strings.TrimSpace(completionPrompt(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return completionPrompt(content)
	}
}

func parseChatGPTCodexResponse(content []byte) chatGPTCodexParseResult {
	result := chatGPTCodexParseResult{Created: unixNow(), imageSeen: map[string]bool{}}
	dataLines := []string{}
	foundSSE := false
	flushEvent := func() {
		if len(dataLines) == 0 {
			return
		}
		data := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = nil
		if data == "" || data == "[DONE]" {
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(data), &payload); err == nil {
			collectCodexPayload(payload, &result)
		}
	}
	for _, rawLine := range strings.Split(string(content), "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if strings.TrimSpace(line) == "" {
			flushEvent()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			foundSSE = true
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	flushEvent()
	if foundSSE {
		return result
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(content, &payload); err == nil {
		collectCodexPayload(payload, &result)
	}
	return result
}

func collectCodexPayload(payload map[string]interface{}, result *chatGPTCodexParseResult) {
	if result == nil || payload == nil {
		return
	}
	if providerErr := codexProviderErrorFromPayload(payload); providerErr != nil {
		result.Error = providerErr
	}
	if delta := codexOutputTextDelta(payload); delta != "" {
		result.Text += delta
	}
	if text := codexCompletedText(payload); text != "" {
		result.CompletedText = text
	}
	if usage, ok := codexUsage(payload["usage"]); ok {
		result.Usage = usage
	}
	if created, ok := asInt(payload["created_at"]); ok && created > 0 {
		result.Created = int64(created)
	}
	collectCodexImagesFromMap(payload, result)
	if response, ok := payload["response"].(map[string]interface{}); ok {
		if usage, ok := codexUsage(response["usage"]); ok {
			result.Usage = usage
		}
		if created, ok := asInt(response["created_at"]); ok && created > 0 {
			result.Created = int64(created)
		}
		if text := codexResponseText(response); text != "" {
			result.CompletedText = text
		}
		collectCodexImagesFromMap(response, result)
	}
	if item, ok := payload["item"].(map[string]interface{}); ok {
		collectCodexImagesFromMap(item, result)
	}
}

func codexProviderErrorFromPayload(payload map[string]interface{}) *ProviderError {
	eventType := strings.ToLower(strings.TrimSpace(completionPrompt(payload["type"])))
	switch eventType {
	case "error":
		return providerErrorFromCodexErrorValue(payload["error"], http.StatusBadGateway)
	case "response.failed":
		if response, ok := payload["response"].(map[string]interface{}); ok {
			return providerErrorFromCodexErrorValue(response["error"], http.StatusBadGateway)
		}
	case "response.incomplete":
		reason := ""
		if response, ok := payload["response"].(map[string]interface{}); ok {
			reason = strings.TrimSpace(completionPrompt(response["incomplete_details"]))
			if details, ok := response["incomplete_details"].(map[string]interface{}); ok {
				reason = firstNonEmptyString(completionPrompt(details["reason"]), reason)
			}
		}
		message := "Upstream response incomplete before returning image data"
		if reason != "" && reason != "null" {
			message += ": " + reason
		}
		return &ProviderError{Status: http.StatusGatewayTimeout, Code: "upstream_timeout", Message: message, Type: "api_error"}
	}
	return nil
}

func providerErrorFromCodexErrorValue(value interface{}, fallbackStatus int) *ProviderError {
	errorMap, ok := value.(map[string]interface{})
	if !ok || errorMap == nil {
		message := strings.TrimSpace(completionPrompt(value))
		if message == "" || message == "null" {
			return nil
		}
		return &ProviderError{Status: fallbackStatus, Code: "upstream_error", Message: message, Type: "api_error"}
	}
	code := sanitizeErrorCode(completionPrompt(errorMap["code"]))
	if code == "" || code == "null" {
		code = "upstream_error"
	} else if !strings.HasPrefix(code, "upstream_") {
		code = "upstream_" + code
	}
	message := strings.TrimSpace(completionPrompt(errorMap["message"]))
	if message == "" || message == "null" {
		message = "Upstream returned an error"
	}
	errorType := strings.TrimSpace(completionPrompt(errorMap["type"]))
	if errorType == "" || errorType == "null" {
		errorType = "api_error"
	}
	status := codexErrorStatus(code, message, errorType, fallbackStatus)
	if imagePermissionErrorText(code, message, errorType) {
		code = "upstream_missing_image_scope"
	}
	return &ProviderError{Status: status, Code: code, Message: message, Type: errorType}
}

func codexErrorStatus(code, message, errorType string, fallbackStatus int) int {
	if usageLimitErrorText(code, message, errorType) {
		return http.StatusTooManyRequests
	}
	if billingErrorText(code, message, errorType) {
		return http.StatusPaymentRequired
	}
	if strings.Contains(strings.ToLower(errorType), "invalid_request") || strings.Contains(strings.ToLower(code), "invalid_request") {
		return http.StatusBadRequest
	}
	if fallbackStatus > 0 {
		return fallbackStatus
	}
	return http.StatusBadGateway
}

func billingErrorText(parts ...string) bool {
	text := strings.ToLower(strings.Join(parts, " "))
	return strings.Contains(text, "billing") ||
		strings.Contains(text, "payment") ||
		strings.Contains(text, "insufficient_quota") ||
		strings.Contains(text, "quota")
}

func codexOutputTextDelta(payload map[string]interface{}) string {
	if !strings.EqualFold(completionPrompt(payload["type"]), "response.output_text.delta") {
		return ""
	}
	return completionPrompt(payload["delta"])
}

func codexCompletedText(payload map[string]interface{}) string {
	if response, ok := payload["response"].(map[string]interface{}); ok {
		return codexResponseText(response)
	}
	return codexResponseText(payload)
}

func codexResponseText(payload map[string]interface{}) string {
	if text := completionPrompt(payload["output_text"]); strings.TrimSpace(text) != "" && text != "null" {
		return text
	}
	output, ok := payload["output"].([]interface{})
	if !ok {
		return ""
	}
	parts := []string{}
	for _, item := range output {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if text := codexOutputItemText(entry); strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

func codexOutputItemText(item map[string]interface{}) string {
	if text := completionPrompt(item["text"]); strings.TrimSpace(text) != "" && text != "null" {
		return text
	}
	content, ok := item["content"].([]interface{})
	if !ok {
		return ""
	}
	parts := []string{}
	for _, partValue := range content {
		part, ok := partValue.(map[string]interface{})
		if !ok {
			continue
		}
		partType := strings.ToLower(strings.TrimSpace(completionPrompt(part["type"])))
		if partType != "" && partType != "output_text" && partType != "text" {
			continue
		}
		if text := strings.TrimSpace(completionPrompt(part["text"])); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

func collectCodexImagesFromMap(payload map[string]interface{}, result *chatGPTCodexParseResult) {
	if payload == nil || result == nil {
		return
	}
	if image := codexImageFromMap(payload); image != nil {
		appendCodexImage(result, image)
	}
	if output, ok := payload["output"].([]interface{}); ok {
		for _, item := range output {
			if entry, ok := item.(map[string]interface{}); ok {
				collectCodexImagesFromMap(entry, result)
			}
		}
	}
}

func codexImageFromMap(payload map[string]interface{}) gin.H {
	itemType := strings.ToLower(strings.TrimSpace(completionPrompt(payload["type"])))
	result := strings.TrimSpace(completionPrompt(payload["result"]))
	if result == "" {
		result = strings.TrimSpace(completionPrompt(payload["b64_json"]))
	}
	if result == "" {
		result = strings.TrimSpace(completionPrompt(payload["partial_image_b64"]))
	}
	if result == "" || (itemType != "image_generation_call" && itemType != "response.image_generation_call.partial_image" && payload["result"] == nil && payload["b64_json"] == nil && payload["partial_image_b64"] == nil) {
		return nil
	}
	image := gin.H{"b64_json": result}
	for _, key := range []string{"revised_prompt", "output_format", "quality", "size", "background"} {
		if value := strings.TrimSpace(completionPrompt(payload[key])); value != "" && value != "null" {
			image[key] = value
		}
	}
	return image
}

func appendCodexImage(result *chatGPTCodexParseResult, image gin.H) {
	if result.imageSeen == nil {
		result.imageSeen = map[string]bool{}
	}
	key := completionPrompt(image["b64_json"])
	if key == "" || result.imageSeen[key] {
		return
	}
	result.imageSeen[key] = true
	result.Images = append(result.Images, image)
}

func codexUsage(value interface{}) (gin.H, bool) {
	switch usage := value.(type) {
	case map[string]interface{}:
		return gin.H(usage), true
	case gin.H:
		return usage, true
	default:
		return nil, false
	}
}

func chatCompletionFromCodexResult(call GatewayCall, result chatGPTCodexParseResult) gin.H {
	text := result.Text
	if text == "" {
		text = result.CompletedText
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
					"content": text,
				},
				"finish_reason": "stop",
			},
		},
		"usage": chatCompletionUsageFromCodex(result.Usage, call.Body.Messages),
	}
}

func chatCompletionUsageFromCodex(usage gin.H, messages []ChatMessage) gin.H {
	promptTokens := estimateTokens(messages)
	completionTokens := 18
	if usage != nil {
		promptTokens, completionTokens = tokenUsageFromMap(map[string]interface{}(usage), promptTokens, completionTokens)
	}
	totalTokens := promptTokens + completionTokens
	if usage != nil {
		if value, ok := asInt(usage["total_tokens"]); ok {
			totalTokens = value
		}
	}
	return gin.H{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      totalTokens,
	}
}

func (s *Server) fetchUpstreamModelIDs(channel Channel, upstreamKey string) ([]string, error) {
	if isCodexChannel(channel) {
		return codexChannelModelIDs(), nil
	}
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

func (s *Server) checkOpenAIAccount(account OpenAIAccount, channel Channel, allowProbe bool) OpenAIAccountCheckResult {
	result := OpenAIAccountCheckResult{Status: "unchecked"}
	accessToken, err := s.revealSecret(account.AccessToken)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	refreshToken := ""
	hasRefreshToken := strings.TrimSpace(account.RefreshToken) != ""
	if hasRefreshToken {
		refreshToken, err = s.revealSecret(account.RefreshToken)
		if err != nil {
			result.Message = err.Error()
			return result
		}
	}
	if !hasRefreshToken && strings.TrimSpace(account.SessionToken) != "" && openAIAccountAccessTokenExpiring(accessToken, account.ExpiresAt, 5*time.Minute) {
		refreshedAccessToken, resolveErr := s.resolveOpenAIAccountAccessToken(account)
		if resolveErr != nil {
			result.Message = resolveErr.Error()
			return result
		}
		accessToken = refreshedAccessToken
	}
	if openAIAccountAccessExpired(account.ExpiresAt) && strings.TrimSpace(refreshToken) != "" {
		refreshed, err := s.refreshOpenAIAccount(refreshToken)
		if err == nil && strings.TrimSpace(refreshed.AccessToken) != "" {
			accessToken = refreshed.AccessToken
			protectedAccess, err := s.protectSecret(refreshed.AccessToken)
			if err == nil {
				result.AccessToken = protectedAccess
			}
			if strings.TrimSpace(refreshed.RefreshToken) != "" {
				protectedRefresh, err := s.protectSecret(refreshed.RefreshToken)
				if err == nil {
					result.RefreshToken = protectedRefresh
					refreshToken = refreshed.RefreshToken
				}
			}
			if strings.TrimSpace(refreshed.IDToken) != "" {
				protectedID, err := s.protectSecret(refreshed.IDToken)
				if err == nil {
					result.IDToken = protectedID
				}
			}
			if refreshed.ExpiresAt != "" {
				result.ExpiresAt = refreshed.ExpiresAt
			}
			result.LastRefresh = now()
			hasRefreshToken = strings.TrimSpace(refreshToken) != ""
		} else if err != nil {
			result.Message = "refresh failed: " + err.Error()
		}
	}
	if strings.TrimSpace(accessToken) == "" {
		if strings.TrimSpace(refreshToken) == "" {
			result.Message = "missing access_token and refresh_token"
			return result
		}
		refreshed, err := s.refreshOpenAIAccount(refreshToken)
		if err != nil || strings.TrimSpace(refreshed.AccessToken) == "" {
			if err != nil {
				result.Message = "refresh failed: " + err.Error()
			} else {
				result.Message = "refresh returned empty access_token"
			}
			return result
		}
		accessToken = refreshed.AccessToken
		if protectedAccess, err := s.protectSecret(refreshed.AccessToken); err == nil {
			result.AccessToken = protectedAccess
		}
		if strings.TrimSpace(refreshed.RefreshToken) != "" {
			if protectedRefresh, err := s.protectSecret(refreshed.RefreshToken); err == nil {
				result.RefreshToken = protectedRefresh
				refreshToken = refreshed.RefreshToken
			}
		}
		if strings.TrimSpace(refreshed.IDToken) != "" {
			if protectedID, err := s.protectSecret(refreshed.IDToken); err == nil {
				result.IDToken = protectedID
			}
		}
		if refreshed.ExpiresAt != "" {
			result.ExpiresAt = refreshed.ExpiresAt
		}
		result.LastRefresh = now()
		hasRefreshToken = strings.TrimSpace(refreshToken) != ""
	}
	if openAIAccountAccessTokenExpiring(accessToken, firstNonEmptyString(result.ExpiresAt, account.ExpiresAt), 5*time.Minute) {
		if strings.TrimSpace(refreshToken) == "" {
			result.Message = "access_token expired, missing refresh_token"
			return result
		}
		refreshed, err := s.refreshOpenAIAccount(refreshToken)
		if err != nil || strings.TrimSpace(refreshed.AccessToken) == "" {
			if err != nil {
				result.Message = "refresh failed: " + err.Error()
			} else {
				result.Message = "refresh returned empty access_token"
			}
			return result
		}
		accessToken = refreshed.AccessToken
		if protectedAccess, err := s.protectSecret(refreshed.AccessToken); err == nil {
			result.AccessToken = protectedAccess
		}
		if strings.TrimSpace(refreshed.RefreshToken) != "" {
			if protectedRefresh, err := s.protectSecret(refreshed.RefreshToken); err == nil {
				result.RefreshToken = protectedRefresh
				refreshToken = refreshed.RefreshToken
			}
		}
		if strings.TrimSpace(refreshed.IDToken) != "" {
			if protectedID, err := s.protectSecret(refreshed.IDToken); err == nil {
				result.IDToken = protectedID
			}
		}
		if refreshed.ExpiresAt != "" {
			result.ExpiresAt = refreshed.ExpiresAt
		}
		result.LastRefresh = now()
		hasRefreshToken = strings.TrimSpace(refreshToken) != ""
	}
	_, result.PlanType = openAIClaimsAccountInfo(accessToken)
	result.PlanType = firstNonEmptyString(result.PlanType, account.PlanType)
	quotaLimits, providerErr := s.checkOpenAIAccountUsage(accessToken, account)
	if providerErr != nil {
		if allowProbe && isSub2APIAccount(account) && (providerErr.Status == http.StatusUnauthorized || providerErr.Status == http.StatusForbidden) {
			if probeErr := s.probeOpenAIAccountViaCodex(account, channel, accessToken); probeErr == nil {
				// Sub2API can reject the quota endpoint while the account remains
				// usable for the actual Codex request path.
				result.Status = "healthy"
				result.Message = ""
				return result
			} else {
				providerErr = probeErr
			}
		}
		result.ErrorCode = providerErr.Code
		result.Message = fmt.Sprintf("HTTP %d: %s", providerErr.Status, truncateString(providerErr.Message, 300))
		if shouldInvalidateOpenAIAccountForUsage(account, providerErr, hasRefreshToken) {
			result.Status = "invalid"
		}
		return result
	}
	if len(quotaLimits) == 0 {
		result.Message = "usage response missing rate_limit"
		return result
	}
	result.QuotaLimits = quotaLimits
	result.Status = "healthy"
	if !openAIQuotaLimitsHaveRemaining(quotaLimits) {
		result.Message = "usage limit reached"
		return result
	}
	result.Message = ""
	return result
}

func isSub2APIAccount(account OpenAIAccount) bool {
	switch strings.ToLower(strings.TrimSpace(account.Source)) {
	case "sub", "sub2", "sub2api":
		return true
	default:
		return false
	}
}

func (s *Server) probeOpenAIAccountViaCodex(account OpenAIAccount, channel Channel, accessToken string) *ProviderError {
	modelID := "gpt-5.4"
	if len(channel.Models) > 0 && strings.TrimSpace(channel.Models[0]) != "" {
		modelID = strings.TrimSpace(channel.Models[0])
	}
	call := GatewayCall{
		RequestID: newID("health"),
		Model:     Model{ID: modelID},
		Channel:   channel,
		Body: ChatRequest{Model: modelID, Messages: []ChatMessage{
			{Role: "user", Content: "ping"},
		}},
	}
	_, providerErr := s.callChatGPTCodexWithAccount(call, account, accessToken)
	return providerErr
}

// Sub2API may surface account-level or subscription errors as HTTP 401/403
// from its usage endpoint. Only eject those accounts when the response
// explicitly identifies an invalid credential; CPA keeps the stricter legacy
// behavior because its gateway uses 401/403 for authentication failures.
func shouldInvalidateOpenAIAccountForUsage(account OpenAIAccount, providerErr *ProviderError, hasRefreshToken bool) bool {
	if providerErr == nil {
		return false
	}
	if providerErr.Status == http.StatusPaymentRequired && !hasRefreshToken {
		return true
	}
	if !isCodexProxyImportSource(account.Source) || strings.EqualFold(strings.TrimSpace(account.Source), "cpa") {
		return providerErr.Status == http.StatusUnauthorized || providerErr.Status == http.StatusForbidden
	}
	return isExplicitTokenInvalidProviderError(providerErr)
}

func isExplicitTokenInvalidProviderError(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	if isTokenInvalidatedProviderError(providerErr) {
		return true
	}
	text := strings.ToLower(strings.Join([]string{providerErr.Code, providerErr.Message, providerErr.Type}, " "))
	return strings.Contains(text, "invalid token") ||
		strings.Contains(text, "token rejected") ||
		strings.Contains(text, "invalid_api_key") ||
		strings.Contains(text, "authentication failed")
}

func (s *Server) checkOpenAIAccountUsage(accessToken string, account OpenAIAccount) ([]OpenAIQuotaLimit, *ProviderError) {
	request, err := http.NewRequest(http.MethodGet, joinURL(s.chatGPTAPIBase, "wham/usage"), nil)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	request.Header.Set("Accept", "application/json")
	setChatGPTCodexClientHeaders(request, account)
	if accountID := strings.TrimSpace(account.AccountID); accountID != "" {
		setHeaderPreserveCase(request.Header, "Chatgpt-Account-Id", accountID)
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_unreachable", Message: err.Error(), Type: "api_error"}
	}
	defer response.Body.Close()
	content, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return parseWhamUsageQuotaLimits(content), nil
	}
	return nil, providerErrorFromUpstream(response.StatusCode, content)
}

func (s *Server) checkOpenAIAccountImageGeneration(channel Channel, accessToken string) *ProviderError {
	modelID := imageHealthCheckModelID(channel)
	if modelID == "" {
		return &ProviderError{Status: http.StatusBadRequest, Code: "image_model_not_configured", Message: "No image model is configured for this channel", Type: "invalid_request_error"}
	}
	call := ImageGatewayCall{
		RequestID: newID("req"),
		Model:     Model{ID: modelID},
		Channel:   channel,
		Body: ImageRequest{
			Model:  modelID,
			Prompt: "health check",
			Payload: map[string]interface{}{
				"model":  modelID,
				"prompt": "health check",
				"n":      float64(1),
				"size":   "1024x1024",
			},
		},
	}
	_, providerErr := s.callChatGPTCodexImageWithAccount(call, OpenAIAccount{}, accessToken)
	return providerErr
}

func imageHealthCheckModelID(channel Channel) string {
	for _, candidate := range channel.Models {
		if strings.EqualFold(strings.TrimSpace(candidate), "gpt-image-2") {
			return "gpt-image-2"
		}
	}
	for _, candidate := range channel.Models {
		if strings.Contains(strings.ToLower(candidate), "image") {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

func upstreamBillingError(content []byte) bool {
	var payload struct {
		Error struct {
			Code interface{} `json:"code"`
			Type string      `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		return false
	}
	code := strings.ToLower(completionPrompt(payload.Error.Code))
	errorType := strings.ToLower(strings.TrimSpace(payload.Error.Type))
	return strings.Contains(code, "billing") || strings.Contains(errorType, "billing")
}

func parseWhamUsageQuotaLimits(content []byte) []OpenAIQuotaLimit {
	var payload interface{}
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil
	}
	limits := []OpenAIQuotaLimit{}
	if root, ok := payload.(map[string]interface{}); ok {
		limits = append(limits, whamRateLimitWindows(root)...)
	}
	collectQuotaLimits(payload, nil, &limits)
	if len(limits) == 0 {
		return nil
	}
	seen := map[string]bool{}
	result := []OpenAIQuotaLimit{}
	for _, limit := range limits {
		limit = normalizeQuotaLimit(limit)
		if limit.Label == "" {
			continue
		}
		key := strings.ToLower(limit.Label + "|" + limit.Window + "|" + limit.ResetAt)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, limit)
		if len(result) >= 8 {
			break
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		return quotaPriority(result[left]) < quotaPriority(result[right])
	})
	return result
}

func whamRateLimitWindows(payload map[string]interface{}) []OpenAIQuotaLimit {
	rateLimit, _ := payload["rate_limit"].(map[string]interface{})
	if rateLimit == nil {
		rateLimit = payload
	}
	windows := []struct {
		Key   string
		Label string
	}{
		{Key: "primary_window", Label: "5h"},
		{Key: "secondary_window", Label: "Weekly"},
		{Key: "monthly_window", Label: "Monthly"},
	}
	limits := []OpenAIQuotaLimit{}
	for _, window := range windows {
		values, _ := rateLimit[window.Key].(map[string]interface{})
		if values == nil {
			continue
		}
		if limit, ok := whamQuotaLimitFromWindow(window.Key, window.Label, values); ok {
			limits = append(limits, limit)
		}
	}
	return limits
}

func whamQuotaLimitFromWindow(name string, label string, values map[string]interface{}) (OpenAIQuotaLimit, bool) {
	usedPercent, hasUsedPercent := numberFromAnyKeys(values, "used_percent")
	if !hasUsedPercent {
		return OpenAIQuotaLimit{}, false
	}
	usedPercent = clampPercent(usedPercent)
	resetAt := stringFromAnyKeys(values, "reset_at", "reset_time", "resets_at")
	if resetAt == "" {
		if resetAfterSeconds, ok := numberFromAnyKeys(values, "reset_after_seconds", "reset_after_sec", "reset_after", "resets_in_seconds", "reset_in_seconds", "seconds_until_reset"); ok && resetAfterSeconds > 0 {
			resetAt = time.Now().Add(time.Duration(resetAfterSeconds) * time.Second).UTC().Format(time.RFC3339)
		}
	}
	return OpenAIQuotaLimit{
		Name:             firstNonEmptyString(stringFromAnyKeys(values, "name", "window_name", "window_key"), name),
		Label:            label,
		Window:           quotaWindowFromLabel(label),
		Limit:            100,
		Used:             usedPercent,
		Remaining:        math.Max(0, 100-usedPercent),
		PercentRemaining: math.Max(0, 100-usedPercent),
		ResetAt:          normalizeQuotaResetAt(resetAt),
	}, true
}

func collectQuotaLimits(value interface{}, path []string, limits *[]OpenAIQuotaLimit) {
	switch typed := value.(type) {
	case map[string]interface{}:
		if limit, ok := quotaLimitFromMap(typed, path); ok {
			*limits = append(*limits, limit)
		}
		for key, child := range typed {
			collectQuotaLimits(child, append(path, key), limits)
		}
	case []interface{}:
		for _, child := range typed {
			collectQuotaLimits(child, path, limits)
		}
	}
}

func quotaLimitFromMap(values map[string]interface{}, path []string) (OpenAIQuotaLimit, bool) {
	if whamUsageWindowPath(path) {
		if _, ok := values["used_percent"]; ok {
			return OpenAIQuotaLimit{}, false
		}
	}
	limit, hasLimit := numberFromAnyKeys(values, "limit", "max", "cap", "quota", "total", "allowed", "maximum", "capacity")
	used, hasUsed := numberFromAnyKeys(values, "used", "num_used", "current", "consumed", "usage", "used_count", "count")
	remaining, hasRemaining := numberFromAnyKeys(values, "remaining", "available", "left", "remaining_messages", "remaining_credits", "remaining_count", "remaining_uses")
	percent, hasPercent := percentFromAnyKeys(values, "percent_remaining", "remaining_percent", "remaining_percentage", "remaining_pct", "percentage", "pct_remaining")
	if !hasPercent {
		if usedPercent, ok := numberFromAnyKeys(values, "used_percent", "used_percentage", "used_pct"); ok {
			percent = math.Max(0, 100-usedPercent)
			hasPercent = true
			if !hasRemaining {
				remaining = percent
				hasRemaining = true
			}
			if !hasLimit {
				limit = 100
				hasLimit = true
			}
			if !hasUsed {
				used = usedPercent
				hasUsed = true
			}
		}
	}
	if !hasRemaining && hasLimit {
		if hasUsed {
			remaining = math.Max(0, limit-used)
			hasRemaining = true
		} else if !hasPercent {
			remaining = limit
			hasRemaining = true
		}
	}
	if !hasPercent && hasLimit && limit > 0 && hasRemaining {
		percent = (remaining / limit) * 100
		hasPercent = true
	}
	resetAt := stringFromAnyKeys(values, "reset_at", "resets_at", "reset_time", "next_reset", "expires_at")
	if resetAt == "" {
		if resetAfterSeconds, ok := numberFromAnyKeys(values, "reset_after_seconds", "reset_after_sec", "reset_after", "resets_in_seconds", "reset_in_seconds", "seconds_until_reset"); ok && resetAfterSeconds > 0 {
			resetAt = time.Now().Add(time.Duration(resetAfterSeconds) * time.Second).UTC().Format(time.RFC3339)
		}
	}
	label := firstNonEmptyString(
		stringFromAnyKeys(values, "label", "name", "title", "bucket", "window", "period", "model"),
		quotaLabelFromPath(path),
	)
	if !hasLimit && !hasUsed && !hasRemaining && !hasPercent && resetAt == "" {
		return OpenAIQuotaLimit{}, false
	}
	if !quotaLabelLooksUseful(label) && resetAt == "" && !hasPercent {
		return OpenAIQuotaLimit{}, false
	}
	return OpenAIQuotaLimit{
		Name:             label,
		Label:            quotaDisplayLabel(label),
		Window:           quotaWindowFromLabel(label),
		Limit:            limit,
		Used:             used,
		Remaining:        remaining,
		PercentRemaining: percent,
		ResetAt:          normalizeQuotaResetAt(resetAt),
	}, true
}

func whamUsageWindowPath(path []string) bool {
	if len(path) < 2 {
		return false
	}
	parent := strings.ToLower(strings.TrimSpace(path[len(path)-2]))
	name := strings.ToLower(strings.TrimSpace(path[len(path)-1]))
	return parent == "rate_limit" && (name == "primary_window" || name == "secondary_window" || name == "monthly_window")
}

func normalizeQuotaLimit(limit OpenAIQuotaLimit) OpenAIQuotaLimit {
	if limit.Name == "" {
		limit.Name = limit.Label
	}
	if limit.Label == "" {
		limit.Label = quotaDisplayLabel(limit.Name)
	}
	if limit.Window == "" {
		limit.Window = quotaWindowFromLabel(limit.Name)
	}
	if limit.Remaining == 0 && limit.Limit > 0 && limit.Used > 0 {
		limit.Remaining = math.Max(0, limit.Limit-limit.Used)
	}
	if limit.PercentRemaining == 0 && limit.Limit > 0 {
		if limit.Remaining > 0 {
			limit.PercentRemaining = clampPercent((limit.Remaining / limit.Limit) * 100)
		} else if limit.Used > 0 {
			limit.PercentRemaining = clampPercent(((limit.Limit - limit.Used) / limit.Limit) * 100)
		}
	}
	limit.PercentRemaining = clampPercent(limit.PercentRemaining)
	return limit
}

func openAIQuotaLimitsHaveRemaining(limits []OpenAIQuotaLimit) bool {
	hasPrimaryWindow := false
	primaryWindowHasRemaining := false
	for _, limit := range limits {
		if quotaPriority(limit) == 0 {
			hasPrimaryWindow = true
			if openAIQuotaLimitHasRemaining(limit) {
				primaryWindowHasRemaining = true
			}
		}
	}
	if hasPrimaryWindow {
		return primaryWindowHasRemaining
	}
	for _, limit := range limits {
		if openAIQuotaLimitHasRemaining(limit) {
			return true
		}
	}
	return false
}

func openAIQuotaLimitHasRemaining(limit OpenAIQuotaLimit) bool {
	if limit.Remaining > 0 || limit.PercentRemaining > 0 {
		return true
	}
	return limit.Limit > 0 && limit.Used > 0 && limit.Used < limit.Limit
}

func numberFromAnyKeys(values map[string]interface{}, keys ...string) (float64, bool) {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if number, ok := numberFromAny(value); ok {
				return number, true
			}
		}
	}
	return 0, false
}

func percentFromAnyKeys(values map[string]interface{}, keys ...string) (float64, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		if percent, ok := percentFromAny(value); ok {
			return percent, true
		}
	}
	return 0, false
}

func percentFromAny(value interface{}) (float64, bool) {
	if text, ok := value.(string); ok {
		trimmed := strings.TrimSpace(text)
		percent, ok := numberFromAny(trimmed)
		if !ok {
			return 0, false
		}
		if strings.HasSuffix(trimmed, "%") {
			return percent, true
		}
		if percent > 0 && percent <= 1 {
			return percent * 100, true
		}
		return percent, true
	}
	percent, ok := numberFromAny(value)
	if !ok {
		return 0, false
	}
	if percent > 0 && percent <= 1 {
		return percent * 100, true
	}
	return percent, true
}

func numberFromAny(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case string:
		number, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(typed), "%"), 64)
		return number, err == nil
	default:
		return 0, false
	}
}

func stringFromAnyKeys(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text := stringFromAny(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func stringFromAny(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		if typed > 1000000000 {
			return time.Unix(int64(typed), 0).UTC().Format(time.RFC3339)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int64:
		if typed > 1000000000 {
			return time.Unix(typed, 0).UTC().Format(time.RFC3339)
		}
		return strconv.FormatInt(typed, 10)
	case int:
		if typed > 1000000000 {
			return time.Unix(int64(typed), 0).UTC().Format(time.RFC3339)
		}
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func quotaLabelFromPath(path []string) string {
	for index := len(path) - 1; index >= 0; index-- {
		label := strings.ToLower(strings.TrimSpace(path[index]))
		if quotaLabelLooksUseful(label) {
			return path[index]
		}
	}
	if len(path) > 0 {
		return path[len(path)-1]
	}
	return ""
}

func quotaLabelLooksUseful(label string) bool {
	label = strings.ToLower(label)
	return strings.Contains(label, "5h") ||
		strings.Contains(label, "5_hour") ||
		strings.Contains(label, "hour") ||
		strings.Contains(label, "weekly") ||
		strings.Contains(label, "week") ||
		strings.Contains(label, "gpt") ||
		strings.Contains(label, "message") ||
		strings.Contains(label, "codex")
}

func quotaDisplayLabel(label string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(label), "_", " "))
	switch {
	case strings.Contains(normalized, "5h") || strings.Contains(normalized, "5 hour"):
		return "5h"
	case strings.Contains(normalized, "weekly") || strings.Contains(normalized, "week"):
		return "Weekly"
	case strings.Contains(normalized, "gpt"):
		return "GPT"
	case strings.Contains(normalized, "codex"):
		return "Codex"
	case strings.Contains(normalized, "message"):
		return "Messages"
	default:
		return strings.TrimSpace(label)
	}
}

func quotaWindowFromLabel(label string) string {
	normalized := strings.ToLower(label)
	switch {
	case strings.Contains(normalized, "5h") || strings.Contains(normalized, "5_hour") || strings.Contains(normalized, "5 hour"):
		return "5h"
	case strings.Contains(normalized, "weekly") || strings.Contains(normalized, "week"):
		return "weekly"
	default:
		return ""
	}
}

func normalizeQuotaResetAt(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if number, err := strconv.ParseInt(value, 10, 64); err == nil && number > 1000000000 {
		return time.Unix(number, 0).UTC().Format(time.RFC3339)
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return value
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return math.Round(value*10) / 10
}

func quotaPriority(limit OpenAIQuotaLimit) int {
	label := strings.ToLower(limit.Label + " " + limit.Name + " " + limit.Window)
	switch {
	case strings.Contains(label, "5h"):
		return 0
	case strings.Contains(label, "weekly") || strings.Contains(label, "week"):
		return 1
	case strings.Contains(label, "gpt"):
		return 2
	default:
		return 10
	}
}

type OpenAIRefreshResult struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresAt    string
}

func openAIAccountAccessExpired(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		expiresAt, err := time.Parse(layout, value)
		if err == nil {
			return time.Now().UTC().After(expiresAt.Add(-1 * time.Minute))
		}
	}
	return false
}

func openAIAccountAccessTokenExpiring(accessToken string, expiresAt string, leeway time.Duration) bool {
	if openAIAccountAccessExpiredWithLeeway(expiresAt, leeway) {
		return true
	}
	if jwtExpiresAt, ok := jwtExpiry(accessToken); ok {
		return time.Now().UTC().After(jwtExpiresAt.Add(-leeway))
	}
	return false
}

func openAIAccountAccessExpiredWithLeeway(value string, leeway time.Duration) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		expiresAt, err := time.Parse(layout, value)
		if err == nil {
			return time.Now().UTC().After(expiresAt.Add(-leeway))
		}
	}
	return false
}

func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			return time.Time{}, false
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0).UTC(), true
}

func (s *Server) refreshOpenAIAccount(refreshToken string) (OpenAIRefreshResult, error) {
	form := url.Values{}
	form.Set("client_id", openAIAuthClientID)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("scope", "openid profile email")
	request, err := http.NewRequest(http.MethodPost, joinURL(s.openAIAuthBase, "oauth/token"), strings.NewReader(form.Encode()))
	if err != nil {
		return OpenAIRefreshResult{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return OpenAIRefreshResult{}, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return OpenAIRefreshResult{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		providerErr := providerErrorFromUpstream(response.StatusCode, content)
		return OpenAIRefreshResult{}, fmt.Errorf("%s", providerErr.Message)
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
		ExpiresAt    string `json:"expires_at"`
	}
	if err := json.Unmarshal(content, &body); err != nil {
		return OpenAIRefreshResult{}, err
	}
	result := OpenAIRefreshResult{
		AccessToken:  strings.TrimSpace(body.AccessToken),
		RefreshToken: strings.TrimSpace(body.RefreshToken),
		IDToken:      strings.TrimSpace(body.IDToken),
		ExpiresAt:    strings.TrimSpace(body.ExpiresAt),
	}
	if result.ExpiresAt == "" && body.ExpiresIn > 0 {
		result.ExpiresAt = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second).UTC().Format(time.RFC3339Nano)
	}
	return result, nil
}

func (s *Server) streamOpenAICompatible(c *gin.Context, call GatewayCall) *ProviderError {
	if len(activeOpenAIAccounts(call.Channel.OpenAIAccounts)) > 0 {
		return s.streamChatGPTCodex(c, call)
	}

	endpoint := openAICompatibleChatEndpoint(call.Channel.BaseURL)
	request, providerErr := s.newOpenAICompatibleRequest(call, true, endpoint)
	if providerErr != nil {
		return providerErr
	}

	response, err := s.httpClient.Do(request)
	if err != nil {
		return &ProviderError{Status: http.StatusBadGateway, Code: "upstream_unreachable", Message: err.Error(), Type: "api_error"}
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		content, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		return providerErrorFromUpstream(response.StatusCode, content)
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	_, _ = io.Copy(c.Writer, response.Body)
	_ = response.Body.Close()
	c.Writer.Flush()
	return nil
}

func (s *Server) shouldUseCompatibleProvider(channel Channel) bool {
	if strings.EqualFold(s.providerMode, "compatible") {
		return true
	}
	if isCodexChannel(channel) && len(channel.OpenAIAccounts) > 0 {
		return true
	}
	return strings.TrimSpace(channel.BaseURL) != "" && (strings.TrimSpace(channel.UpstreamAPIKey) != "" || len(channel.OpenAIAccounts) > 0 || strings.TrimSpace(s.upstreamAPIKey) != "")
}

func shouldFallbackStreamToNonStream(providerErr *ProviderError, channel Channel) bool {
	if providerErr == nil || isCodexChannel(channel) {
		return false
	}
	return providerErr.Status == http.StatusNotFound ||
		providerErr.Status == http.StatusMethodNotAllowed ||
		providerErr.Status == http.StatusNotImplemented
}

func (s *Server) newOpenAICompatibleRequest(call GatewayCall, stream bool, endpoint string) (*http.Request, *ProviderError) {
	upstreamKey, err := s.channelUpstreamKey(call.Channel)
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
		if value == nil {
			continue
		}
		payload[key] = value
	}
	payload["model"] = call.Model.ID
	payload["stream"] = stream
	payload["messages"] = call.Body.Messages
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Failed to encode upstream request", Type: "invalid_request_error"}
	}

	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	request.Header.Set("Authorization", "Bearer "+upstreamKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", call.RequestID)
	return request, nil
}

func (s *Server) newOpenAICompatibleImageRequest(call ImageGatewayCall) (*http.Request, *ProviderError) {
	upstreamKey, err := s.channelUpstreamKey(call.Channel)
	if err != nil {
		return nil, &ProviderError{
			Status:  http.StatusBadGateway,
			Code:    "upstream_key_unavailable",
			Message: err.Error(),
			Type:    "api_error",
		}
	}
	return s.newOpenAICompatibleImageRequestWithKey(call, upstreamKey)
}

func (s *Server) newOpenAICompatibleImageRequestWithKey(call ImageGatewayCall, upstreamKey string) (*http.Request, *ProviderError) {
	upstreamKey = strings.TrimSpace(upstreamKey)
	if upstreamKey == "" {
		upstreamKey = s.upstreamAPIKey
	}
	if upstreamKey == "" {
		return nil, &ProviderError{
			Status:  http.StatusBadGateway,
			Code:    "upstream_not_configured",
			Message: "Channel upstreamApiKey or UPSTREAM_API_KEY is required when forwarding image generation",
			Type:    "api_error",
		}
	}

	payload := gin.H{}
	for key, value := range call.Body.Payload {
		payload[key] = value
	}
	payload["model"] = call.Model.ID
	payload["prompt"] = call.Body.Prompt
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Failed to encode upstream request", Type: "invalid_request_error"}
	}

	endpointPath := "images/generations"
	if normalizeImageOperation(call.Operation) == "edit" {
		endpointPath = "images/edits"
	}
	endpoint := joinURL(call.Channel.BaseURL, endpointPath)
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, &ProviderError{Status: http.StatusBadGateway, Code: "upstream_request_error", Message: err.Error(), Type: "api_error"}
	}
	request.Header.Set("Authorization", "Bearer "+upstreamKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", call.RequestID)
	return request, nil
}

func (s *Server) channelUpstreamKey(channel Channel) (string, error) {
	upstreamKey, err := s.revealSecret(channel.UpstreamAPIKey)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(upstreamKey) == "" {
		upstreamKey = s.upstreamAPIKey
	}
	return strings.TrimSpace(upstreamKey), nil
}

func activeOpenAIAccounts(accounts []OpenAIAccount) []OpenAIAccount {
	healthy := []OpenAIAccount{}
	standby := []OpenAIAccount{}
	limited := []OpenAIAccount{}
	for _, account := range accounts {
		if strings.TrimSpace(account.AccessToken) == "" || account.Status == "invalid" {
			continue
		}
		if openAIAccountUsageLimited(account) {
			limited = append(limited, account)
			continue
		}
		if account.Status == "healthy" {
			healthy = append(healthy, account)
			continue
		}
		standby = append(standby, account)
	}
	// Spread requests across healthy accounts. Empty LastUsedAt sorts first so
	// newly imported accounts receive a turn before repeatedly using one old
	// account; equal timestamps preserve the configured order.
	sort.SliceStable(healthy, func(i, j int) bool {
		left := strings.TrimSpace(healthy[i].LastUsedAt)
		right := strings.TrimSpace(healthy[j].LastUsedAt)
		if left == right {
			return false
		}
		if left == "" {
			return true
		}
		if right == "" {
			return false
		}
		return left < right
	})
	ordered := append(healthy, standby...)
	return append(ordered, limited...)
}

func openAIAccountUsageLimited(account OpenAIAccount) bool {
	if len(account.QuotaLimits) > 0 {
		if openAIQuotaLimitsHaveRemaining(account.QuotaLimits) {
			return false
		}
		return !openAIQuotaLimitsResetElapsed(account.QuotaLimits)
	}
	return usageLimitErrorText(account.LastError) && !openAIUsageLimitErrorExpired(account.LastCheckedAt)
}

func openAIQuotaLimitsResetElapsed(limits []OpenAIQuotaLimit) bool {
	relevant := []OpenAIQuotaLimit{}
	for _, limit := range limits {
		if quotaPriority(limit) == 0 {
			relevant = append(relevant, limit)
		}
	}
	if len(relevant) == 0 {
		relevant = limits
	}
	for _, limit := range relevant {
		if openAIQuotaLimitHasRemaining(limit) {
			return true
		}
		if !openAIQuotaLimitResetElapsed(limit) {
			return false
		}
	}
	return len(relevant) > 0
}

func openAIQuotaLimitResetElapsed(limit OpenAIQuotaLimit) bool {
	resetAt := strings.TrimSpace(normalizeQuotaResetAt(limit.ResetAt))
	if resetAt == "" {
		return false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, resetAt)
		if err == nil {
			return !time.Now().UTC().Before(parsed)
		}
	}
	return false
}

func openAIUsageLimitErrorExpired(lastCheckedAt string) bool {
	lastCheckedAt = strings.TrimSpace(lastCheckedAt)
	if lastCheckedAt == "" {
		return false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		checkedAt, err := time.Parse(layout, lastCheckedAt)
		if err == nil {
			return time.Since(checkedAt) >= openAIUsageLimitRetryAfter
		}
	}
	return false
}

func (s *Server) markOpenAIAccountInvalid(channelID string, accountID string, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := s.findChannel(channelID)
	if channel == nil {
		return
	}
	for index := range channel.OpenAIAccounts {
		if channel.OpenAIAccounts[index].ID != accountID {
			continue
		}
		channel.OpenAIAccounts[index].Status = "invalid"
		channel.OpenAIAccounts[index].LastCheckedAt = now()
		channel.OpenAIAccounts[index].LastError = truncateString(message, 500)
		s.saveStateLocked()
		return
	}
}

func (s *Server) markOpenAIAccountUsed(channelID string, accountID string, requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := s.findChannel(channelID)
	if channel == nil {
		return
	}
	for index := range channel.OpenAIAccounts {
		if channel.OpenAIAccounts[index].ID != accountID {
			continue
		}
		channel.OpenAIAccounts[index].LastUsedAt = now()
		channel.OpenAIAccounts[index].RequestCount++
		if requestID != "" {
			s.requestAccounts[requestID] = firstNonEmptyString(channel.OpenAIAccounts[index].Email, channel.OpenAIAccounts[index].Name, channel.OpenAIAccounts[index].AccountID, accountID)
		}
		s.saveStateLocked()
		return
	}
}

func (s *Server) markOpenAIAccountUsageLimited(channelID string, accountID string, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := s.findChannel(channelID)
	if channel == nil {
		return
	}
	for index := range channel.OpenAIAccounts {
		if channel.OpenAIAccounts[index].ID != accountID {
			continue
		}
		channel.OpenAIAccounts[index].Status = "unchecked"
		channel.OpenAIAccounts[index].LastCheckedAt = now()
		channel.OpenAIAccounts[index].LastError = truncateString(firstNonEmptyString(message, "usage limit reached"), 500)
		s.saveStateLocked()
		return
	}
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

func (s *Server) writeFakeProviderStream(c *gin.Context, call GatewayCall, response gin.H) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	chunkID, _ := response["id"].(string)
	if chunkID == "" {
		chunkID = newID("chatcmpl")
	}
	text := assistantTextFromChatCompletion(response)
	if strings.TrimSpace(text) == "" {
		text = " "
	}
	parts := splitFakeStreamText(text)
	for index, part := range parts {
		delta := gin.H{"content": part}
		if index == 0 {
			delta["role"] = "assistant"
		}
		chunk := gin.H{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": unixNow(),
			"model":   call.Model.ID,
			"choices": []gin.H{{
				"index":         0,
				"delta":         delta,
				"finish_reason": nil,
			}},
		}
		writeSSEChunk(c, chunk)
	}
	writeSSEChunk(c, gin.H{
		"id":      chunkID,
		"object":  "chat.completion.chunk",
		"created": unixNow(),
		"model":   call.Model.ID,
		"choices": []gin.H{{
			"index":         0,
			"delta":         gin.H{},
			"finish_reason": "stop",
		}},
	})
	_, _ = c.Writer.WriteString("data: [DONE]\n\n")
	c.Writer.Flush()
}

func writeSSEChunk(c *gin.Context, chunk gin.H) {
	encoded, _ := json.Marshal(chunk)
	_, _ = c.Writer.WriteString("data: " + string(encoded) + "\n\n")
	c.Writer.Flush()
}

func splitFakeStreamText(text string) []string {
	runes := []rune(text)
	if len(runes) == 0 {
		return []string{" "}
	}
	size := 24
	parts := []string{}
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, string(runes[start:end]))
	}
	return parts
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

func removeString(values []string, target string) []string {
	target = strings.TrimSpace(target)
	if target == "" || len(values) == 0 {
		return values
	}
	next := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			next = append(next, value)
		}
	}
	return next
}

func parseOpenAIAccountImport(payload map[string]interface{}) []ImportedOpenAIAccount {
	if payload == nil {
		return nil
	}
	// Codex CLI auth exports keep OAuth credentials under a top-level `tokens`
	// object, unlike CPA exports which store the same fields at the root.
	if tokens, ok := payload["tokens"].(map[string]interface{}); ok {
		accountPayload := make(map[string]interface{}, len(payload)+len(tokens))
		for key, value := range payload {
			accountPayload[key] = value
		}
		for key, value := range tokens {
			accountPayload[key] = value
		}
		delete(accountPayload, "tokens")
		return parseOpenAIAccountImport(accountPayload)
	}
	if rawAccounts, ok := payload["accounts"].([]interface{}); ok {
		accounts := []ImportedOpenAIAccount{}
		for _, raw := range rawAccounts {
			item, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			credentials, _ := item["credentials"].(map[string]interface{})
			codexAuth, _ := firstMapFromAnyKeys(item, "codex_auth", "codexAuthJson", "auth")
			tokens, _ := codexAuth["tokens"].(map[string]interface{})
			if tokens == nil {
				tokens, _ = item["tokens"].(map[string]interface{})
			}
			metadata, _ := item["metadata"].(map[string]interface{})
			account := ImportedOpenAIAccount{
				Name: firstNonEmptyString(
					stringFromMapAnyKeys(item, "name", "account_name", "label", "username"),
					stringFromMapAnyKeys(credentials, "name", "account_name", "label", "username"),
				),
				Email: firstNonEmptyString(
					stringFromMap(credentials, "email"),
					stringFromMap(tokens, "email"),
					stringFromMap(item, "email"),
					stringFromMap(metadata, "email"),
				),
				AccessToken: firstNonEmptyString(
					stringFromMapAnyKeys(credentials, "access_token", "accessToken"),
					stringFromMapAnyKeys(tokens, "access_token", "accessToken"),
					stringFromMapAnyKeys(item, "access_token", "accessToken"),
				),
				RefreshToken: firstNonEmptyString(
					stringFromMapAnyKeys(credentials, "refresh_token", "refreshToken", "chatgpt_refresh_token"),
					stringFromMapAnyKeys(tokens, "refresh_token", "refreshToken", "chatgpt_refresh_token"),
					stringFromMapAnyKeys(item, "refresh_token", "refreshToken", "chatgpt_refresh_token"),
				),
				SessionToken: firstNonEmptyString(
					stringFromMapAnyKeys(credentials, "session_token", "sessionToken"),
					stringFromMapAnyKeys(tokens, "session_token", "sessionToken"),
					stringFromMapAnyKeys(item, "session_token", "sessionToken"),
				),
				IDToken: firstNonEmptyString(
					stringFromMapAnyKeys(credentials, "id_token", "idToken"),
					stringFromMapAnyKeys(tokens, "id_token", "idToken"),
					stringFromMapAnyKeys(item, "id_token", "idToken"),
				),
				AccountID: firstNonEmptyString(
					stringFromMapAnyKeys(credentials, "chatgpt_account_id", "account_id", "accountId"),
					stringFromMapAnyKeys(tokens, "chatgpt_account_id", "account_id", "accountId"),
					stringFromMapAnyKeys(item, "chatgpt_account_id", "account_id", "accountId"),
					stringFromMapAnyKeys(metadata, "chatgpt_account_id", "account_id", "accountId"),
				),
				UserID: firstNonEmptyString(
					stringFromMapAnyKeys(credentials, "chatgpt_user_id", "user_id", "userId"),
					stringFromMapAnyKeys(tokens, "chatgpt_user_id", "user_id", "userId"),
					stringFromMapAnyKeys(item, "chatgpt_user_id", "user_id", "userId"),
				),
				ExpiresAt: firstNonEmptyString(
					stringFromMapAnyKeys(credentials, "expires_at", "expiresAt", "expired"),
					stringFromMapAnyKeys(tokens, "expires_at", "expiresAt", "expired"),
					stringFromMapAnyKeys(item, "expires_at", "expiresAt", "expired"),
				),
				LastRefresh: firstNonEmptyString(
					stringFromMapAnyKeys(credentials, "last_refresh", "lastRefresh"),
					stringFromMapAnyKeys(tokens, "last_refresh", "lastRefresh"),
					stringFromMapAnyKeys(item, "last_refresh", "lastRefresh"),
				),
				PlanType: firstNonEmptyString(
					stringFromMapAnyKeys(credentials, "plan_type", "planType"),
					stringFromMapAnyKeys(tokens, "plan_type", "planType"),
					stringFromMapAnyKeys(item, "plan_type", "planType"),
				),
				Source: "sub2api",
				ClientProfile: firstNonEmptyString(
					stringFromMapAnyKeys(credentials, "client_profile", "clientProfile", "codex_client_profile", "codexClientProfile"),
					stringFromMapAnyKeys(tokens, "client_profile", "clientProfile", "codex_client_profile", "codexClientProfile"),
					stringFromMapAnyKeys(item, "client_profile", "clientProfile", "codex_client_profile", "codexClientProfile", "originator"),
				),
			}
			if account.Email == "" && strings.Contains(account.Name, "@") {
				account.Email = account.Name
			}
			accounts = append(accounts, account)
		}
		return accounts
	}
	if stringFromMapAnyKeys(payload, "access_token", "accessToken") != "" ||
		stringFromMapAnyKeys(payload, "refresh_token", "refreshToken", "chatgpt_refresh_token") != "" ||
		stringFromMapAnyKeys(payload, "session_token", "sessionToken") != "" ||
		stringFromMapAnyKeys(payload, "id_token", "idToken") != "" {
		accountInfo, _ := firstMapFromAnyKeys(payload, "account", "accountInfo")
		userInfo, _ := firstMapFromAnyKeys(payload, "user", "userInfo")
		account := ImportedOpenAIAccount{
			Name:          firstNonEmptyString(stringFromMapAnyKeys(payload, "email", "name", "username"), stringFromMapAnyKeys(userInfo, "email", "name", "username")),
			Email:         firstNonEmptyString(stringFromMap(payload, "email"), stringFromMap(userInfo, "email")),
			AccessToken:   stringFromMapAnyKeys(payload, "access_token", "accessToken"),
			RefreshToken:  stringFromMapAnyKeys(payload, "refresh_token", "refreshToken", "chatgpt_refresh_token"),
			SessionToken:  stringFromMapAnyKeys(payload, "session_token", "sessionToken"),
			IDToken:       stringFromMapAnyKeys(payload, "id_token", "idToken"),
			AccountID:     firstNonEmptyString(stringFromMapAnyKeys(payload, "account_id", "accountId", "chatgpt_account_id"), stringFromMapAnyKeys(accountInfo, "id", "account_id", "accountId")),
			UserID:        firstNonEmptyString(stringFromMapAnyKeys(payload, "chatgpt_user_id", "user_id", "userId"), stringFromMapAnyKeys(userInfo, "id", "user_id", "userId")),
			ExpiresAt:     stringFromMapAnyKeys(payload, "expired", "expires", "expires_at", "expiresAt"),
			LastRefresh:   stringFromMapAnyKeys(payload, "last_refresh", "lastRefresh"),
			PlanType:      firstNonEmptyString(stringFromMapAnyKeys(payload, "plan_type", "planType"), stringFromMapAnyKeys(accountInfo, "plan_type", "planType")),
			Source:        firstNonEmptyString(stringFromMapAnyKeys(payload, "source"), "cpa"),
			ClientProfile: stringFromMapAnyKeys(payload, "client_profile", "clientProfile", "codex_client_profile", "codexClientProfile", "originator"),
		}
		return []ImportedOpenAIAccount{account}
	}
	return nil
}

func parseOpenAIAccountImportRequest(c *gin.Context) ([]ImportedOpenAIAccount, []string, int, error) {
	contentType := strings.ToLower(c.GetHeader("Content-Type"))
	if strings.Contains(contentType, "multipart/form-data") {
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			return nil, nil, 0, fmt.Errorf("missing import file")
		}
		defer file.Close()
		content, err := io.ReadAll(io.LimitReader(file, 128<<20))
		if err != nil {
			return nil, nil, 0, err
		}
		accounts, invalid, err := parseOpenAIAccountImportFile(header.Filename, content)
		if err != nil {
			return nil, nil, invalid, err
		}
		return accounts, stringSliceFromCSV(c.PostForm("models")), invalid, nil
	}

	content, err := io.ReadAll(io.LimitReader(c.Request.Body, 128<<20))
	if err != nil {
		return nil, nil, 0, err
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, nil, 0, fmt.Errorf("empty import body")
	}
	if strings.Contains(contentType, "zip") {
		accounts, invalid, err := parseOpenAIAccountImportFile("import.zip", content)
		return accounts, nil, invalid, err
	}
	return parseOpenAIAccountImportJSON(content)
}

func parseOpenAIAccountImportFile(filename string, content []byte) ([]ImportedOpenAIAccount, int, error) {
	lowerName := strings.ToLower(strings.TrimSpace(filename))
	if strings.HasSuffix(lowerName, ".zip") || isZipContent(content) {
		return parseOpenAIAccountImportZip(content)
	}
	if strings.HasSuffix(lowerName, ".txt") {
		return parseOpenAIAccountImportText(content)
	}
	if lowerName != "" && !strings.HasSuffix(lowerName, ".json") {
		return nil, 1, fmt.Errorf("import file must be .json, .txt or .zip")
	}
	accounts, _, invalid, err := parseOpenAIAccountImportJSON(content)
	return accounts, invalid, err
}

// parseOpenAIAccountImportText accepts JSON Lines exports and one raw access
// token per line. It intentionally ignores empty/comment lines so account
// manager exports can include separators without breaking the batch.
func parseOpenAIAccountImportText(content []byte) ([]ImportedOpenAIAccount, int, error) {
	accounts := []ImportedOpenAIAccount{}
	invalid := 0
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "{") {
			parsed, _, lineInvalid, err := parseOpenAIAccountImportJSON([]byte(line))
			if err != nil || len(parsed) == 0 {
				if lineInvalid > 0 {
					invalid += lineInvalid
				} else {
					invalid++
				}
				continue
			}
			accounts = append(accounts, parsed...)
			continue
		}
		if strings.Count(line, ".") >= 2 && !strings.ContainsAny(line, " \t") {
			accounts = append(accounts, ImportedOpenAIAccount{AccessToken: line, Source: "web-login"})
			continue
		}
		invalid++
	}
	if len(accounts) == 0 {
		return nil, invalid, fmt.Errorf("TXT does not contain importable account credentials")
	}
	return accounts, invalid, nil
}

func parseOpenAIAccountImportJSON(content []byte) ([]ImportedOpenAIAccount, []string, int, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, nil, 1, fmt.Errorf("import JSON is invalid")
	}
	models := stringSliceFromAny(payload["models"])
	importData := payload
	if nested, ok := payload["data"].(map[string]interface{}); ok {
		importData = nested
		models = mergeStrings(models, stringSliceFromAny(importData["models"]))
	}
	accounts := parseOpenAIAccountImport(importData)
	invalid := 0
	if len(accounts) == 0 {
		invalid = 1
	}
	return accounts, models, invalid, nil
}

func parseOpenAIAccountImportZip(content []byte) ([]ImportedOpenAIAccount, int, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, 1, fmt.Errorf("import ZIP is invalid")
	}
	accounts := []ImportedOpenAIAccount{}
	invalid := 0
	jsonFiles := 0
	totalUncompressed := int64(0)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(file.Name), ".json") {
			continue
		}
		jsonFiles++
		if jsonFiles > 10000 {
			return nil, invalid, fmt.Errorf("import ZIP contains too many JSON files")
		}
		if file.UncompressedSize64 > 8<<20 {
			invalid++
			continue
		}
		totalUncompressed += int64(file.UncompressedSize64)
		if totalUncompressed > 128<<20 {
			return nil, invalid, fmt.Errorf("import ZIP JSON content is too large")
		}
		handle, err := file.Open()
		if err != nil {
			invalid++
			continue
		}
		content, err := io.ReadAll(io.LimitReader(handle, 8<<20))
		_ = handle.Close()
		if err != nil {
			invalid++
			continue
		}
		fileAccounts, _, fileInvalid, err := parseOpenAIAccountImportJSON(content)
		if err != nil {
			invalid++
			continue
		}
		invalid += fileInvalid
		accounts = append(accounts, fileAccounts...)
	}
	if jsonFiles == 0 {
		return nil, invalid, fmt.Errorf("import ZIP does not contain JSON files")
	}
	return accounts, invalid, nil
}

func isZipContent(content []byte) bool {
	return len(content) >= 4 && content[0] == 'P' && content[1] == 'K' && content[2] == 0x03 && content[3] == 0x04
}

func (s *Server) ensureChannelModelLocked(modelID string, provider string, category string, description string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || s.findModel(modelID) != nil {
		return false
	}
	category = strings.TrimSpace(category)
	if category == "" {
		category = "渠道"
	}
	description = strings.TrimSpace(description)
	if description == "" {
		description = "Imported channel model"
	}
	s.state.Models = append(s.state.Models, Model{
		ID:          modelID,
		Name:        modelID,
		Vendor:      providerLabel(provider),
		Aliases:     []string{},
		Category:    category,
		Description: description,
		Price:       "自定义",
		Context:     "未配置上下文",
		Status:      "available",
	})
	return true
}

func (s *Server) pruneUnreferencedImportedModelsLocked() []string {
	referenced := map[string]bool{}
	for _, channel := range s.state.Channels {
		for _, modelID := range channel.Models {
			referenced[modelID] = true
		}
	}

	removed := []string{}
	models := make([]Model, 0, len(s.state.Models))
	for _, model := range s.state.Models {
		if referenced[model.ID] || !isImportedModelManagedByChannel(model) {
			models = append(models, model)
			continue
		}
		removed = append(removed, model.ID)
	}
	if len(removed) == 0 {
		return removed
	}

	s.state.Models = models
	for i := range s.state.APIKeys {
		for _, modelID := range removed {
			s.state.APIKeys[i].AllowedModels = removeString(s.state.APIKeys[i].AllowedModels, modelID)
		}
	}
	return removed
}

func isImportedModelManagedByChannel(model Model) bool {
	if model.Name != model.ID || model.Recommended || model.PricingConfigured {
		return false
	}
	if model.InputPricePer1K != 0 || model.OutputPricePer1K != 0 {
		return false
	}
	if len(model.Aliases) > 0 {
		return false
	}
	description := strings.TrimSpace(model.Description)
	switch {
	case description == "Imported channel model":
	case description == "Imported OpenAI account model":
	case description == "从上游模型列表拉取":
	case strings.HasPrefix(description, "由 ") && strings.HasSuffix(description, " 拉取"):
	default:
		return false
	}
	category := strings.TrimSpace(model.Category)
	return category == "" || category == "渠道" || category == "通用" || category == "导入"
}

func (s *Server) ensureImportedModelLocked(modelID string) bool {
	return s.ensureChannelModelLocked(modelID, "openai", "导入", "Imported OpenAI account model")
}

func openAIImageModelIDs() []string {
	return []string{"gpt-image-2", "gpt-image-1"}
}

func codexChannelModelIDs() []string {
	return mergeStrings([]string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5", "gpt-5.4"}, openAIImageModelIDs())
}

func isCodexChannel(channel Channel) bool {
	return strings.EqualFold(strings.TrimSpace(channel.Provider), "codex") || len(channel.OpenAIAccounts) > 0
}

func (s *Server) ensureCodexChannelLocked(channel *Channel) bool {
	if channel == nil {
		return false
	}
	changed := false
	if !strings.EqualFold(strings.TrimSpace(channel.Provider), "codex") {
		channel.Provider = "codex"
		changed = true
	}
	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(channel.BaseURL), "/")
	if trimmedBaseURL == "" || strings.EqualFold(trimmedBaseURL, strings.TrimRight(defaultOpenAIBaseURL, "/")) {
		channel.BaseURL = defaultChatGPTAPIBaseURL
		changed = true
	}
	for _, modelID := range codexChannelModelIDs() {
		if s.ensureImportedModelLocked(modelID) {
			changed = true
		}
	}
	return changed
}

func stringFromMap(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	return stringFromAny(value)
}

func stringFromMapAnyKeys(values map[string]interface{}, keys ...string) string {
	if values == nil {
		return ""
	}
	for _, key := range keys {
		if text := stringFromMap(values, key); text != "" {
			return text
		}
	}
	return ""
}

func firstMapFromAnyKeys(values map[string]interface{}, keys ...string) (map[string]interface{}, bool) {
	if values == nil {
		return nil, false
	}
	for _, key := range keys {
		if nested, ok := values[key].(map[string]interface{}); ok {
			return nested, true
		}
	}
	return nil, false
}

func stringSliceFromAny(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		return cleanAliases(typed)
	case []interface{}:
		return cleanAliases(stringSlice(typed))
	default:
		return []string{}
	}
}

func stringSliceFromCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	return cleanAliases(strings.Split(value, ","))
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func providerLabel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex":
		return "Codex"
	case "cpa", "cliproxyapi", "cli-proxy-api":
		return "CLIProxyAPI"
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

func writeOpenAIStreamError(c *gin.Context, providerErr *ProviderError, param *string) {
	if providerErr == nil {
		providerErr = &ProviderError{Status: http.StatusBadGateway, Code: "upstream_error", Message: "Upstream request failed", Type: "api_error"}
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	payload := gin.H{
		"error": gin.H{
			"message": providerErr.Message,
			"type":    providerErr.Type,
			"param":   param,
			"code":    providerErr.Code,
			"status":  providerErr.Status,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = []byte(`{"error":{"message":"Upstream request failed","type":"api_error","code":"upstream_error"}}`)
	}
	_, _ = c.Writer.Write([]byte("data: "))
	_, _ = c.Writer.Write(encoded)
	_, _ = c.Writer.Write([]byte("\n\n"))
	_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	c.Writer.Flush()
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
		if key.Status != "active" || apiKeyExpired(key) || !keyMatchesSecret(key, token) {
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

func (s *Server) openAIAPIKeyAuthLocked(c *gin.Context) (*AuthContext, bool) {
	token := apiTokenFromRequest(c)
	if token == "" {
		return nil, true
	}
	auth := s.findUserByAPIKeyLocked(token)
	if auth == nil {
		writeOpenAIError(c, http.StatusUnauthorized, "invalid_api_key", "Invalid CatieAPI key", "invalid_request_error", nil)
		return nil, false
	}
	return auth, true
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

func (s *Server) normalizeAllowedModelsLocked(input []string) ([]string, error) {
	if len(input) == 0 {
		return []string{}, nil
	}
	seen := map[string]bool{}
	models := []string{}
	for _, value := range input {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		model := s.resolveModelLocked(value)
		if model == nil {
			return nil, fmt.Errorf("Unknown model: %s", value)
		}
		if seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		models = append(models, model.ID)
	}
	return models, nil
}

func apiKeyAllowsModel(key *APIKey, modelID string) bool {
	if key == nil || len(key.AllowedModels) == 0 {
		return true
	}
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	for _, allowed := range key.AllowedModels {
		if strings.ToLower(strings.TrimSpace(allowed)) == modelID {
			return true
		}
	}
	return false
}

func apiKeyExpired(key *APIKey) bool {
	if key == nil || strings.TrimSpace(key.ExpiresAt) == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, key.ExpiresAt)
	if err != nil {
		expiresAt, err = time.Parse(time.RFC3339Nano, key.ExpiresAt)
	}
	return err == nil && !time.Now().Before(expiresAt)
}

func stringSliceFromPatch(value interface{}) ([]string, bool) {
	raw, ok := value.([]interface{})
	if !ok {
		return nil, false
	}
	result := []string{}
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}

func intFromPatch(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case float64:
		if typed != math.Trunc(typed) {
			return 0, false
		}
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func normalizeAPIKeyExpiresAt(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	expiresAt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		expiresAt, err = time.Parse(time.RFC3339Nano, value)
	}
	if err != nil {
		return "", fmt.Errorf("expiresAt must be RFC3339 time")
	}
	if !expiresAt.After(time.Now()) {
		return "", fmt.Errorf("expiresAt must be in the future")
	}
	return expiresAt.UTC().Format(time.RFC3339), nil
}

func (s *Server) channelCandidatesLocked(modelID string) []Channel {
	candidates := []Channel{}
	for i := range s.state.Channels {
		channel := &s.state.Channels[i]
		if channel.Status == "disabled" {
			continue
		}
		for _, id := range channel.Models {
			if id == modelID {
				candidates = append(candidates, *channel)
				break
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		iHealthy := candidates[i].Status == "healthy"
		jHealthy := candidates[j].Status == "healthy"
		if iHealthy != jHealthy {
			return iHealthy
		}
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		if candidates[i].Weight != candidates[j].Weight {
			return candidates[i].Weight > candidates[j].Weight
		}
		return candidates[i].ID < candidates[j].ID
	})
	return candidates
}

func (s *Server) updateChannelRuntimeHealth(channelID string, healthy bool, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := s.findChannel(channelID)
	if channel == nil {
		return
	}
	channel.LastCheckedAt = now()
	if healthy {
		channel.Status = "healthy"
		channel.LastError = ""
	} else {
		channel.Status = "standby"
		channel.LastError = truncateString(strings.TrimSpace(message), 500)
	}
	s.saveStateLocked()
}

func retryableProviderError(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	if isUsageLimitProviderError(providerErr) {
		return true
	}
	if providerErr.Code == "upstream_missing_image_scope" {
		return true
	}
	if providerErr.Code == "stream_not_supported" {
		return true
	}
	if allowedString(providerErr.Code,
		"stream_not_supported",
		"upstream_accounts_unavailable",
		"upstream_unreachable",
		"upstream_read_error",
		"upstream_invalid_json",
		"upstream_timeout",
	) {
		return true
	}
	if providerErr.Status == http.StatusTooManyRequests {
		return true
	}
	if providerErr.Status >= http.StatusInternalServerError && strings.HasPrefix(providerErr.Code, "upstream_") {
		return !allowedString(providerErr.Code,
			"upstream_key_unavailable",
			"upstream_not_configured",
			"upstream_request_error",
		)
	}
	return false
}

func shouldMarkChannelUnhealthy(providerErr *ProviderError) bool {
	if providerErr == nil || providerErr.Code == "stream_not_supported" {
		return false
	}
	if retryableProviderError(providerErr) {
		return true
	}
	return allowedString(providerErr.Code,
		"upstream_authentication_error",
		"upstream_accounts_unavailable",
		"upstream_invalid_api_key",
		"upstream_token_invalidated",
		"upstream_key_unavailable",
		"upstream_not_configured",
		"upstream_request_error",
	)
}

func isBillingProviderError(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	return providerErr.Status == http.StatusPaymentRequired || strings.Contains(strings.ToLower(providerErr.Code), "billing") || strings.Contains(strings.ToLower(providerErr.Type), "billing")
}

func isUsageLimitProviderError(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	if providerErr.Status == http.StatusTooManyRequests {
		return true
	}
	return usageLimitErrorText(providerErr.Code, providerErr.Message, providerErr.Type)
}

func usageLimitErrorText(parts ...string) bool {
	text := strings.ToLower(strings.Join(parts, " "))
	return strings.Contains(text, "usage_limit_reached") ||
		strings.Contains(text, "usage limit") ||
		strings.Contains(text, "limit has been reached") ||
		strings.Contains(text, "rate_limit_exceeded") ||
		strings.Contains(text, "quota_exceeded") ||
		strings.Contains(text, "too many requests")
}

func openAIAccountsUsageLimitedError(scope string) *ProviderError {
	return &ProviderError{
		Status:  http.StatusTooManyRequests,
		Code:    "upstream_accounts_usage_limited",
		Message: fmt.Sprintf("All active OpenAI accounts for %s are currently usage-limited. Wait for reset or import accounts with remaining quota, then run batch check.", scope),
		Type:    "rate_limit_error",
	}
}

func shouldInvalidateOpenAIAccountForImage(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	// Image backends do not consistently attach a token-invalidated error code
	// to an expired/revoked credential. A plain 401/403 must still eject this
	// account so the pool can continue with the next one.
	if providerErr.Status == http.StatusUnauthorized || providerErr.Status == http.StatusForbidden {
		return true
	}
	return isBillingProviderError(providerErr) || isImagePermissionProviderError(providerErr) || isTokenInvalidatedProviderError(providerErr)
}

func shouldInvalidateOpenAIAccountForChat(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	if isTokenInvalidatedProviderError(providerErr) || providerErr.Status == http.StatusUnauthorized {
		return true
	}
	return allowedString(providerErr.Code, "upstream_authentication_error", "upstream_invalid_api_key")
}

func isImagePermissionProviderError(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	return imagePermissionErrorText(providerErr.Code, providerErr.Message, providerErr.Type)
}

func imagePermissionErrorText(parts ...string) bool {
	text := strings.ToLower(strings.Join(parts, " "))
	if strings.Contains(text, "api.model.images.request") || strings.Contains(text, "api.model.images.") {
		return true
	}
	if strings.Contains(text, "missing scope") && strings.Contains(text, "image") {
		return true
	}
	return strings.Contains(text, "insufficient permissions") && strings.Contains(text, "image")
}

func isTokenInvalidatedProviderError(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	text := strings.ToLower(strings.Join([]string{providerErr.Code, providerErr.Message, providerErr.Type}, " "))
	return strings.Contains(text, "token_invalidated") ||
		strings.Contains(text, "authentication token has been invalidated") ||
		strings.Contains(text, "try signing in again")
}

func (s *Server) checkRateLimitLocked(key *APIKey) bool {
	bucket := fmt.Sprintf("%s:%d", key.ID, time.Now().Unix()/60)
	current := s.rateLimitBuckets[bucket]
	limit := s.requestLimitPerMinute
	if key.RateLimitPerMinute > 0 {
		limit = key.RateLimitPerMinute
	}
	if limit > 0 && current >= limit {
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
	case "gpt-5.5":
		return model.Name == "GPT-5.5" && model.Vendor == "OpenAI"
	case "gpt-5.4":
		return model.Name == "GPT-5.4" && model.Vendor == "OpenAI"
	case "gpt-5.6":
		// Legacy demo data cleanup only; 5.6 is not offered as an active default.
		return model.Name == "GPT-5.6" && model.Vendor == "OpenAI"
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
		if isCodexChannel(s.state.Channels[i]) {
			if s.ensureCodexChannelLocked(&s.state.Channels[i]) {
				changed = true
			}
		}
	}
	for i := range s.state.Models {
		if s.state.Models[i].Aliases == nil {
			s.state.Models[i].Aliases = []string{}
			changed = true
		}
	}
	for i := range s.state.APIKeys {
		if s.state.APIKeys[i].AllowedModels == nil {
			s.state.APIKeys[i].AllowedModels = []string{}
			changed = true
		}
	}
	return changed
}

func (s *Server) saveStateLocked() {
	s.pruneOperationalHistoryLocked()
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

func (s *Server) maintenanceSettingsLocked() MaintenanceSettings {
	settings := s.state.Settings.Maintenance
	if settings.LogRetentionDays <= 0 {
		settings.LogRetentionDays = 30
	}
	if settings.MaxLogs <= 0 {
		settings.MaxLogs = 10_000
	}
	if settings.MaxQuotaEntries <= 0 {
		settings.MaxQuotaEntries = 20_000
	}
	return settings
}

func (s *Server) pruneOperationalHistoryLocked() bool {
	settings := s.maintenanceSettingsLocked()
	changed := false
	cutoff := time.Now().AddDate(0, 0, -settings.LogRetentionDays)
	logs := make([]RequestLog, 0, len(s.state.Logs))
	for _, log := range s.state.Logs {
		createdAt, err := time.Parse(time.RFC3339Nano, log.CreatedAt)
		if err == nil && createdAt.Before(cutoff) {
			changed = true
			continue
		}
		logs = append(logs, log)
	}
	sort.SliceStable(logs, func(i, j int) bool {
		return logs[i].CreatedAt < logs[j].CreatedAt
	})
	if len(logs) > settings.MaxLogs {
		logs = append([]RequestLog{}, logs[len(logs)-settings.MaxLogs:]...)
		changed = true
	}
	if len(logs) != len(s.state.Logs) {
		changed = true
	}
	s.state.Logs = logs

	if len(s.state.QuotaLedger) > settings.MaxQuotaEntries {
		s.state.QuotaLedger = append([]QuotaEntry{}, s.state.QuotaLedger[len(s.state.QuotaLedger)-settings.MaxQuotaEntries:]...)
		changed = true
	}
	return changed
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

func randomUUID() string {
	raw := randomHex(16)
	if len(raw) != 32 {
		return raw
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s", raw[:8], raw[8:12], raw[12:16], raw[16:20], raw[20:])
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

func estimateImagePromptTokens(prompt string) int {
	characters := len([]rune(prompt))
	tokens := characters / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}

func calculateCallCost(model Model, response gin.H, messages []ChatMessage, stream bool) float64 {
	promptTokens, completionTokens := callTokenUsage(response, messages, stream)
	inputRate, outputRate := modelRates(model)
	cost := (float64(promptTokens)/1000)*inputRate + (float64(completionTokens)/1000)*outputRate
	if cost > 0 && cost < 0.0001 {
		cost = 0.0001
	}
	return round4(cost)
}

func estimateImageGenerationCost(model Model, request ImageRequest) float64 {
	if !model.PricingConfigured {
		return 0
	}
	promptTokens := estimateImagePromptTokens(request.Prompt)
	inputRate, _ := modelRates(model)
	cost := (float64(promptTokens) / 1000) * inputRate
	if cost > 0 && cost < 0.0001 {
		cost = 0.0001
	}
	return round4(cost)
}

func callTokenUsage(response gin.H, messages []ChatMessage, stream bool) (int, int) {
	promptTokens := estimateTokens(messages)
	completionTokens := 18
	if stream || response == nil {
		return promptTokens, completionTokens
	}
	if usage, ok := response["usage"].(map[string]interface{}); ok {
		return tokenUsageFromMap(usage, promptTokens, completionTokens)
	}
	if usage, ok := response["usage"].(gin.H); ok {
		return tokenUsageFromMap(map[string]interface{}(usage), promptTokens, completionTokens)
	}
	return promptTokens, completionTokens
}

func tokenUsageFromMap(usage map[string]interface{}, promptTokens int, completionTokens int) (int, int) {
	if value, ok := asInt(usage["prompt_tokens"]); ok {
		promptTokens = value
	} else if value, ok := asInt(usage["input_tokens"]); ok {
		promptTokens = value
	}
	if value, ok := asInt(usage["completion_tokens"]); ok {
		completionTokens = value
	} else if value, ok := asInt(usage["output_tokens"]); ok {
		completionTokens = value
	}
	return promptTokens, completionTokens
}

func modelRates(model Model) (float64, float64) {
	if !model.PricingConfigured {
		return 0, 0
	}
	return model.InputPricePer1K, model.OutputPricePer1K
}

func modelWithChannelPricing(model Model, channel Channel) Model {
	if !channel.PricingConfigured {
		return model
	}
	model.InputPricePer1K = channel.InputPricePer1K
	model.OutputPricePer1K = channel.OutputPricePer1K
	model.PricingConfigured = true
	return model
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
	if status == http.StatusForbidden && strings.HasPrefix(strings.ToLower(strings.TrimSpace(message)), "<html") {
		message = "网页上游返回 HTML 403，通常是浏览器校验或反爬拦截"
		return &ProviderError{Status: status, Code: "upstream_web_blocked", Message: message, Type: errorType}
	}
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
			code = sanitizeErrorCode(parsedCode)
			if !strings.HasPrefix(code, "upstream_") {
				code = "upstream_" + code
			}
		}
	}
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = fmt.Sprintf("Upstream returned HTTP %d", status)
	}
	if imagePermissionErrorText(code, message, errorType) {
		code = "upstream_missing_image_scope"
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

var openAIRequestEndpointPrefixes = []string{
	"/v1/images/generations",
	"/images/generations",
	"/v1/images/edits",
	"/images/edits",
	"/v1/chat/completions",
	"/chat/completions",
	"/v1/completions",
	"/completions",
	"/v1/responses",
	"/responses",
	"/v1/embeddings",
	"/embeddings",
	"/v1/models/",
	"/v1/models",
	"/models/",
	"/models",
}

var openAIRequestPathMap = map[string]string{
	"/chat/completions":   "/v1/chat/completions",
	"/completions":        "/v1/completions",
	"/responses":          "/v1/responses",
	"/embeddings":         "/v1/embeddings",
	"/images/generations": "/v1/images/generations",
	"/images/edits":       "/v1/images/edits",
	"/models":             "/v1/models",
	"/models/":            "/v1/models/",
}

func normalizeOpenAIRequestPath(value string) string {
	normalized := normalizeRequestPath(value)
	return extractOpenAIRequestPath(normalized)
}

func extractOpenAIRequestPath(path string) string {
	if path == "" {
		return "/"
	}
	for _, prefix := range []string{
		"/api/admin/",
		"/api/auth/",
		"/api/backup",
		"/api/channels",
		"/api/discord",
		"/api/health",
		"/api/keys",
		"/api/logs",
		"/api/me",
		"/api/models",
		"/api/public/",
		"/api/restore",
		"/api/settings",
		"/api/users",
		"/assets/",
		"/favicon",
		"/index.html",
		"/ws/",
	} {
		if strings.HasPrefix(path, prefix) {
			return path
		}
	}
	for _, endpoint := range openAIRequestEndpointPrefixes {
		index := strings.Index(path, endpoint)
		if index < 0 {
			continue
		}
		extracted := path[index:]
		for shortPath, fullPath := range openAIRequestPathMap {
			if shortPath == "/models/" {
				if strings.HasPrefix(extracted, shortPath) {
					return fullPath + strings.TrimPrefix(extracted, shortPath)
				}
				continue
			}
			if extracted == shortPath || strings.HasPrefix(extracted, shortPath+"/") {
				return fullPath + strings.TrimPrefix(extracted, shortPath)
			}
		}
		return extracted
	}
	return path
}

func normalizeRequestPath(value string) string {
	if value == "" {
		return "/"
	}
	var builder strings.Builder
	builder.Grow(len(value))
	previousSlash := false
	for _, char := range value {
		if char == '/' {
			if previousSlash {
				continue
			}
			previousSlash = true
		} else {
			previousSlash = false
		}
		builder.WriteRune(char)
	}
	normalized := builder.String()
	if normalized == "" {
		return "/"
	}
	return normalized
}

func openAICompatibleChatEndpoint(base string) string {
	apiBase := strings.TrimRight(strings.TrimSpace(base), "/")
	for _, suffix := range []string{"/chat/completions", "/completions", "/models"} {
		if strings.HasSuffix(apiBase, suffix) {
			apiBase = strings.TrimRight(apiBase[:len(apiBase)-len(suffix)], "/")
			break
		}
	}
	return joinURL(apiBase, "chat/completions")
}

func openAICompatibleEmbeddingsEndpoint(base string) string {
	apiBase := strings.TrimRight(strings.TrimSpace(base), "/")
	for _, suffix := range []string{"/chat/completions", "/completions", "/models", "/embeddings"} {
		if strings.HasSuffix(apiBase, suffix) {
			apiBase = strings.TrimRight(apiBase[:len(apiBase)-len(suffix)], "/")
			break
		}
	}
	return joinURL(apiBase, "embeddings")
}

func openAICompatibleAudioSpeechEndpoint(base string) string {
	apiBase := strings.TrimRight(strings.TrimSpace(base), "/")
	for _, suffix := range []string{"/chat/completions", "/completions", "/models", "/embeddings", "/audio/speech"} {
		if strings.HasSuffix(apiBase, suffix) {
			apiBase = strings.TrimRight(apiBase[:len(apiBase)-len(suffix)], "/")
			break
		}
	}
	return joinURL(apiBase, "audio/speech")
}

func openAICompatibleOperationEndpoint(base, operation string) string {
	apiBase := strings.TrimRight(strings.TrimSpace(base), "/")
	for _, suffix := range []string{"/chat/completions", "/completions", "/models", "/embeddings", "/audio/speech", "/moderations"} {
		if strings.HasSuffix(apiBase, suffix) {
			apiBase = strings.TrimRight(apiBase[:len(apiBase)-len(suffix)], "/")
			break
		}
	}
	return joinURL(apiBase, operation)
}

func openAICompatibleChatEndpointCandidates(base string) []string {
	base = strings.TrimSpace(base)
	if base == "" {
		return nil
	}
	candidates := make([]string, 0, 3)
	add := func(endpoint string) {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			return
		}
		for _, existing := range candidates {
			if existing == endpoint {
				return
			}
		}
		candidates = append(candidates, endpoint)
	}

	if parsed, err := url.Parse(base); err == nil {
		normalizedPath := normalizeRequestPath(parsed.Path)
		if strings.Contains(strings.ToLower(normalizedPath), "/chat/completions") {
			add(base)
		}
		add(joinURL(base, "chat/completions"))
		if !pathLooksVersioned(normalizedPath) {
			add(joinURL(base, "v1/chat/completions"))
		}
	} else {
		add(joinURL(base, "chat/completions"))
	}
	return candidates
}

func pathLooksVersioned(path string) bool {
	path = strings.Trim(strings.ToLower(path), "/")
	if path == "" {
		return false
	}
	segments := strings.Split(path, "/")
	last := segments[len(segments)-1]
	if last == "v1beta" {
		return true
	}
	if len(last) >= 2 && last[0] == 'v' {
		for _, char := range last[1:] {
			if char < '0' || char > '9' {
				return false
			}
		}
		return true
	}
	return false
}

func shouldRetryOpenAICompatibleEndpoint(providerErr *ProviderError) bool {
	if providerErr == nil {
		return false
	}
	return providerErr.Status == http.StatusNotFound ||
		providerErr.Status == http.StatusMethodNotAllowed ||
		providerErr.Status == http.StatusNotImplemented
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
		ID:                 key.ID,
		UserID:             key.UserID,
		Name:               key.Name,
		Prefix:             key.Prefix,
		Status:             key.Status,
		CreatedAt:          key.CreatedAt,
		LastUsedAt:         key.LastUsedAt,
		RequestCount:       key.RequestCount,
		AllowedModels:      append([]string{}, key.AllowedModels...),
		ExpiresAt:          key.ExpiresAt,
		RateLimitPerMinute: key.RateLimitPerMinute,
	}
}

func publicChannel(channel Channel) PublicChannel {
	accounts := make([]PublicOpenAIAccount, 0, len(channel.OpenAIAccounts))
	for _, account := range channel.OpenAIAccounts {
		accounts = append(accounts, publicOpenAIAccount(account))
	}
	return PublicChannel{
		ID:                 channel.ID,
		Name:               channel.Name,
		Provider:           channel.Provider,
		BaseURL:            channel.BaseURL,
		UpstreamKeySet:     strings.TrimSpace(channel.UpstreamAPIKey) != "",
		OpenAIAccountCount: len(channel.OpenAIAccounts),
		OpenAIAccounts:     accounts,
		Status:             channel.Status,
		StreamMode:         normalizeStreamMode(channel.StreamMode),
		Priority:           channel.Priority,
		Weight:             channel.Weight,
		Models:             append([]string{}, channel.Models...),
		InputPricePer1K:    channel.InputPricePer1K,
		OutputPricePer1K:   channel.OutputPricePer1K,
		PricingConfigured:  channel.PricingConfigured,
		WebEndpoint:        channel.WebEndpoint,
		LastCheckedAt:      channel.LastCheckedAt,
		LastError:          channel.LastError,
	}
}

func publicOpenAIAccount(account OpenAIAccount) PublicOpenAIAccount {
	credentialMode := "access-token"
	if strings.TrimSpace(account.RefreshToken) != "" {
		credentialMode = "refreshable"
	} else if strings.TrimSpace(account.SessionToken) != "" {
		credentialMode = "browser-session"
	}
	return PublicOpenAIAccount{
		ID:              account.ID,
		Name:            account.Name,
		Email:           account.Email,
		AccountID:       account.AccountID,
		UserID:          account.UserID,
		ExpiresAt:       account.ExpiresAt,
		LastRefresh:     account.LastRefresh,
		PlanType:        account.PlanType,
		Source:          account.Source,
		ClientProfile:   codexClientProfileForImport(account.Source, account.ClientProfile),
		ImportedAt:      account.ImportedAt,
		Status:          firstNonEmptyString(account.Status, "unchecked"),
		LastCheckedAt:   account.LastCheckedAt,
		LastError:       account.LastError,
		LastErrorCode:   account.LastErrorCode,
		LastUsedAt:      account.LastUsedAt,
		RequestCount:    account.RequestCount,
		QuotaLimits:     append([]OpenAIQuotaLimit{}, account.QuotaLimits...),
		HasRefreshToken: strings.TrimSpace(account.RefreshToken) != "",
		HasSessionToken: strings.TrimSpace(account.SessionToken) != "",
		CredentialMode:  credentialMode,
	}
}

func normalizeStreamMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto"
	case "real", "real_stream", "stream":
		return "real"
	case "fake", "fake_stream":
		return "fake"
	case "disabled", "no_stream", "non_stream":
		return "disabled"
	default:
		return ""
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

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func truncateString(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func queryInt(c *gin.Context, name string, fallback int, minimum int, maximum int) int {
	value, err := strconv.Atoi(c.Query(name))
	if err != nil || value < minimum {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
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
