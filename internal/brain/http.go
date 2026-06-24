package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// httpBrain calls the OpenAI or Anthropic chat APIs directly over HTTP. Using
// raw HTTP (rather than an SDK) keeps dependencies minimal and insulates us
// from SDK churn; the request shape for both providers is small and stable.
type httpBrain struct {
	provider Provider
	model    string
	apiKey   string
	client   *http.Client
}

func defaultClient() *http.Client { return &http.Client{Timeout: 120 * time.Second} }

func (b *httpBrain) Available() bool { return true }

func (b *httpBrain) ExpandSpec(ctx context.Context, raw string) (string, error) {
	out, err := b.chat(ctx, expandSystemPrompt, raw, 4096)
	if err != nil {
		// Fail soft: a planning prompt is better than none.
		return wrapRawSpec(raw), err
	}
	return out, nil
}

func (b *httpBrain) TriageIdea(ctx context.Context, title, body string) (Triage, error) {
	user := "Title: " + title + "\n\nBody: " + body
	out, err := b.chat(ctx, triageSystemPrompt, user, 256)
	if err != nil {
		return Triage{Priority: 3, Rationale: "triage failed: " + err.Error()}, err
	}
	return parseTriage(out)
}

func (b *httpBrain) SplitPlan(ctx context.Context, plan string) ([]SubTask, error) {
	out, err := b.chat(ctx, splitSystemPrompt, plan, 4096)
	if err != nil {
		// Fail soft: build the whole plan as a single sub-task rather than nothing.
		return []SubTask{singleSubTask(plan)}, err
	}
	subs, perr := parseSplit(out)
	if perr != nil {
		return []SubTask{singleSubTask(plan)}, perr
	}
	// Frame each self-contained slice with the agent operating context, so
	// callers can launch SubTask.Prompt verbatim.
	for i := range subs {
		subs[i].Prompt = wrapBuildPrompt(subs[i].Prompt)
	}
	return subs, nil
}

// chat dispatches to the configured provider and returns the assistant text.
func (b *httpBrain) chat(ctx context.Context, system, user string, maxTokens int) (string, error) {
	switch b.provider {
	case Anthropic:
		return b.chatAnthropic(ctx, system, user, maxTokens)
	default:
		return b.chatOpenAI(ctx, system, user, maxTokens)
	}
}

func (b *httpBrain) chatOpenAI(ctx context.Context, system, user string, maxTokens int) (string, error) {
	reqBody := map[string]any{
		"model": b.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_tokens": maxTokens,
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := b.do(ctx, "https://api.openai.com/v1/chat/completions",
		map[string]string{"Authorization": "Bearer " + b.apiKey}, reqBody, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("openai: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("openai: empty response")
	}
	return resp.Choices[0].Message.Content, nil
}

func (b *httpBrain) chatAnthropic(ctx context.Context, system, user string, maxTokens int) (string, error) {
	reqBody := map[string]any{
		"model":      b.model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages": []map[string]string{
			{"role": "user", "content": user},
		},
	}
	var resp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := b.do(ctx, "https://api.anthropic.com/v1/messages",
		map[string]string{
			"x-api-key":         b.apiKey,
			"anthropic-version": "2023-06-01",
		}, reqBody, &resp); err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("anthropic: %s", resp.Error.Message)
	}
	if len(resp.Content) == 0 {
		return "", fmt.Errorf("anthropic: empty response")
	}
	return resp.Content[0].Text, nil
}

// do performs a JSON POST and decodes the response into out.
func (b *httpBrain) do(ctx context.Context, url string, headers map[string]string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		// Still attempt to decode for the structured error message.
		_ = json.Unmarshal(data, out)
		if len(data) > 300 {
			data = data[:300]
		}
		return fmt.Errorf("%s: http %d: %s", b.provider, resp.StatusCode, string(data))
	}
	return json.Unmarshal(data, out)
}
