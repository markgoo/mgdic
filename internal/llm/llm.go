// Package llm 只干一件事：句子的整句翻译。词和词组永远走本地词典，不联网。
package llm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config 是 DeepSeek / OpenAI 兼容接口的三件套。
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	Timeout time.Duration
}

// Client 是一个极小的 chat/completions 客户端，够用一个方向翻译就够。
type Client struct {
	cfg  Config
	http *http.Client
}

// New 没有 key 就返回 nil：没有 key 时句子翻译降级成"逐词给结果"，不报错。
func New(cfg Config) *Client {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 8 * time.Second
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.deepseek.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "deepseek-chat"
	}
	return &Client{cfg: cfg, http: &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        4,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			MaxConnsPerHost:     2,
		},
	}}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Translate 把 text 翻到 to 语言（"中文" 或 "英文"）。
// 温度 0、只要译文，避免模型开始讲解语法。
func (c *Client) Translate(ctx context.Context, text, to string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("没有配置 api_key")
	}
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	body := chatRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: "你是专业翻译引擎。把用户文本翻译成" + to + "。" +
				"只输出译文：不要解释，不要加引号，不要补充说明，不要列多个候选。"},
			{Role: "user", Content: text},
		},
		Temperature: 0,
		MaxTokens:   512,
		Stream:      false,
	}
	have, _ := json.Marshal(body)
	url := strings.TrimSuffix(c.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(have))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var got chatResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		return "", fmt.Errorf("接口返回不是 JSON（HTTP %d）: %.120s", resp.StatusCode, string(raw))
	}
	if got.Error != nil {
		return "", fmt.Errorf("接口报错 %d: %s", resp.StatusCode, got.Error.Message)
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("接口报错 %d: %.200s", resp.StatusCode, string(raw))
	}
	if len(got.Choices) == 0 {
		return "", fmt.Errorf("接口没给译文")
	}
	return strings.TrimSpace(got.Choices[0].Message.Content), nil
}
