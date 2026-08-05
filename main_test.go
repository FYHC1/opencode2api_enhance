package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVersionStringIncludesBuildMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})

	version = "v1.2.3"
	commit = "abc1234"
	date = "2026-06-04T00:00:00Z"

	got := versionString()
	for _, want := range []string{"opencode2api", "v1.2.3", "abc1234", "2026-06-04T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Fatalf("versionString() = %q, want it to contain %q", got, want)
		}
	}
}
func TestIsFreeModelIncludesBigPickleOnly(t *testing.T) {
	for _, model := range []string{"deepseek-v4-flash-free", "big-pickle", "BIG-PICKLE"} {
		if !isFreeModel(model) {
			t.Fatalf("isFreeModel(%q) = false, want true", model)
		}
	}
	for _, model := range []string{"deepseek-v4-flash", "minimax-m2.7"} {
		if isFreeModel(model) {
			t.Fatalf("isFreeModel(%q) = true, want false", model)
		}
	}
}


type fakeUpstreamResponse struct {
	status int
	body   string
	header http.Header
}

type fakeRetryTransport struct {
	t               *testing.T
	responses       []fakeUpstreamResponse
	requestedModels []string
	requestedURLs   []string
	requestPayloads []map[string]any
	closeIdleCalls  int
}

func (f *fakeRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(f.responses) == 0 {
		f.t.Fatalf("unexpected request to %s", req.URL.String())
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		f.t.Fatalf("read request body: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		f.t.Fatalf("unmarshal request body: %v", err)
	}
	model, _ := payload["model"].(string)
	f.requestedModels = append(f.requestedModels, model)
	f.requestedURLs = append(f.requestedURLs, req.URL.String())
	f.requestPayloads = append(f.requestPayloads, payload)

	next := f.responses[0]
	f.responses = f.responses[1:]
	header := next.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: next.status,
		Header:     header.Clone(),
		Body:       io.NopCloser(strings.NewReader(next.body)),
		Request:    req,
	}, nil
}

func (f *fakeRetryTransport) CloseIdleConnections() {
	f.closeIdleCalls++
}

func installFakeOpenCodeClient(t *testing.T, responses []fakeUpstreamResponse) *fakeRetryTransport {
	t.Helper()

	oldHTTPClient := httpClient
	oldModelsCache := modelsCache
	oldGoModelsCache := goModelsCache
	oldOCClientVer := ocClientVer
	oldOCSessionID := ocSessionID
	oldOCProjectID := ocProjectID
	oldActiveSocks5 := activeSocks5
	oldSocks5Client := socks5Client
	oldSocks5ClientAddr := socks5ClientAddr

	transport := &fakeRetryTransport{
		t:         t,
		responses: append([]fakeUpstreamResponse(nil), responses...),
	}
	httpClient = &http.Client{Transport: transport}

	modelMu.Lock()
	modelsCache = []ModelInfo{{ID: "fallback-model-free"}}
	goModelsCache = nil
	modelMu.Unlock()

	socks5Mu.Lock()
	activeSocks5 = ""
	socks5Client = nil
	socks5ClientAddr = ""
	socks5Mu.Unlock()

	ocOnce = sync.Once{}
	ocOnce.Do(func() {})
	ocClientVer = "test-version"
	ocSessionID = "ses_test"
	ocProjectID = "project_test"

	t.Cleanup(func() {
		httpClient = oldHTTPClient
		modelMu.Lock()
		modelsCache = oldModelsCache
		goModelsCache = oldGoModelsCache
		modelMu.Unlock()
		socks5Mu.Lock()
		activeSocks5 = oldActiveSocks5
		socks5Client = oldSocks5Client
		socks5ClientAddr = oldSocks5ClientAddr
		socks5Mu.Unlock()
		ocOnce = sync.Once{}
		ocClientVer = oldOCClientVer
		ocSessionID = oldOCSessionID
		ocProjectID = oldOCProjectID
	})

	return transport
}

func TestCallOpenCodeAPIRetries4xxAndClosesConnectionBeforeRetry(t *testing.T) {
	tests := []struct {
		name        string
		stream      bool
		responses   []fakeUpstreamResponse
		wantStatus  int
		wantBody    string
		wantModels  []string
		wantCloses  int
		requestBody string
	}{
	// F6: 同模型路由重试，不换候选模型
	{
		name:   "non-stream retries 401",
		stream: false,
		responses: []fakeUpstreamResponse{
			{status: http.StatusUnauthorized, body: `{"error":"unauthorized"}`},
			{status: http.StatusOK, body: `{"id":"chatcmpl_test","choices":[]}`},
		},
		wantStatus:  http.StatusOK,
		wantBody:    `{"id":"chatcmpl_test","choices":[]}`,
		wantModels:  []string{"primary-model", "primary-model"},
		wantCloses:  1,
		requestBody: `{"model":"primary-model","messages":[]}`,
	},
	{
		name:   "stream retries 429",
		stream: true,
		responses: []fakeUpstreamResponse{
			{status: http.StatusTooManyRequests, body: `{"error":"rate_limited"}`},
			{status: http.StatusOK, body: "data: ok\n\n"},
		},
		wantStatus:  http.StatusOK,
		wantBody:    "data: ok\n\n",
		wantModels:  []string{"primary-model", "primary-model"},
		wantCloses:  1,
		requestBody: `{"model":"primary-model","messages":[],"stream":true}`,
	},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := installFakeOpenCodeClient(t, tt.responses)

			var (
				body   []byte
				status int
				err    error
			)
			if tt.stream {
				var respBody io.ReadCloser
				respBody, status, _, _, err = callOpenCodeAPIStream([]byte(tt.requestBody), "primary-model", UpstreamAuth{Mode: AuthRoutePublic})
				if respBody != nil {
					defer respBody.Close()
				}
				if err == nil {
					body, err = io.ReadAll(respBody)
				}
			} else {
				body, status, _, _, err = callOpenCodeAPI([]byte(tt.requestBody), "primary-model", UpstreamAuth{Mode: AuthRoutePublic})
			}
			if err != nil {
				t.Fatalf("upstream call error = %v", err)
			}
			if status != tt.wantStatus {
				t.Fatalf("upstream call status = %d, want %d", status, tt.wantStatus)
			}
			if string(body) != tt.wantBody {
				t.Fatalf("upstream call body = %q, want %q", string(body), tt.wantBody)
			}
			if !reflect.DeepEqual(transport.requestedModels, tt.wantModels) {
				t.Fatalf("requested models = %#v, want %#v", transport.requestedModels, tt.wantModels)
			}
			if transport.closeIdleCalls != tt.wantCloses {
				t.Fatalf("CloseIdleConnections calls = %d, want %d", transport.closeIdleCalls, tt.wantCloses)
			}
		})
	}
}

func TestCallOpenCodeAPIFallbackKeepsOriginalGoEndpoint(t *testing.T) {
	tests := []struct {
		name   string
		stream bool
	}{
		{name: "non-stream", stream: false},
		{name: "stream", stream: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := installFakeOpenCodeClient(t, []fakeUpstreamResponse{
				{status: http.StatusUnauthorized, body: `{"error":"unauthorized"}`},
				{status: http.StatusOK, body: `{"id":"chatcmpl_test","choices":[]}`},
			})
			modelMu.Lock()
			modelsCache = []ModelInfo{{ID: "shared-model"}}
			goModelsCache = []ModelInfo{{ID: "go-only-model"}, {ID: "shared-model"}}
			modelMu.Unlock()

			auth := UpstreamAuth{Mode: AuthRouteAuto, Token: "sk-validkey0123456789abcdef"}
			body := []byte(`{"model":"go-only-model","messages":[]}`)
			if tt.stream {
				body = []byte(`{"model":"go-only-model","messages":[],"stream":true}`)
				respBody, status, _, _, err := callOpenCodeAPIStream(body, "go-only-model", auth)
				if respBody != nil {
					defer respBody.Close()
				}
				if err != nil {
					t.Fatalf("callOpenCodeAPIStream() error = %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("callOpenCodeAPIStream() status = %d, want %d", status, http.StatusOK)
				}
			} else {
				_, status, _, _, err := callOpenCodeAPI(body, "go-only-model", auth)
				if err != nil {
					t.Fatalf("callOpenCodeAPI() error = %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("callOpenCodeAPI() status = %d, want %d", status, http.StatusOK)
				}
			}

			wantURL := "https://opencode.ai/zen/go/v1/chat/completions"
			if !reflect.DeepEqual(transport.requestedURLs, []string{wantURL, wantURL}) {
				t.Fatalf("requested URLs = %#v, want both requests to %q", transport.requestedURLs, wantURL)
			}
		})
	}
}

func TestCallOpenCodeAPIExhausted4xxReturnsLastUpstreamResponse(t *testing.T) {
	transport := installFakeOpenCodeClient(t, []fakeUpstreamResponse{
		{
			status: http.StatusUnauthorized,
			body:   `{"error":"unauthorized"}`,
			header: http.Header{"X-Upstream-Error": []string{"first"}},
		},
		{
			status: http.StatusForbidden,
			body:   `{"error":"forbidden"}`,
			header: http.Header{"X-Upstream-Error": []string{"last"}},
		},
	})

	body, status, header, _, err := callOpenCodeAPI([]byte(`{"model":"primary-model","messages":[]}`), "primary-model", UpstreamAuth{Mode: AuthRoutePublic})
	if err == nil {
		t.Fatal("callOpenCodeAPI() error = nil, want upstream error")
	}
	if status != http.StatusForbidden {
		t.Fatalf("callOpenCodeAPI() status = %d, want %d", status, http.StatusForbidden)
	}
	if string(body) != `{"error":"forbidden"}` {
		t.Fatalf("callOpenCodeAPI() body = %s, want final upstream body", string(body))
	}
	if header.Get("X-Upstream-Error") != "last" {
		t.Fatalf("final header = %q, want last", header.Get("X-Upstream-Error"))
	}
	// F6: 同模型路由重试，不换候选模型。401 触发一次重试后遇到不可重试 403 结束。
	wantModels := []string{"primary-model", "primary-model"}
	if !reflect.DeepEqual(transport.requestedModels, wantModels) {
		t.Fatalf("requested models = %#v, want %#v", transport.requestedModels, wantModels)
	}
	if transport.closeIdleCalls != 1 {
		t.Fatalf("CloseIdleConnections calls = %d, want 1", transport.closeIdleCalls)
	}
}

func TestBuildOCRequestRoutesSharedAndGoOnlyModelsByAuthMode(t *testing.T) {
	oldModelsCache := modelsCache
	oldGoModelsCache := goModelsCache
	modelMu.Lock()
	modelsCache = []ModelInfo{
		{ID: "glm-5.2"},
		{ID: "gpt-5.5"},
	}
	goModelsCache = []ModelInfo{
		{ID: "glm-5.2"},
		{ID: "kimi-k2.7-code"},
	}
	modelMu.Unlock()
	t.Cleanup(func() {
		modelMu.Lock()
		modelsCache = oldModelsCache
		goModelsCache = oldGoModelsCache
		modelMu.Unlock()
	})

	tests := []struct {
		name    string
		auth    UpstreamAuth
		modelID string
		wantURL string
	}{
		{
			name:    "public stays on zen free surface",
			auth:    UpstreamAuth{Mode: AuthRoutePublic},
			modelID: "deepseek-v4-flash-free",
			wantURL: "https://opencode.ai/zen/v1/chat/completions",
		},
		{
			name:    "bare key keeps shared model on zen",
			auth:    UpstreamAuth{Mode: AuthRouteAuto, Token: "sk-auto"},
			modelID: "glm-5.2",
			wantURL: "https://opencode.ai/zen/v1/chat/completions",
		},
		{
			name:    "go prefix sends shared model to go surface",
			auth:    UpstreamAuth{Mode: AuthRouteGo, Token: "sk-go"},
			modelID: "glm-5.2",
			wantURL: "https://opencode.ai/zen/go/v1/chat/completions",
		},
		{
			name:    "bare key still reaches go only models",
			auth:    UpstreamAuth{Mode: AuthRouteAuto, Token: "sk-auto"},
			modelID: "kimi-k2.7-code",
			wantURL: "https://opencode.ai/zen/go/v1/chat/completions",
		},
		{
			name:    "zen prefix forces zen surface",
			auth:    UpstreamAuth{Mode: AuthRouteZen, Token: "sk-zen"},
			modelID: "glm-5.2",
			wantURL: "https://opencode.ai/zen/v1/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := buildOCRequest(tt.modelID, map[string]any{"messages": []any{}}, tt.auth)
			if err != nil {
				t.Fatalf("buildOCRequest() error = %v", err)
			}
			if got := req.URL.String(); got != tt.wantURL {
				t.Fatalf("buildOCRequest() URL = %q, want %q", got, tt.wantURL)
			}
			wantAuth := "Bearer public"
			if tt.auth.Mode != AuthRoutePublic {
				wantAuth = "Bearer " + tt.auth.Token
			}
			if got := req.Header.Get("Authorization"); got != wantAuth {
				t.Fatalf("buildOCRequest() Authorization = %q, want %q", got, wantAuth)
			}
		})
	}
}

func TestListModelsHandlerSeparatesPublicZenAndGoCatalogs(t *testing.T) {
	oldModelsCache := modelsCache
	oldGoModelsCache := goModelsCache
	oldModelsLoaded := modelsLoaded
	oldModelAlias := modelAlias
	modelMu.Lock()
	modelsCache = []ModelInfo{
		{ID: "deepseek-v4-flash-free"},
		{ID: "glm-5.2"},
		{ID: "gpt-5.5"},
	}
	goModelsCache = []ModelInfo{
		{ID: "glm-5.2"},
		{ID: "kimi-k2.7-code"},
	}
	modelsLoaded = true
	modelMu.Unlock()
	configMu.Lock()
	modelAlias = map[string]string{}
	configMu.Unlock()
	t.Cleanup(func() {
		modelMu.Lock()
		modelsCache = oldModelsCache
		goModelsCache = oldGoModelsCache
		modelsLoaded = oldModelsLoaded
		modelMu.Unlock()
		configMu.Lock()
		modelAlias = oldModelAlias
		configMu.Unlock()
	})

	tests := []struct {
		name       string
		authHeader string
		wantIDs    []string
	}{
		{
			name:    "public only sees free zen models",
			wantIDs: []string{"deepseek-v4-flash-free"},
		},
		{
			name:       "bare zen key sees zen catalog only",
			authHeader: "Bearer sk-auto0123456789abcdef",
			wantIDs:    []string{"deepseek-v4-flash-free", "glm-5.2", "gpt-5.5"},
		},
		{
			name:       "go prefix sees free and go catalog",
			authHeader: "Bearer go:sk-go0123456789abcdef",
			wantIDs:    []string{"deepseek-v4-flash-free", "glm-5.2", "kimi-k2.7-code"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			listModelsHandler(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("listModelsHandler() status = %d, want %d", rec.Code, http.StatusOK)
			}
			var payload struct {
				Data []ModelInfo `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("unmarshal models response: %v", err)
			}
			gotIDs := make([]string, 0, len(payload.Data))
			for _, model := range payload.Data {
				gotIDs = append(gotIDs, model.ID)
			}
			if !reflect.DeepEqual(gotIDs, tt.wantIDs) {
				t.Fatalf("listModelsHandler() ids = %#v, want %#v", gotIDs, tt.wantIDs)
			}
		})
	}
}

func TestListModelsHandlerReplacesMappedModelIDsWithAliases(t *testing.T) {
	oldModelsCache := modelsCache
	oldGoModelsCache := goModelsCache
	oldModelsLoaded := modelsLoaded
	oldModelAlias := modelAlias
	modelMu.Lock()
	modelsCache = []ModelInfo{
		{ID: "deepseek-v4-flash-free", Object: "model", OwnedBy: "opencode"},
		{ID: "gpt-5.5", Object: "model", OwnedBy: "opencode"},
	}
	goModelsCache = nil
	modelsLoaded = true
	modelMu.Unlock()
	configMu.Lock()
	modelAlias = map[string]string{
		"deepseek-v4-flash": "deepseek-v4-flash-free",
	}
	configMu.Unlock()
	t.Cleanup(func() {
		modelMu.Lock()
		modelsCache = oldModelsCache
		goModelsCache = oldGoModelsCache
		modelsLoaded = oldModelsLoaded
		modelMu.Unlock()
		configMu.Lock()
		modelAlias = oldModelAlias
		configMu.Unlock()
	})

	for _, tt := range []struct {
		name       string
		authHeader string
		wantIDs    []string
	}{
		{
			name:    "public sees free alias instead of upstream name",
			wantIDs: []string{"deepseek-v4-flash"},
		},
		{
			name:       "authenticated catalog replaces upstream name",
			authHeader: "Bearer sk-auto0123456789abcdef",
			wantIDs:    []string{"deepseek-v4-flash", "gpt-5.5"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			listModelsHandler(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("listModelsHandler() status = %d, want %d", rec.Code, http.StatusOK)
			}
			var payload struct {
				Data []ModelInfo `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("unmarshal models response: %v", err)
			}
			gotIDs := make([]string, 0, len(payload.Data))
			for _, model := range payload.Data {
				gotIDs = append(gotIDs, model.ID)
			}
			if !reflect.DeepEqual(gotIDs, tt.wantIDs) {
				t.Fatalf("listModelsHandler() ids = %#v, want %#v", gotIDs, tt.wantIDs)
			}
		})
	}
}

func TestExtractUpstreamAuthKeyValidation(t *testing.T) {
	// 本次修复核心：本地门禁密钥（adminPassword）不得作为上游付费 key 透传，
	// 应识别为 public（底层免费通道）。
	const localKey = "sk-localgate0123456789"
	old := adminPassword
	adminPassword = localKey
	t.Cleanup(func() { adminPassword = old })

	tests := []struct {
		name       string
		authHeader string
		wantMode   AuthRouteMode
		wantToken  string
	}{
		{"no header", "", AuthRoutePublic, ""},
		{"bearer empty", "Bearer ", AuthRoutePublic, ""},
		{"bearer public", "Bearer public", AuthRoutePublic, ""},
		{"bearer no-key-required placeholder", "Bearer no-key-required", AuthRoutePublic, ""},
		{"bearer random non-key", "Bearer abc123xyz", AuthRoutePublic, ""},
		{"LOCAL GATE KEY must be public(free)", "Bearer " + localKey, AuthRoutePublic, ""},
		{"go prefix with LOCAL gate key must be public", "Bearer go:" + localKey, AuthRoutePublic, ""},
		{"zen prefix with LOCAL gate key must be public", "Bearer zen:" + localKey, AuthRoutePublic, ""},
		{"valid external sk key", "Bearer sk-validkey0123456789abcdef", AuthRouteAuto, "sk-validkey0123456789abcdef"},
		{"go prefix with external sk key", "Bearer go:sk-gokey0123456789abcdef", AuthRouteGo, "sk-gokey0123456789abcdef"},
		{"zen prefix with external sk key", "Bearer zen:sk-zenkey0123456789abcdef", AuthRouteZen, "sk-zenkey0123456789abcdef"},
		{"go prefix with placeholder falls to public", "Bearer go:no-key-required", AuthRoutePublic, ""},
		{"bare sk- with no suffix is invalid", "Bearer sk-", AuthRoutePublic, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			auth := extractUpstreamAuth(req)
			if auth.Mode != tt.wantMode {
				t.Fatalf("mode = %v, want %v", auth.Mode, tt.wantMode)
			}
			if auth.Token != tt.wantToken {
				t.Fatalf("token = %q, want %q", auth.Token, tt.wantToken)
			}
		})
	}
}

func TestValidAPIKey(t *testing.T) {
	const key = "sk-testkey0123456789"
	old := adminPassword
	adminPassword = key
	t.Cleanup(func() { adminPassword = old })

	tests := []struct {
		name       string
		authHeader string
		want       bool
	}{
		{"no header", "", false},
		{"bearer empty", "Bearer ", false},
		{"bearer public placeholder", "Bearer public", false},
		{"bearer no-key placeholder", "Bearer no-key-required", false},
		{"wrong sk key", "Bearer sk-wrongkey0123456789", false},
		{"correct bare key", "Bearer " + key, true},
		{"go prefix with correct key", "Bearer go:" + key, true},
		{"zen prefix with correct key", "Bearer zen:" + key, true},
		{"go prefix with wrong key", "Bearer go:sk-wrongkey0123456789", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			if got := validAPIKey(req); got != tt.want {
				t.Fatalf("validAPIKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIAuthMiddleware(t *testing.T) {
	const key = "sk-midtestkey0123456"
	old := adminPassword
	t.Cleanup(func() { adminPassword = old })

	// 未设置密钥时放行
	adminPassword = ""
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	apiKeyAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-key mode: status = %d, want 200", rec.Code)
	}

	// 设置密钥后：无头/错头 401，正确头放行
	adminPassword = key
	for _, tt := range []struct {
		name       string
		authHeader string
		want       int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"wrong key", "Bearer sk-wrongkey0123456789", http.StatusUnauthorized},
		{"placeholder key", "Bearer no-key-required", http.StatusUnauthorized},
		{"correct key", "Bearer " + key, http.StatusOK},
		{"correct key with go prefix", "Bearer go:" + key, http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			apiKeyAuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

// ======================== F3 代理池健康检查 ========================

func TestPickHealthyProxySingleProxyNeverSwitches(t *testing.T) {
	proxies := []Socks5Proxy{{Addr: "127.0.0.1:1080"}}
	for i := 0; i < 5; i++ {
		got := pickHealthyProxy(proxies, 0)
		if got.Addr != "127.0.0.1:1080" {
			t.Fatalf("pickHealthyProxy() = %q, want single proxy always", got.Addr)
		}
	}
}

func TestPickHealthyProxySkipsCoolingProxy(t *testing.T) {
	proxies := []Socks5Proxy{
		{Addr: "127.0.0.1:1080"},
		{Addr: "127.0.0.1:1081"},
	}
	socks5HealthMu.Lock()
	socks5Health["127.0.0.1:1080"] = socks5HealthState{failures: 1, until: time.Now().Add(2 * time.Minute)}
	socks5HealthMu.Unlock()
	t.Cleanup(func() {
		socks5HealthMu.Lock()
		delete(socks5Health, "127.0.0.1:1080")
		socks5HealthMu.Unlock()
	})

	// start=0 指向冷却中的 1080，应跳过落到 1081
	got := pickHealthyProxy(proxies, 0)
	if got.Addr != "127.0.0.1:1081" {
		t.Fatalf("pickHealthyProxy() = %q, want healthy 1081", got.Addr)
	}
}

func TestPickHealthyProxyAllCoolingReturnsEarliest(t *testing.T) {
	proxies := []Socks5Proxy{
		{Addr: "127.0.0.1:1080"},
		{Addr: "127.0.0.1:1081"},
	}
	socks5HealthMu.Lock()
	socks5Health["127.0.0.1:1080"] = socks5HealthState{failures: 1, until: time.Now().Add(2 * time.Minute)}
	socks5Health["127.0.0.1:1081"] = socks5HealthState{failures: 1, until: time.Now().Add(30 * time.Second)}
	socks5HealthMu.Unlock()
	t.Cleanup(func() {
		socks5HealthMu.Lock()
		delete(socks5Health, "127.0.0.1:1080")
		delete(socks5Health, "127.0.0.1:1081")
		socks5HealthMu.Unlock()
	})

	// 全冷 → 兜底返回冷却最早结束的 1081
	got := pickHealthyProxy(proxies, 0)
	if got.Addr != "127.0.0.1:1081" {
		t.Fatalf("pickHealthyProxy() = %q, want earliest-ending 1081", got.Addr)
	}
}

func TestMarkSocks5ResultCooldownClassification(t *testing.T) {
	cleanup := func() {
		socks5HealthMu.Lock()
		socks5Health = map[string]socks5HealthState{}
		socks5HealthMu.Unlock()
	}
	t.Cleanup(cleanup)

	// 连接错误 → 20s 冷却
	markSocks5Result("127.0.0.1:1080", 0, io.EOF)
	socks5HealthMu.Lock()
	s := socks5Health["127.0.0.1:1080"]
	socks5HealthMu.Unlock()
	if s.failures != 1 {
		t.Fatalf("failures = %d, want 1", s.failures)
	}
	if until := time.Until(s.until); until < 19*time.Second || until > 21*time.Second {
		t.Fatalf("connection-error cooldown = %v, want ~20s", until)
	}

	// 429 → 45s 冷却
	markSocks5Result("127.0.0.1:1080", http.StatusTooManyRequests, nil)
	socks5HealthMu.Lock()
	s = socks5Health["127.0.0.1:1080"]
	socks5HealthMu.Unlock()
	if s.failures != 2 {
		t.Fatalf("failures = %d, want 2", s.failures)
	}
	if until := time.Until(s.until); until < 44*time.Second || until > 46*time.Second {
		t.Fatalf("429 cooldown = %v, want ~45s", until)
	}

	// 连续 3 次失败 → 2min 冷却
	markSocks5Result("127.0.0.1:1080", http.StatusBadGateway, nil)
	socks5HealthMu.Lock()
	s = socks5Health["127.0.0.1:1080"]
	socks5HealthMu.Unlock()
	if s.failures != 3 {
		t.Fatalf("failures = %d, want 3", s.failures)
	}
	if until := time.Until(s.until); until < 119*time.Second || until > 121*time.Second {
		t.Fatalf("3-failure cooldown = %v, want ~2min", until)
	}

	// 成功请求 → 清除健康记录
	markSocks5Result("127.0.0.1:1080", http.StatusOK, nil)
	socks5HealthMu.Lock()
	_, ok := socks5Health["127.0.0.1:1080"]
	socks5HealthMu.Unlock()
	if ok {
		t.Fatal("socks5Health entry not cleared after success")
	}
}

func TestGetStreamingHTTPClientRemovesTotalTimeout(t *testing.T) {
	socks5Mu.Lock()
	oldActive := activeSocks5
	activeSocks5 = ""
	socks5Mu.Unlock()
	t.Cleanup(func() {
		socks5Mu.Lock()
		activeSocks5 = oldActive
		socks5Mu.Unlock()
	})

	client, _ := getStreamingHTTPClientForTierWithProxy(TierFree)
	if client == nil {
		t.Fatal("streaming client = nil")
	}
	if client.Timeout != 0 {
		t.Fatalf("streaming client Timeout = %v, want 0 (no total limit for SSE)", client.Timeout)
	}
}


// ======================== F5 模型必填校验 ========================

func TestChatCompletionsHandlerRequiresModel(t *testing.T) {
	body := `{"messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	chatCompletionsHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model is required") {
		t.Fatalf("body = %q, want 'model is required'", rec.Body.String())
	}
}

func TestChatCompletionsHandlerWithModelSucceeds(t *testing.T) {
	transport := installFakeOpenCodeClient(t, []fakeUpstreamResponse{
		{status: http.StatusOK, body: `{"id":"chatcmpl_test","choices":[]}`},
	})
	_ = transport
	body := `{"model":"primary-model","messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	chatCompletionsHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestClaudeMessagesHandlerRequiresModel(t *testing.T) {
	body := `{"messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	claudeMessagesHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model is required") {
		t.Fatalf("body = %q, want 'model is required'", rec.Body.String())
	}
}

func TestResponsesHandlerRequiresModel(t *testing.T) {
	body := `{"input":"hi"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	responsesHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model is required") {
		t.Fatalf("body = %q, want 'model is required'", rec.Body.String())
	}
}

