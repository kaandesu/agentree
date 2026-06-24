package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

func (b *httpBrain) Suggest(ctx context.Context, situations []string) ([]string, error) {
	if len(situations) == 0 {
		return situations, nil
	}
	in, _ := json.Marshal(situations)
	out, err := b.chat(ctx, suggestSystemPrompt, string(in), 1024)
	if err != nil {
		return situations, err // fail soft: keep deterministic phrasing
	}
	refined, perr := parseStringArray(out)
	if perr != nil || len(refined) != len(situations) {
		return situations, perr
	}
	return refined, nil
}

func (b *httpBrain) PlanChat(ctx context.Context, messages []ChatMessage) (PlanChatResponse, error) {
	// Build the messages array: system prompt + conversation history.
	msgs := make([]map[string]string, 0, len(messages)+1)
	msgs = append(msgs, map[string]string{"role": "system", "content": planChatSystemPrompt})
	for _, m := range messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}

	reqBody := map[string]any{
		"model":      b.model,
		"messages":   msgs,
		"tools":      []any{proposePlanToolDef},
		"max_tokens": 4096,
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := b.do(ctx, "https://api.openai.com/v1/chat/completions",
		map[string]string{"Authorization": "Bearer " + b.apiKey}, reqBody, &resp); err != nil {
		return PlanChatResponse{}, err
	}
	if resp.Error != nil {
		return PlanChatResponse{}, fmt.Errorf("openai: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return PlanChatResponse{}, fmt.Errorf("openai: empty response")
	}

	choice := resp.Choices[0].Message

	// Check for tool call (propose_plan).
	for _, tc := range choice.ToolCalls {
		if tc.Function.Name == "propose_plan" {
			proposal, err := parsePlanProposal(tc.Function.Arguments)
			if err != nil {
				return PlanChatResponse{}, fmt.Errorf("failed to parse propose_plan: %w", err)
			}
			// Wrap each sub-task prompt with agent operating context.
			for i := range proposal.Features {
				for j := range proposal.Features[i].SubTasks {
					proposal.Features[i].SubTasks[j].Prompt = wrapBuildPrompt(proposal.Features[i].SubTasks[j].Prompt)
				}
			}
			return PlanChatResponse{Proposal: &proposal}, nil
		}
	}

	// Text response (interrogation).
	text := ""
	if choice.Content != nil {
		text = *choice.Content
	}
	return PlanChatResponse{Text: text}, nil
}

// parsePlanProposal extracts a PlanProposal from the function call arguments.
func parsePlanProposal(raw string) (PlanProposal, error) {
	var p PlanProposal
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return PlanProposal{}, err
	}
	// Drop empty features/sub-tasks.
	var features []Feature
	for _, f := range p.Features {
		var subs []SubTask
		for _, st := range f.SubTasks {
			st.Title = strings.TrimSpace(st.Title)
			st.Prompt = strings.TrimSpace(st.Prompt)
			if st.Prompt != "" {
				if st.Title == "" {
					st.Title = firstNonEmptyLine(st.Prompt)
				}
				subs = append(subs, st)
			}
		}
		if len(subs) > 0 {
			f.SubTasks = subs
			f.Title = strings.TrimSpace(f.Title)
			if f.Title == "" {
				f.Title = subs[0].Title
			}
			features = append(features, f)
		}
	}
	if len(features) == 0 {
		return PlanProposal{}, fmt.Errorf("proposal contained no usable features")
	}
	p.Features = features
	return p, nil
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
