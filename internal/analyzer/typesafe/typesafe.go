// Package typesafe is the HTTP adapter for TypeSafe's System One endpoint.
// It owns transport, retries, response validation, and normalization into
// router.Assessment. No vendor type leaves this package.
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/frost/internal/analyzer"
	"github.com/marcus/frost/internal/router"
)

const (
	defaultBaseURL     = "https://api.typesafe.ai"
	defaultTimeout     = 45 * time.Second
	defaultMaxAttempts = 3
	maxBody            = 4 * 1024 * 1024
	providerName       = "typesafe"
)

// Options configures a Client.
type Options struct {
	BaseURL     string
	APIKey      string
	Model       string
	Spec        *analyzer.Spec
	Timeout     time.Duration
	HTTPClient  *http.Client
	MaxAttempts int
	Sleep       func(time.Duration)
}

// Client calls TypeSafe.
type Client struct {
	opt Options
}

var _ analyzer.Analyzer = (*Client)(nil)

// New validates options and applies defaults.
func New(opt Options) (*Client, error) {
	if opt.APIKey == "" {
		return nil, errors.New("typesafe: API key is required")
	}
	if opt.Model == "" {
		return nil, errors.New("typesafe: model is required")
	}
	if opt.Spec == nil {
		return nil, errors.New("typesafe: question spec is required")
	}
	if opt.BaseURL == "" {
		opt.BaseURL = defaultBaseURL
	}
	opt.BaseURL = strings.TrimRight(opt.BaseURL, "/")
	if opt.Timeout <= 0 {
		opt.Timeout = defaultTimeout
	}
	if opt.MaxAttempts <= 0 {
		opt.MaxAttempts = defaultMaxAttempts
	}
	if opt.Sleep == nil {
		opt.Sleep = time.Sleep
	}
	if opt.HTTPClient == nil {
		opt.HTTPClient = &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &Client{opt: opt}, nil
}

// Analyze implements analyzer.Analyzer.
func (c *Client) Analyze(ctx context.Context, task router.Task) (router.Assessment, error) {
	a, _, err := c.AnalyzeRaw(ctx, task)
	return a, err
}

// AnalyzeRaw analyzes the task and also returns the raw response body so a
// recorder can keep the exact provider output.
func (c *Client) AnalyzeRaw(ctx context.Context, task router.Task) (router.Assessment, json.RawMessage, error) {
	state := map[string]string{"task": task.Text}
	if task.Context != "" {
		state["context"] = task.Context
	}
	body, err := json.Marshal(map[string]any{
		"model":     c.opt.Model,
		"state":     state,
		"questions": c.opt.Spec.Questions,
	})
	if err != nil {
		return router.Assessment{}, nil, err
	}
	started := time.Now()
	raw, err := c.do(ctx, http.MethodPost, "/v1/systemone", body)
	if err != nil {
		return router.Assessment{}, nil, err
	}
	latency := time.Since(started).Milliseconds()

	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return router.Assessment{}, nil, fmt.Errorf("typesafe: decode response: %w", err)
	}
	a, err := normalize(resp, c.opt.Spec)
	if err != nil {
		return router.Assessment{}, nil, err
	}
	a.RequestedModel = c.opt.Model
	a.LatencyMS = latency
	return a, json.RawMessage(raw), nil
}

// ListModels returns the model names the account can use. The response shape
// is undocumented, so parsing is tolerant.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	raw, err := c.do(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	names := collectModelNames(raw)
	if names == nil {
		return nil, errors.New("typesafe: could not find model names in /v1/models response")
	}
	return names, nil
}

// do performs one logical request with bounded retries. Only 429 and 529 are
// retried up to MaxAttempts; other 5xx once; 4xx never.
func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= c.opt.MaxAttempts; attempt++ {
		raw, retryable, err := c.once(ctx, method, path, body)
		if err == nil {
			return raw, nil
		}
		lastErr = err
		if !retryable || ctx.Err() != nil {
			return nil, err
		}
		var pe *analyzer.ProviderError
		if errors.As(err, &pe) && pe.Status >= 500 && pe.Status != 529 && attempt >= 2 {
			return nil, err
		}
		if attempt == c.opt.MaxAttempts {
			break
		}
		delay := time.Duration(math.Pow(2, float64(attempt-1))) * 500 * time.Millisecond
		if delay > 8*time.Second {
			delay = 8 * time.Second
		}
		c.opt.Sleep(delay)
	}
	return nil, lastErr
}

func (c *Client) once(ctx context.Context, method, path string, body []byte) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opt.Timeout)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.opt.BaseURL+path, rdr)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.opt.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.opt.HTTPClient.Do(req)
	if err != nil {
		// Transport failures are retryable unless the context is done.
		return nil, ctx.Err() == nil, fmt.Errorf("typesafe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, true, fmt.Errorf("typesafe: read response: %w", err)
	}
	if len(raw) > maxBody {
		return nil, false, fmt.Errorf("typesafe: response exceeds %d bytes", maxBody)
	}
	if resp.StatusCode == http.StatusOK {
		return raw, false, nil
	}
	pe := &analyzer.ProviderError{Status: resp.StatusCode}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == 529:
		pe.Retryable = true
	case resp.StatusCode >= 500:
		pe.Retryable = true
	case resp.StatusCode == http.StatusUnprocessableEntity:
		pe.Detail = strings.TrimSpace(string(raw))
	}
	return nil, pe.Retryable, pe
}

// Vendor response types. They never leave this package.
type response struct {
	Model   string            `json:"model"`
	Answers map[string]answer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type answer struct {
	Type          string             `json:"type"`
	Score         *float64           `json:"score,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}

func normalize(r response, spec *analyzer.Spec) (router.Assessment, error) {
	if r.Model == "" {
		return router.Assessment{}, errors.New("typesafe: response missing model")
	}
	a := router.Assessment{
		Provider:         providerName,
		ReturnedModel:    r.Model,
		QuestionsVersion: spec.Version,
		QuestionsHash:    spec.Hash,
		Scores:           map[string]router.ScoreAnswer{},
		Nouls:            map[string]float64{},
		Choices:          map[string]router.ChoiceAnswer{},
		Usage:            router.Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens},
	}
	for id, q := range spec.Questions {
		ans, ok := r.Answers[id]
		if !ok {
			return router.Assessment{}, fmt.Errorf("typesafe: missing answer %s", id)
		}
		if ans.Type != q.Type {
			return router.Assessment{}, fmt.Errorf("typesafe: answer %s has type %q, want %q", id, ans.Type, q.Type)
		}
		switch q.Type {
		case "noul":
			if ans.Noul == nil || !finiteRange(*ans.Noul, 0, 1) {
				return router.Assessment{}, fmt.Errorf("typesafe: invalid noul %s", id)
			}
			a.Nouls[id] = *ans.Noul
		case "score":
			levels, err := spec.ScoreLevels(id)
			if err != nil {
				return router.Assessment{}, err
			}
			if ans.Confidence == nil || !finiteRange(*ans.Confidence, 0, 1) {
				return router.Assessment{}, fmt.Errorf("typesafe: invalid confidence %s", id)
			}
			if ans.Score == nil || !finiteRange(*ans.Score, 0, float64(len(levels)-1)) {
				return router.Assessment{}, fmt.Errorf("typesafe: invalid score %s", id)
			}
			probs := make([]float64, len(levels))
			if len(ans.Probabilities) != len(levels) {
				return router.Assessment{}, fmt.Errorf("typesafe: incomplete distribution %s", id)
			}
			sum := 0.0
			for k, p := range ans.Probabilities {
				i, err := strconv.Atoi(k)
				if err != nil || i < 0 || i >= len(levels) || !finiteRange(p, 0, 1) {
					return router.Assessment{}, fmt.Errorf("typesafe: invalid probabilities %s", id)
				}
				probs[i] = p
				sum += p
			}
			if math.Abs(sum-1) > 0.02 {
				return router.Assessment{}, fmt.Errorf("typesafe: distribution %s sums to %.3f", id, sum)
			}
			legend := make([]string, len(levels))
			for i := range levels {
				if l, ok := ans.Legend[strconv.Itoa(i)]; ok {
					legend[i] = l
				} else {
					legend[i] = levels[i]
				}
			}
			a.Scores[id] = router.ScoreAnswer{Mean: *ans.Score, Probabilities: probs, Confidence: *ans.Confidence, Legend: legend}
		case "choice":
			opts := spec.ChoiceOptions(id)
			if ans.Confidence == nil || !finiteRange(*ans.Confidence, 0, 1) {
				return router.Assessment{}, fmt.Errorf("typesafe: invalid confidence %s", id)
			}
			if !contains(opts, ans.Choice) {
				return router.Assessment{}, fmt.Errorf("typesafe: invalid choice %s", id)
			}
			if len(ans.Probabilities) != len(opts) {
				return router.Assessment{}, fmt.Errorf("typesafe: incomplete distribution %s", id)
			}
			sum := 0.0
			probs := make(map[string]float64, len(opts))
			for k, p := range ans.Probabilities {
				if !contains(opts, k) || !finiteRange(p, 0, 1) {
					return router.Assessment{}, fmt.Errorf("typesafe: invalid probabilities %s", id)
				}
				probs[k] = p
				sum += p
			}
			if math.Abs(sum-1) > 0.02 {
				return router.Assessment{}, fmt.Errorf("typesafe: distribution %s sums to %.3f", id, sum)
			}
			a.Choices[id] = router.ChoiceAnswer{Choice: ans.Choice, Probabilities: probs, Confidence: *ans.Confidence}
		}
	}
	return a, nil
}

func finiteRange(x, lo, hi float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && x >= lo && x <= hi
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// collectModelNames pulls name or id fields out of any of the plausible
// list shapes. It returns nil when no list is recognizable.
func collectModelNames(raw []byte) []string {
	var top any
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil
	}
	var list []any
	switch v := top.(type) {
	case []any:
		list = v
	case map[string]any:
		for _, key := range []string{"models", "data", "items"} {
			if l, ok := v[key].([]any); ok {
				list = l
				break
			}
		}
	}
	if list == nil {
		return nil
	}
	names := []string{}
	for _, item := range list {
		switch it := item.(type) {
		case string:
			names = append(names, it)
		case map[string]any:
			for _, key := range []string{"name", "id", "model"} {
				if s, ok := it[key].(string); ok && s != "" {
					names = append(names, s)
					break
				}
			}
		}
	}
	sort.Strings(names)
	return names
}
