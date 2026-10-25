// Package provider 封装对上游的转发。Veyra 是透传优先的网关：
// 各协议入口（chat/messages/responses/gemini/systemone）都不改写协议体
// （OpenAI chat 只动 model 与 stream_options 两个字段；Gemini 连 body 都不动，
// model 在 URL 里）。网关不做跨协议转换——协议对了才透传，不对就报错。
//
// 与 Bifrost 上游的对应关系见 docs/upstream-mapping.md（M2 起维护）：
// 本包概念上对应 core/providers/*，新增 provider 时按上游同名包 port。
package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 是共享的上游 HTTP 客户端。连接池全局复用，
// ResponseHeaderTimeout 60s 容纳推理模型的长首字节；整体 Timeout 必须为 0——
// SSE 长流的耗时由 idle 的 chunk 间隔决定，任何整体超时都会掐断正常会话。
type Client struct {
	hc *http.Client
}

func NewClient() *Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	return &Client{hc: &http.Client{
		Transport: tr,
		Timeout:   0, // 整体超时必须为 0：SSE 长流靠 chunk 保活，见类型注释
	}}
}

// TrimBaseURL 归一化存储格式：去尾斜杠。
// 约定：base_url 与 OpenAI SDK 的 base_url 完全一致（通常含 /v1），
// 例如 https://api.openai.com/v1、https://openrouter.ai/api/v1、
// https://api.typesafe.ai/v1。
func TrimBaseURL(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

// ChatURL 拼上游 chat/completions 地址。
func ChatURL(baseURL string) string { return TrimBaseURL(baseURL) + "/chat/completions" }

// SystemOneURL 拼上游 /v1/systemone 地址（TypeSafe Jev 协议）。
func SystemOneURL(baseURL string) string { return TrimBaseURL(baseURL) + "/systemone" }

// ModelsURL 拼上游模型列表地址。
func ModelsURL(baseURL string) string { return TrimBaseURL(baseURL) + "/models" }

// MessagesURL 拼上游 Anthropic Messages 地址。base_url 约定含 /v1：
// https://api.anthropic.com/v1、https://api.deepseek.com/anthropic/v1 等。
func MessagesURL(baseURL string) string { return TrimBaseURL(baseURL) + "/messages" }

// ResponsesURL 拼上游 OpenAI Responses 地址（base_url 约定含 /v1）。
func ResponsesURL(baseURL string) string { return TrimBaseURL(baseURL) + "/responses" }

// GeminiURL 拼上游 Gemini 原生地址。base_url 约定含版本段：
// https://generativelanguage.googleapis.com/v1beta。model 在 URL 路径里
// （不在请求体），method 为 generateContent / streamGenerateContent 等，
// 任意方法名透传。
func GeminiURL(baseURL, model, method string) string {
	return TrimBaseURL(baseURL) + "/models/" + model + ":" + method
}

// Do 发 JSON POST 并带上游 Bearer key（OpenAI 生态认证风格）。
// 失败分类：网络层错误（DNS/连接/超时）返回 error，上游已应答则原样返回
// *http.Response 由调用方决定如何透传——网关不吞上游错误。
func (c *Client) Do(ctx context.Context, method, url, apiKey string, body []byte) (*http.Response, error) {
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	return c.DoWithHeaders(ctx, method, url, headers, body)
}

// DoWithHeaders 发 JSON 请求，headers 由调用方完全给出。
// 引入原因：认证风格因上游而异——OpenAI 生态 Bearer、Anthropic 生态
// x-api-key + anthropic-version、Gemini 生态 x-goog-api-key。
// Content-Type 统一在此设置，调用方只管认证与协议头。
func (c *Client) DoWithHeaders(ctx context.Context, method, url string, headers map[string]string, body []byte) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, fmt.Errorf("构造上游请求 %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求上游 %s: %w", url, err)
	}
	return resp, nil
}
