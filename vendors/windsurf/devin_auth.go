// Devin/Windsurf 注册链（移植 devin_auth.rs）。
// 流程：邮箱注册 → email_start → 收码 → email_complete → post-auth/bootstrap →
// windsurf/continue 取一次性 code → ExchangeDevinCode 换 windsurf session token。
package windsurf

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/6Kmfi6HP/opencode2api/vendors/windsurf/connect"
)

var (
	devinBase    = "https://app.devin.ai"
	codeiumBase  = "https://server.codeium.com"
	exchangePath = "/exa.seat_management_pb.SeatManagementService/ExchangeDevinCode"
)

var devinUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

// devinClient 是 Devin 站点 HTTP 客户端（带 client uuid cookie 与可选会话态）。
type devinClient struct {
	c          *http.Client
	clientUUID string
	auth1      string
	userID     string
	orgID      string
	orgName    string
}

func newDevinClient(hc *http.Client) *devinClient {
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	if hc.Jar == nil {
		if jar, err := cookiejar.New(nil); err == nil {
			hc.Jar = jar
		}
	}
	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	return &devinClient{c: hc, clientUUID: hex.EncodeToString(uuid)}
}

func (d *devinClient) baseHeaders(referer string) http.Header {
	h := http.Header{}
	h.Set("User-Agent", devinUA)
	h.Set("Origin", devinBase)
	h.Set("Referer", referer)
	h.Set("Accept", "application/json")
	h.Set("Cookie", "devin_client_uuid="+d.clientUUID)
	if d.auth1 != "" {
		h.Set("Authorization", "Bearer "+d.auth1)
	}
	if d.orgID != "" {
		h.Set("x-cog-org-id", d.orgID)
	}
	return h
}

// request 发起一次 Devin 站点请求（method GET/POST，body 可选）。
func (d *devinClient) request(ctx context.Context, method, path string, body map[string]any, referer string) (map[string]any, error) {
	u := devinBase + path
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	h := d.baseHeaders(referer)
	req.Header = h
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s -> %d: %s", path, resp.StatusCode, truncateBytes(raw))
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("%s: non-json %s", path, truncateBytes(raw))
	}
	return v, nil
}

// devinRegistrar 实现 Registrar（Devin/Windsurf 注册链）。
type devinRegistrar struct {
	c *http.Client
}

// NewDevinRegistrar 构造注册器。
func NewDevinRegistrar(hc *http.Client) Registrar {
	return &devinRegistrar{c: hc}
}

// Register 实现 Registrar：完成一次完整注册并返回邮箱 + session token。
func (r *devinRegistrar) Register(ctx context.Context, mb MailboxProvider) (*RegisterResult, error) {
	email, err := mb.Create(ctx)
	if err != nil {
		return nil, err
	}
	d := newDevinClient(r.c)

	// 1) 允许注册检查
	info, err := d.request(ctx, "POST", "/api/auth1/connections", map[string]any{"product": "devin", "email": email}, devinBase+"/auth/signup")
	if err != nil {
		return nil, err
	}
	if blocked, _ := info["blocked_enterprise_signup"].(bool); blocked {
		return nil, fmt.Errorf("blocked_enterprise_signup (%s)", email)
	}

	// 2) 发起邮箱验证
	start, err := d.request(ctx, "POST", "/api/auth1/email/start", map[string]any{"email": email, "mode": "signup"}, devinBase+"/auth/signup")
	if err != nil {
		return nil, err
	}
	verifyToken, _ := start["email_verification_token"].(string)
	if verifyToken == "" {
		return nil, fmt.Errorf("email/start missing token: %v", start)
	}

	// 3) 等待验证码
	code, err := mb.WaitCode(ctx, email, "devin|cognition", 120*time.Second)
	if err != nil {
		return nil, err
	}

	// 4) 完成验证 → auth1 token + user_id
	complete, err := d.request(ctx, "POST", "/api/auth1/email/complete", map[string]any{
		"email_verification_token": verifyToken, "code": code, "mode": "signup",
	}, devinBase+"/auth/signup")
	if err != nil {
		return nil, err
	}
	if s, _ := complete["status"].(string); s == "enterprise_redirect" {
		return nil, fmt.Errorf("enterprise_redirect after complete")
	}
	d.auth1, _ = complete["token"].(string)
	d.userID, _ = complete["user_id"].(string)
	if d.auth1 == "" || d.userID == "" {
		return nil, fmt.Errorf("email/complete missing token/user_id: %v", complete)
	}

	// 5) bootstrap session（post-auth ×2 + attachment cookie）
	if err := d.bootstrap(ctx, email); err != nil {
		return nil, err
	}

	// 6) windsurf/continue 取一次性 code
	code2, err := d.windsurfContinue(ctx)
	if err != nil {
		return nil, err
	}

	// 7) ExchangeDevinCode → session token
	session, err := d.exchangeDevinCode(ctx, code2)
	if err != nil {
		return nil, err
	}

	return &RegisterResult{Email: email, SessionToken: session}, nil
}

func (d *devinClient) bootstrap(ctx context.Context, email string) error {
	if _, err := d.request(ctx, "POST", "/api/users/post-auth", map[string]any{}, devinBase+"/auth/upgrade"); err != nil {
		return err
	}
	local := strings.SplitN(email, "@", 2)[0]
	if local == "" {
		local = "user"
	}
	pa, err := d.request(ctx, "POST", "/api/users/post-auth", map[string]any{"org_name": local}, devinBase+"/auth/upgrade")
	if err != nil {
		return err
	}
	if oid, _ := pa["org_id"].(string); oid != "" {
		d.orgID = oid
	}
	if name, _ := pa["org_name"].(string); name != "" {
		d.orgName = name
	}
	_, _ = d.request(ctx, "POST", "/api/users/set-attachment-cookie", map[string]any{}, devinBase)
	return nil
}

func (d *devinClient) windsurfContinue(ctx context.Context) (string, error) {
	ref := devinBase + "/auth/windsurf/continue"
	_, _ = d.request(ctx, "GET", "/api/auth/windsurf/eligible-organizations", nil, ref)
	_, _ = d.request(ctx, "GET", "/api/auth/windsurf/legacy-enterprise-check", nil, ref)

	data, err := d.request(ctx, "POST", "/api/auth/windsurf/continue", nil, ref)
	if err == nil {
		if c, _ := data["code"].(string); c != "" {
			return c, nil
		}
	}
	data2, err := d.request(ctx, "POST", "/api/auth/windsurf/continue", map[string]any{}, ref)
	if err != nil {
		return "", err
	}
	c, _ := data2["code"].(string)
	if c == "" {
		return "", fmt.Errorf("windsurf/continue missing code: %v", data2)
	}
	return c, nil
}

func (d *devinClient) exchangeDevinCode(ctx context.Context, devinCode string) (string, error) {
	body := connect.EncodeString(1, devinCode)
	url := codeiumBase + exchangePath
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("User-Agent", "connect-es/1.6.1")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := d.c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ExchangeDevinCode HTTP %d: %s", resp.StatusCode, truncateBytes(raw))
	}
	parsed, err := connect.ParseTop(raw)
	if err != nil {
		return "", err
	}
	session := parsed.FirstString(1)
	if !strings.HasPrefix(session, "devin-session-token$") {
		return "", fmt.Errorf("unexpected session token: %s", truncate(session, 40))
	}
	return session, nil
}

func truncateBytes(b []byte) string {
	s := string(b)
	if len(s) > 240 {
		return s[:240]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

var _ Registrar = (*devinRegistrar)(nil)
