// sign.go — sign-token 鉴权（对齐 参考实现-agent yuanbao.py SignManager）。
//
// POST {domain}/api/v5/robotLogic/sign-token，payload {app_key, nonce,
// timestamp, signature}，其中 signature = HMAC-SHA256(
// nonce+timestamp+app_key+app_secret, key=app_secret) hexdigest；
// timestamp 为北京时区 ISO-8601（无毫秒）。响应 {code, data:{token, bot_id,
// source, duration, product}}，token 缓存至过期（提前 60s 视为过期）。
package yuanbao

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultAPIDomain   = "https://bot.yuanbao.tencent.com"
	tokenPath          = "/api/v5/robotLogic/sign-token"
	signRetryableCode  = 10099
	signMaxRetries     = 3
	signRetryDelay     = 1 * time.Second
	signHTTPTimeout    = 10 * time.Second
	cacheRefreshMargin = 60 * time.Second // 提前视为过期
	defaultTokenTTL    = time.Hour        // duration 缺省时
	beijingOffset      = 8 * time.Hour
)

// computeSignature：HMAC-SHA256(key=app_secret, msg=nonce+timestamp+app_key+app_secret)。
func computeSignature(nonce, timestamp, appKey, appSecret string) string {
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write([]byte(nonce + timestamp + appKey + appSecret))
	return hex.EncodeToString(mac.Sum(nil))
}

// buildSignTimestamp：北京时区 ISO-8601 无毫秒（2006-01-02T15:04:05+08:00）。
func buildSignTimestamp(t time.Time) string {
	return t.In(time.FixedZone("UTC+8", int(beijingOffset.Seconds()))).Format("2006-01-02T15:04:05+08:00")
}

// tokenData 为 sign-token 响应 data。
type tokenData struct {
	Token    string
	BotID    string
	Source   string
	Product  string
	RouteEnv string
	ExpireTS time.Time
}

// signManager 管理 token 获取与缓存（同一 app_key 复用直至过期）。
type signManager struct {
	client *http.Client
	now    func() time.Time
	// sleep 可注入（测试用），默认 ctx 感知的定时等待。
	sleep func(context.Context, time.Duration) error
	// appVersion / botVersion 作为请求头 X-AppVersion / X-Bot-Version。
	appVersion string

	mu    sync.Mutex
	cache map[string]tokenData
}

func newSignManager(appVersion string) *signManager {
	return &signManager{
		client:     &http.Client{Timeout: signHTTPTimeout},
		now:        time.Now,
		sleep:      func(ctx context.Context, d time.Duration) error { return sleepCtx(ctx, d) },
		appVersion: appVersion,
		cache:      map[string]tokenData{},
	}
}

type signTokenResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Token    string `json:"token"`
		BotID    string `json:"bot_id"`
		Source   string `json:"source"`
		Product  string `json:"product"`
		Duration int64  `json:"duration"`
		RouteEnv string `json:"route_env"`
	} `json:"data"`
}

// getToken 返回有效 token；缓存有效（距过期 > margin）时直接复用，否则
// 重新签名。force 为 true 时跳过缓存强制刷新（重连场景）。
func (m *signManager) getToken(ctx context.Context, appKey, appSecret, apiDomain, routeEnv string, force bool) (tokenData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !force {
		if td, ok := m.cache[appKey]; ok && td.ExpireTS.Sub(m.now()) > cacheRefreshMargin {
			return td, nil
		}
	}
	td, err := m.fetch(ctx, appKey, appSecret, apiDomain, routeEnv)
	if err != nil {
		return tokenData{}, err
	}
	m.cache[appKey] = td
	return td, nil
}

// fetch POST sign-token，遇到可重试错误码（10099）最多重试 signMaxRetries 次。
func (m *signManager) fetch(ctx context.Context, appKey, appSecret, apiDomain, routeEnv string) (tokenData, error) {
	url := strings.TrimRight(apiDomain, "/") + tokenPath
	var lastErr error
	for attempt := 0; attempt <= signMaxRetries; attempt++ {
		if attempt > 0 {
			if err := m.sleep(ctx, signRetryDelay); err != nil {
				return tokenData{}, err
			}
		}
		td, err := m.fetchOnce(ctx, url, appKey, appSecret, routeEnv)
		if err == nil {
			return td, nil
		}
		lastErr = err
		var retryable *retryableSignError
		if !asRetryableSign(err, &retryable) {
			return tokenData{}, err
		}
	}
	return tokenData{}, fmt.Errorf("sign token failed after %d retries: %w", signMaxRetries, lastErr)
}

type retryableSignError struct {
	code int
	msg  string
}

func (e *retryableSignError) Error() string {
	return fmt.Sprintf("sign token retryable: code=%d, msg=%s", e.code, e.msg)
}

func asRetryableSign(err error, target **retryableSignError) bool {
	if e, ok := err.(*retryableSignError); ok {
		*target = e
		return true
	}
	return false
}

func (m *signManager) fetchOnce(ctx context.Context, url, appKey, appSecret, routeEnv string) (tokenData, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return tokenData{}, fmt.Errorf("sign token nonce: %w", err)
	}
	nonceStr := hex.EncodeToString(nonce)
	timestamp := buildSignTimestamp(m.now())
	payload := map[string]string{
		"app_key":   appKey,
		"nonce":     nonceStr,
		"signature": computeSignature(nonceStr, timestamp, appKey, appSecret),
		"timestamp": timestamp,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return tokenData{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return tokenData{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AppVersion", m.appVersion)
	req.Header.Set("X-OperationSystem", runtime.GOOS)
	req.Header.Set("X-Instance-Id", fmt.Sprint(instanceID))
	req.Header.Set("X-Bot-Version", m.appVersion)
	if routeEnv != "" {
		req.Header.Set("X-Route-Env", routeEnv)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return tokenData{}, fmt.Errorf("sign token request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenData{}, fmt.Errorf("sign token read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return tokenData{}, fmt.Errorf("sign token API returned %d: %s", resp.StatusCode, truncateBytes(raw, 200))
	}
	var parsed signTokenResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return tokenData{}, fmt.Errorf("sign token response parse error: %w", err)
	}
	switch {
	case parsed.Code == 0:
		now := m.now()
		ttl := defaultTokenTTL
		if parsed.Data.Duration > 0 {
			ttl = time.Duration(parsed.Data.Duration) * time.Second
		}
		return tokenData{
			Token:    parsed.Data.Token,
			BotID:    parsed.Data.BotID,
			Source:   parsed.Data.Source,
			Product:  parsed.Data.Product,
			RouteEnv: parsed.Data.RouteEnv,
			ExpireTS: now.Add(ttl),
		}, nil
	case parsed.Code == signRetryableCode:
		return tokenData{}, &retryableSignError{code: parsed.Code, msg: parsed.Msg}
	default:
		return tokenData{}, fmt.Errorf("sign token error: code=%d, msg=%s", parsed.Code, parsed.Msg)
	}
}

func truncateBytes(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
