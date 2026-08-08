// GetUserStatus 用量回写（移植 usage.rs）。
// POST server.codeium.com/exa.seat_management_pb.SeatManagementService/GetUserStatus，
// 响应为 protobuf（可能 Connect 帧/gzip），用 connect.ExtractUsageFromUserStatus 解析。
package windsurf

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/6Kmfi6HP/opencode2api/vendors/windsurf/connect"
)

const getUserStatusPath = "/exa.seat_management_pb.SeatManagementService/GetUserStatus"

// buildGetUserStatusBody 构造 GetUserStatus 请求体（client metadata + token）。
func buildGetUserStatusBody(sessionToken string) []byte {
	var meta []byte
	for _, kv := range []struct {
		f uint32
		v string
	}{
		{1, "windsurf"}, {2, "1.48.2"}, {3, sessionToken}, {4, "en"},
		{5, `{"Os":"windows","Arch":"amd64","Version":"10.0","ProductName":"Windows"}`},
		{7, "3.5.17"}, {12, "windsurf"}, {30, "Free"},
	} {
		meta = append(meta, connect.EncodeString(kv.f, kv.v)...)
	}
	return connect.EncodeBytes(1, meta)
}

// fetchUsage 拉取并解析一个账号的用量（daily/weekly 剩余百分比；失败返回错误）。
func fetchUsage(ctx context.Context, sessionToken string, hc *http.Client) (daily, weekly float64, err error) {
	if !strings.HasPrefix(sessionToken, "devin-session-token$") {
		return 0, 0, fmt.Errorf("session token 格式无效")
	}
	body := buildGetUserStatusBody(sessionToken)
	url := codeiumBase + getUserStatusPath
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("User-Agent", "connect-go/1.18.1 (go1.26.4)")
	req.Header.Set("Accept-Encoding", "identity")

	resp, err := hc.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, 0, fmt.Errorf("GetUserStatus HTTP %d: %s", resp.StatusCode, truncateBytes(raw))
	}
	// 解 Connect 帧 / gzip
	unwrapped := unwrapProto(raw)
	fields := connect.ExtractUsageFromUserStatus(unwrapped)
	if !fields.HasDailyPct && !fields.HasWeeklyPct {
		return 0, 0, fmt.Errorf("parsed empty usage (%d bytes)", len(unwrapped))
	}
	return float64(fields.DailyRemainingPct), float64(fields.WeeklyRemainingPct), nil
}

// unwrapProto 去除 Connect 帧头 / gzip（getUserStatus 响应两种形态都兼容）。
func unwrapProto(data []byte) []byte {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		if out, err := gunzipBytes(data); err == nil {
			return out
		}
		return data
	}
	if len(data) >= 5 && data[0] < 0x10 {
		size := int(data[1])<<24 | int(data[2])<<16 | int(data[3])<<8 | int(data[4])
		if len(data) >= 5+size {
			chunk := data[5 : 5+size]
			if len(chunk) >= 2 && chunk[0] == 0x1f && chunk[1] == 0x8b {
				if out, err := gunzipBytes(chunk); err == nil {
					return out
				}
			}
			return chunk
		}
	}
	return data
}

func gunzipBytes(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// refreshPoolUsage 异步刷新全部账号额度并回写（P3-B6 挂钩）。
func (v *Vendor) refreshPoolUsage() {
	v.pool.mu.Lock()
	accounts := append([]*Account(nil), v.pool.accounts...)
	v.pool.mu.Unlock()
	for _, a := range accounts {
		if a.WindsurfSessionToken == "" {
			continue
		}
		token := a.WindsurfSessionToken
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			daily, weekly, err := fetchUsage(ctx, token, v.cfg.HTTPClient)
			if err != nil {
				slog.Debug("windsurf: usage refresh failed", "err", err)
				return
			}
			v.SetPoolUsage(a.Email, daily, weekly)
			slog.Debug("windsurf: usage refreshed", "email", maskEmail(a.Email), "daily", daily, "weekly", weekly)
		}()
	}
}

var _ = json.Marshal
