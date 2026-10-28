package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL = "https://api.flow.microsoft.com"
	apiVersion     = "2016-11-01"
	maxAttempts    = 6
	runConcurrency = 4
)

// Action is a trigger or flow action recorded on one run.
type Action struct {
	Name   string
	Status string
	Input  json.RawMessage
	Output json.RawMessage
	Start  time.Time
	End    time.Time
}

// Run is one cloud flow run with its trigger and actions.
type Run struct {
	Name    string
	Start   time.Time
	End     time.Time
	Status  string
	Error   json.RawMessage
	Trigger Action
	Actions []Action
}

// Client calls the Power Automate Flow Service API.
type Client struct {
	baseURL    string
	token      string
	asAdmin    bool
	httpClient *http.Client
	sleep      func(time.Duration)
}

func NewClient(token string, asAdmin bool) *Client {
	return &Client{
		baseURL: defaultBaseURL,
		token:   token,
		asAdmin: asAdmin,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// FetchActionNames reads the flow definition and returns action names in portal column order.
func (c *Client) FetchActionNames(ctx context.Context, environment, flowID string) ([]string, error) {
	body, err := c.get(ctx, c.resourceURL(environment, flowID)+"?api-version="+apiVersion)
	if err != nil {
		return nil, err
	}
	return ActionNames(body)
}

// Runs lists every run of status, optionally bounded by start time, and loads actions.
// since and until are inclusive and exclusive RFC3339 bounds. limit 0 means no cap.
func (c *Client) Runs(ctx context.Context, environment, flowID, status string, since, until *time.Time, limit int) ([]Run, error) {
	filter := RunFilter(status, formatBound(since), formatBound(until))
	page := c.runsURL(environment, flowID, filter)
	var summaries []runItem
	seen := map[string]struct{}{}
	for page != "" {
		if _, ok := seen[page]; ok {
			break
		}
		seen[page] = struct{}{}
		body, err := c.get(ctx, page)
		if err != nil {
			return nil, err
		}
		var envelope listEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("decode runs: %w", err)
		}
		summaries = append(summaries, envelope.Value...)
		if limit > 0 && len(summaries) >= limit {
			summaries = summaries[:limit]
			break
		}
		if envelope.NextLink == "" {
			break
		}
		page = resolve(c.baseURL, envelope.NextLink)
	}
	return c.hydrate(ctx, environment, flowID, summaries)
}

func formatBound(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (c *Client) hydrate(ctx context.Context, environment, flowID string, items []runItem) ([]Run, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	out := make([]Run, len(items))
	sem := make(chan struct{}, runConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error

	for i := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			run, err := c.loadRun(ctx, environment, flowID, items[i])
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if first == nil {
					first = err
					cancel()
				}
				return
			}
			out[i] = run
		}(i)
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}
	return out, nil
}

func (c *Client) loadRun(ctx context.Context, environment, flowID string, item runItem) (Run, error) {
	props, err := decodeRunProps(item.Properties)
	if err != nil {
		return Run{}, fmt.Errorf("run %s: %w", item.Name, err)
	}
	if props.Trigger == nil {
		body, err := c.get(ctx, c.resourceURL(environment, flowID, "runs", item.Name)+"?api-version="+apiVersion)
		if err != nil {
			return Run{}, err
		}
		var single struct {
			Properties json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(body, &single); err != nil {
			return Run{}, fmt.Errorf("decode run %s: %w", item.Name, err)
		}
		props, err = decodeRunProps(single.Properties)
		if err != nil {
			return Run{}, fmt.Errorf("run %s: %w", item.Name, err)
		}
	}

	actions, err := c.listActions(ctx, environment, flowID, item.Name)
	if err != nil {
		return Run{}, err
	}

	trigger := Action{Name: "Trigger"}
	if props.Trigger != nil {
		trigger, err = c.materialize(ctx, "Trigger", *props.Trigger)
		if err != nil {
			return Run{}, err
		}
	}
	loaded := make([]Action, len(actions))
	for i, action := range actions {
		loaded[i], err = c.materialize(ctx, action.name, action.props)
		if err != nil {
			return Run{}, err
		}
	}
	return Run{
		Name:    item.Name,
		Start:   props.StartTime.Time,
		End:     props.EndTime.Time,
		Status:  props.Status,
		Error:   props.Error,
		Trigger: trigger,
		Actions: loaded,
	}, nil
}

func (c *Client) listActions(ctx context.Context, environment, flowID, runName string) ([]namedAction, error) {
	page := c.resourceURL(environment, flowID, "runs", runName, "actions") + "?api-version=" + apiVersion
	var actions []namedAction
	seen := map[string]struct{}{}
	for page != "" {
		if _, ok := seen[page]; ok {
			break
		}
		seen[page] = struct{}{}
		body, err := c.get(ctx, page)
		if err != nil {
			return nil, err
		}
		var envelope actionEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, fmt.Errorf("decode actions for %s: %w", runName, err)
		}
		for _, item := range envelope.Value {
			var props actionProps
			if len(item.Properties) > 0 {
				if err := json.Unmarshal(item.Properties, &props); err != nil {
					return nil, fmt.Errorf("decode action %s: %w", item.Name, err)
				}
			}
			actions = append(actions, namedAction{name: item.Name, props: props})
		}
		if envelope.NextLink == "" {
			break
		}
		page = resolve(c.baseURL, envelope.NextLink)
	}
	return actions, nil
}

func (c *Client) materialize(ctx context.Context, name string, props actionProps) (Action, error) {
	input, err := c.loadPayload(ctx, props.Inputs, props.InputsLink)
	if err != nil {
		return Action{}, err
	}
	output, err := c.loadPayload(ctx, props.Outputs, props.OutputsLink)
	if err != nil {
		return Action{}, err
	}
	return Action{
		Name:   name,
		Status: props.Status,
		Input:  input,
		Output: output,
		Start:  props.StartTime.Time,
		End:    props.EndTime.Time,
	}, nil
}

func (c *Client) loadPayload(ctx context.Context, inline json.RawMessage, link *contentLink) (json.RawMessage, error) {
	if !absent(inline) {
		return inline, nil
	}
	if link == nil || strings.TrimSpace(link.URI) == "" {
		return nil, nil
	}
	return c.fetchContent(ctx, link.URI)
}

func (c *Client) runsURL(environment, flowID, filter string) string {
	q := url.Values{}
	q.Set("api-version", apiVersion)
	q.Set("$filter", filter)
	return c.resourceURL(environment, flowID, "runs") + "?" + q.Encode()
}

func (c *Client) resourceURL(environment, flowID string, extra ...string) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(c.baseURL, "/"))
	b.WriteString("/providers/Microsoft.ProcessSimple/")
	if c.asAdmin {
		b.WriteString("scopes/admin/")
	}
	b.WriteString("environments/")
	b.WriteString(url.PathEscape(environment))
	b.WriteString("/flows/")
	b.WriteString(url.PathEscape(flowID))
	for _, part := range extra {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(part))
	}
	return b.String()
}

type listEnvelope struct {
	Value    []runItem `json:"value"`
	NextLink string    `json:"nextLink"`
}

type runItem struct {
	Name       string          `json:"name"`
	Properties json.RawMessage `json:"properties"`
}

type runProps struct {
	StartTime apiTime         `json:"startTime"`
	EndTime   apiTime         `json:"endTime"`
	Status    string          `json:"status"`
	Error     json.RawMessage `json:"error"`
	Trigger   *actionProps    `json:"trigger"`
}

type actionEnvelope struct {
	Value    []runItem `json:"value"`
	NextLink string    `json:"nextLink"`
}

type namedAction struct {
	name  string
	props actionProps
}

type actionProps struct {
	StartTime   apiTime         `json:"startTime"`
	EndTime     apiTime         `json:"endTime"`
	Status      string          `json:"status"`
	Inputs      json.RawMessage `json:"inputs"`
	Outputs     json.RawMessage `json:"outputs"`
	InputsLink  *contentLink    `json:"inputsLink"`
	OutputsLink *contentLink    `json:"outputsLink"`
}

type contentLink struct {
	URI string `json:"uri"`
}

type apiTime struct {
	time.Time
}

func (t *apiTime) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) || bytes.Equal(b, []byte(`""`)) {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, s)
	}
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}

func decodeRunProps(raw json.RawMessage) (runProps, error) {
	var props runProps
	if len(bytes.TrimSpace(raw)) == 0 {
		return props, nil
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return props, err
	}
	return props, nil
}

func absent(raw json.RawMessage) bool {
	s := bytes.TrimSpace(raw)
	return len(s) == 0 || bytes.Equal(s, []byte("null"))
}

func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	var last error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		body, status, retry, err := c.doOnce(ctx, rawURL, true)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = err
			if attempt == maxAttempts-1 {
				return nil, err
			}
			if werr := c.wait(ctx, backoff(attempt)); werr != nil {
				return nil, werr
			}
			continue
		}
		if status == http.StatusTooManyRequests {
			last = fmt.Errorf("GET %s: 429 Too Many Requests", sanitizeURL(rawURL))
			if attempt == maxAttempts-1 {
				return nil, last
			}
			if werr := c.wait(ctx, retryAfter(retry, attempt)); werr != nil {
				return nil, werr
			}
			continue
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("GET %s: %d: %s", sanitizeURL(rawURL), status, truncate(body))
		}
		return body, nil
	}
	if last == nil {
		last = fmt.Errorf("GET %s failed", sanitizeURL(rawURL))
	}
	return nil, last
}

// fetchContent loads an inputs or outputs link. HTTP error bodies are returned
// as the cell text so secured content still appears in the report.
func (c *Client) fetchContent(ctx context.Context, rawURL string) ([]byte, error) {
	auth := useFlowAuth(rawURL, c.baseURL)
	var last []byte
	for attempt := 0; attempt < maxAttempts; attempt++ {
		body, status, retry, err := c.doOnce(ctx, rawURL, auth)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt == maxAttempts-1 {
				return []byte("content request failed"), nil
			}
			if werr := c.wait(ctx, backoff(attempt)); werr != nil {
				return nil, werr
			}
			continue
		}
		if status == http.StatusTooManyRequests {
			last = body
			if attempt == maxAttempts-1 {
				return contentError(body, status), nil
			}
			if werr := c.wait(ctx, retryAfter(retry, attempt)); werr != nil {
				return nil, werr
			}
			continue
		}
		if status < 200 || status >= 300 {
			return contentError(body, status), nil
		}
		return body, nil
	}
	return contentError(last, http.StatusTooManyRequests), nil
}

func contentError(body []byte, status int) []byte {
	if len(bytes.TrimSpace(body)) > 0 {
		return body
	}
	return []byte(http.StatusText(status))
}

func (c *Client) doOnce(ctx context.Context, rawURL string, auth bool) ([]byte, int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, "", err
	}
	if auth && c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "powerautomate-run-history")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, "", err
	}
	return body, resp.StatusCode, resp.Header.Get("Retry-After"), nil
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if c.sleep != nil {
		c.sleep(d)
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryAfter(header string, attempt int) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return backoff(attempt)
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(header); err == nil {
		d := time.Until(when)
		if d < 0 {
			return 0
		}
		return d
	}
	return backoff(attempt)
}

func backoff(attempt int) time.Duration {
	d := time.Duration(1<<attempt) * time.Second
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

func useFlowAuth(rawURL, base string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, b.Host)
}

func resolve(base, ref string) string {
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if u.IsAbs() {
		return ref
	}
	bu, err := url.Parse(base)
	if err != nil {
		return ref
	}
	return bu.ResolveReference(u).String()
}

func sanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		return s[:500]
	}
	return s
}
