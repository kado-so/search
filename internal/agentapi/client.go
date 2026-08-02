// Package agentapi implements the public kado-app /api/agent/* contract.
package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	APISchemaVersion  = "agent-api.v1"
	JSONSchemaVersion = "agent-cli-json.v1"
	maxResponseBytes  = 4 * 1024 * 1024
	maxCredentialSize = 32 * 1024
)

var (
	ErrProtocol       = errors.New("agent API response protocol failed")
	ErrAuthentication = errors.New("agent API authentication failed")
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,127}$`)
	allowedStates     = map[string]bool{
		"accepted": true, "structuring": true, "hydrating_hyde": true,
		"retrieving": true, "ranking": true, "completed": true,
		"failed": true, "canceled": true,
	}
)

// AuthorizationSource provides a short-lived bearer authorization value.
// A refresh is requested only after kado-app rejects the first value.
type AuthorizationSource interface {
	Authorization(context.Context, bool) (string, error)
}

type Options struct {
	BaseURL       *url.URL
	HTTPClient    *http.Client
	Authorization AuthorizationSource
	APIKey        string
	UserAgent     string
}

type Client struct {
	base          *url.URL
	http          *http.Client
	authorization AuthorizationSource
	apiKey        string
	userAgent     string
}

type WaitOptions struct {
	Enabled        bool
	TimeoutMS      int
	PollIntervalMS int
}

type ResultLimits struct {
	BestMatches    int
	StretchMatches int
	LaterMatches   int
	LaterOffset    int
}

type StartRequest struct {
	Query   string
	Wait    WaitOptions
	Limits  ResultLimits
	Version string
}

type DimensionUpdate struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
}

type RefineRequest struct {
	SearchID   string
	Dimensions []DimensionUpdate
	Wait       WaitOptions
	Limits     ResultLimits
}

type Answer struct {
	QuestionID string `json:"question_id"`
	Answer     string `json:"answer"`
}

type AnswerRequest struct {
	SearchID string
	Answers  []Answer
	Wait     WaitOptions
	Limits   ResultLimits
}

type CancelRequest struct {
	SearchID string
	Reason   string
}

type Response struct {
	StatusCode int
	Envelope   Envelope
	Result     Result
	ResultJSON []byte
}

type Envelope struct {
	SchemaVersion string          `json:"schema_version"`
	SearchID      *string         `json:"search_id"`
	State         string          `json:"state"`
	Result        json.RawMessage `json:"result"`
}

type Result struct {
	SchemaVersion  string       `json:"schema_version"`
	SearchID       *string      `json:"search_id"`
	SearchURL      *string      `json:"search_url"`
	State          string       `json:"state"`
	BestMatches    []Match      `json:"best_matches"`
	StretchMatches []Match      `json:"stretch_matches"`
	LaterMatches   []Match      `json:"later_matches"`
	Questions      []Question   `json:"questions"`
	Dimensions     []Dimension  `json:"dimensions"`
	Error          *ResultError `json:"error"`
	Pagination     Pagination   `json:"pagination"`
	Continuation   Continuation `json:"continuation"`
}

type Match struct {
	Rank                 int         `json:"rank"`
	SolutionID           string      `json:"solution_id"`
	Name                 string      `json:"name"`
	Summary              string      `json:"summary"`
	SolutionURL          string      `json:"solution_url"`
	Why                  []string    `json:"why"`
	SourceURL            string      `json:"source_url"`
	Score                int         `json:"score"`
	Constraints          Constraints `json:"constraints"`
	RequiredIntegrations []string    `json:"required_integrations"`
}

type Constraints struct {
	Satisfied []string `json:"satisfied"`
	Violated  []string `json:"violated"`
}

type Question struct {
	ID           string `json:"id"`
	Prompt       string `json:"prompt"`
	Reason       string `json:"reason"`
	BlocksResult bool   `json:"blocks_results"`
	DimensionID  string `json:"dimension_id,omitempty"`
	ConstraintID string `json:"constraint_id,omitempty"`
}

type Dimension struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Value      string `json:"value"`
	Unit       string `json:"unit,omitempty"`
	Confidence string `json:"confidence"`
}

type ResultError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type Pagination struct {
	TotalAvailable      int     `json:"total_available"`
	ReturnedCount       int     `json:"returned_count"`
	BestAvailable       int     `json:"best_available"`
	StretchAvailable    int     `json:"stretch_available"`
	LaterAvailable      int     `json:"later_available"`
	LaterOffset         int     `json:"later_offset"`
	LaterLimit          int     `json:"later_limit"`
	LaterReturned       int     `json:"later_returned"`
	NextLaterOffset     *int    `json:"next_later_offset"`
	PreviousLaterOffset *int    `json:"previous_later_offset"`
	NextLaterURL        *string `json:"next_later_url"`
	PreviousLaterURL    *string `json:"previous_later_url"`
}

type Continuation struct {
	StatusURL     *string `json:"status_url"`
	AnswersURL    *string `json:"answers_url,omitempty"`
	DimensionsURL *string `json:"dimensions_url,omitempty"`
	CancelURL     *string `json:"cancel_url,omitempty"`
	PollAfterMS   *int    `json:"poll_after_ms"`
	CanRefine     bool    `json:"can_refine"`
	CanCancel     bool    `json:"can_cancel"`
	NextCommand   *string `json:"next_command"`
}

// Error is a bounded public Agent API failure. The full response is available
// separately through Response.ResultJSON and is never interpolated here.
type Error struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
}

func (failure *Error) Error() string {
	if failure == nil || strings.TrimSpace(failure.Message) == "" {
		return "Agent API request failed."
	}
	return failure.Message
}

func New(options Options) (*Client, error) {
	if options.BaseURL == nil || options.BaseURL.Scheme != "https" ||
		options.BaseURL.Hostname() == "" || options.BaseURL.User != nil ||
		options.BaseURL.RawQuery != "" || options.BaseURL.Fragment != "" {
		return nil, ErrProtocol
	}
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey != options.APIKey || len(apiKey) > maxCredentialSize ||
		strings.ContainsAny(apiKey, " \t\r\n") {
		return nil, ErrAuthentication
	}
	if apiKey == "" && options.Authorization == nil {
		return nil, ErrAuthentication
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cloned := *httpClient
	cloned.Jar = nil
	if cloned.Timeout <= 0 || cloned.Timeout > 130*time.Second {
		cloned.Timeout = 130 * time.Second
	}
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("agent API redirect rejected")
	}
	base := *options.BaseURL
	return &Client{
		base:          &base,
		http:          &cloned,
		authorization: options.Authorization,
		apiKey:        apiKey,
		userAgent:     boundedUserAgent(options.UserAgent),
	}, nil
}

func (client *Client) Start(ctx context.Context, request StartRequest) (Response, error) {
	body := map[string]any{
		"schema_version": APISchemaVersion,
		"query":          request.Query,
		"mode":           "compact",
		"client": map[string]string{
			"name": "kado-cli", "version": request.Version,
		},
	}
	addWait(body, request.Wait)
	addLimits(body, request.Limits)
	return client.do(ctx, http.MethodPost, "/api/agent/searches", nil, body)
}

func (client *Client) Status(ctx context.Context, searchID string, wait WaitOptions, limits ResultLimits) (Response, error) {
	query := waitQuery(wait)
	addLimitQuery(query, limits)
	return client.do(ctx, http.MethodGet, searchPath(searchID), query, nil)
}

func (client *Client) Refine(ctx context.Context, request RefineRequest) (Response, error) {
	body := map[string]any{
		"schema_version": APISchemaVersion,
		"dimensions":     request.Dimensions,
		"mode":           "compact",
	}
	addWait(body, request.Wait)
	addLimits(body, request.Limits)
	return client.do(ctx, http.MethodPost, searchPath(request.SearchID)+"/dimensions", nil, body)
}

func (client *Client) Answer(ctx context.Context, request AnswerRequest) (Response, error) {
	body := map[string]any{
		"schema_version": APISchemaVersion,
		"answers":        request.Answers,
		"mode":           "compact",
	}
	addWait(body, request.Wait)
	addLimits(body, request.Limits)
	return client.do(ctx, http.MethodPost, searchPath(request.SearchID)+"/answers", nil, body)
}

func (client *Client) Cancel(ctx context.Context, request CancelRequest) (Response, error) {
	body := map[string]any{"schema_version": APISchemaVersion}
	if reason := strings.TrimSpace(request.Reason); reason != "" {
		body["reason"] = reason
	}
	return client.do(ctx, http.MethodPost, searchPath(request.SearchID)+"/cancel", nil, body)
}

func (client *Client) do(ctx context.Context, method, path string, query url.Values, requestBody any) (Response, error) {
	if client == nil || client.base == nil || client.http == nil {
		return Response{}, ErrProtocol
	}
	if !validAPIPath(path) {
		return Response{}, ErrProtocol
	}
	var encoded []byte
	var err error
	if requestBody != nil {
		encoded, err = json.Marshal(requestBody)
		if err != nil || len(encoded) > 64*1024 {
			return Response{}, ErrProtocol
		}
	}
	refreshed := false
	for {
		credentialHeader, credentialValue, err := client.credential(ctx, refreshed)
		if err != nil {
			return Response{}, err
		}
		endpoint := *client.base
		endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + path
		endpoint.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(encoded))
		if err != nil {
			return Response{}, ErrProtocol
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set(credentialHeader, credentialValue)
		if requestBody != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if client.userAgent != "" {
			request.Header.Set("User-Agent", client.userAgent)
		}
		response, err := client.http.Do(request)
		if err != nil {
			return Response{}, err
		}
		data, readErr := readResponse(response.Body)
		_ = response.Body.Close()
		if readErr != nil || len(credentialValue) >= 16 && bytes.Contains(data, []byte(credentialValue)) {
			return Response{}, ErrProtocol
		}
		if response.StatusCode == http.StatusUnauthorized && client.apiKey == "" && !refreshed {
			refreshed = true
			continue
		}
		mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if mediaErr != nil || mediaType != "application/json" {
			return Response{}, ErrProtocol
		}
		parsed, err := parseResponse(response.StatusCode, data)
		if err != nil {
			return Response{}, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			failure := &Error{StatusCode: response.StatusCode}
			if parsed.Result.Error != nil {
				failure.Code = parsed.Result.Error.Code
				failure.Message = parsed.Result.Error.Message
				failure.Retryable = parsed.Result.Error.Retryable
			}
			return parsed, failure
		}
		return parsed, nil
	}
}

func (client *Client) credential(ctx context.Context, refresh bool) (string, string, error) {
	if client.apiKey != "" {
		return "X-API-Key", client.apiKey, nil
	}
	value, err := client.authorization.Authorization(ctx, refresh)
	if err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(value, "Bearer ") || len(value) > maxCredentialSize ||
		strings.ContainsAny(strings.TrimPrefix(value, "Bearer "), " \t\r\n") {
		return "", "", ErrAuthentication
	}
	return "Authorization", value, nil
}

func parseResponse(statusCode int, data []byte) (Response, error) {
	if len(data) == 0 {
		return Response{}, ErrProtocol
	}
	var rawEnvelope map[string]json.RawMessage
	if json.Unmarshal(data, &rawEnvelope) != nil || !hasKeys(rawEnvelope,
		"schema_version", "search_id", "state", "result") {
		return Response{}, ErrProtocol
	}
	var envelope Envelope
	if json.Unmarshal(data, &envelope) != nil || envelope.SchemaVersion != APISchemaVersion ||
		!allowedStates[envelope.State] || len(envelope.Result) == 0 {
		return Response{}, ErrProtocol
	}
	var rawResult map[string]json.RawMessage
	if json.Unmarshal(envelope.Result, &rawResult) != nil || !hasKeys(rawResult,
		"schema_version", "search_id", "search_url", "state", "best_matches",
		"stretch_matches", "later_matches", "questions", "dimensions", "error",
		"pagination", "continuation") {
		return Response{}, ErrProtocol
	}
	var result Result
	if json.Unmarshal(envelope.Result, &result) != nil || validateResult(envelope, result) != nil {
		return Response{}, ErrProtocol
	}
	if !validNestedContract(rawResult) {
		return Response{}, ErrProtocol
	}
	var compact bytes.Buffer
	if json.Compact(&compact, envelope.Result) != nil {
		return Response{}, ErrProtocol
	}
	compact.WriteByte('\n')
	return Response{StatusCode: statusCode, Envelope: envelope, Result: result, ResultJSON: compact.Bytes()}, nil
}

func validNestedContract(rawResult map[string]json.RawMessage) bool {
	var pagination map[string]json.RawMessage
	if json.Unmarshal(rawResult["pagination"], &pagination) != nil || !hasKeys(pagination,
		"total_available", "returned_count", "best_available", "stretch_available",
		"later_available", "later_offset", "later_limit", "later_returned",
		"next_later_offset", "previous_later_offset", "next_later_url",
		"previous_later_url") {
		return false
	}
	var continuation map[string]json.RawMessage
	if json.Unmarshal(rawResult["continuation"], &continuation) != nil || !hasKeys(continuation,
		"status_url", "poll_after_ms", "can_refine", "can_cancel", "next_command") {
		return false
	}
	return true
}

func validateResult(envelope Envelope, result Result) error {
	if result.SchemaVersion != JSONSchemaVersion || result.State != envelope.State ||
		!allowedStates[result.State] || result.BestMatches == nil ||
		result.StretchMatches == nil || result.LaterMatches == nil ||
		result.Questions == nil || result.Dimensions == nil {
		return ErrProtocol
	}
	if !sameOptionalString(envelope.SearchID, result.SearchID) {
		return ErrProtocol
	}
	if result.SearchID == nil {
		if result.State != "failed" || result.Error == nil {
			return ErrProtocol
		}
	} else if !identifierPattern.MatchString(*result.SearchID) {
		return ErrProtocol
	}
	if result.Error != nil {
		if result.Error.Code == "" || result.Error.Message == "" ||
			result.Error.HTTPStatus < 0 || result.Error.HTTPStatus > 599 {
			return ErrProtocol
		}
	}
	for _, values := range [][]Match{result.BestMatches, result.StretchMatches, result.LaterMatches} {
		for _, match := range values {
			if match.Rank < 1 || match.SolutionID == "" || match.Name == "" ||
				match.Why == nil || match.Constraints.Satisfied == nil ||
				match.Constraints.Violated == nil || match.RequiredIntegrations == nil {
				return ErrProtocol
			}
		}
	}
	if result.Pagination.TotalAvailable < 0 || result.Pagination.ReturnedCount < 0 ||
		result.Pagination.BestAvailable < 0 || result.Pagination.StretchAvailable < 0 ||
		result.Pagination.LaterAvailable < 0 || result.Pagination.LaterOffset < 0 ||
		result.Pagination.LaterLimit < 0 || result.Pagination.LaterReturned < 0 {
		return ErrProtocol
	}
	return nil
}

func readResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, ErrProtocol
	}
	return data, nil
}

func addWait(body map[string]any, wait WaitOptions) {
	if wait.Enabled {
		body["wait"] = map[string]any{
			"until": "completed_or_terminal", "timeout_ms": wait.TimeoutMS,
			"poll_interval_ms": wait.PollIntervalMS,
		}
	}
}

func waitQuery(wait WaitOptions) url.Values {
	query := url.Values{}
	if wait.Enabled {
		query.Set("wait", "1")
		query.Set("timeout_ms", strconv.Itoa(wait.TimeoutMS))
		query.Set("poll_interval_ms", strconv.Itoa(wait.PollIntervalMS))
	}
	return query
}

func addLimits(body map[string]any, limits ResultLimits) {
	values := map[string]int{}
	if limits.BestMatches > 0 {
		values["best_matches"] = limits.BestMatches
	}
	if limits.StretchMatches > 0 {
		values["stretch_matches"] = limits.StretchMatches
	}
	if limits.LaterMatches > 0 {
		values["later_matches"] = limits.LaterMatches
	}
	if len(values) > 0 {
		body["max_results"] = values
	}
	if limits.LaterOffset > 0 {
		body["later_offset"] = limits.LaterOffset
	}
}

func addLimitQuery(query url.Values, limits ResultLimits) {
	if limits.BestMatches > 0 {
		query.Set("max_best_matches", strconv.Itoa(limits.BestMatches))
	}
	if limits.StretchMatches > 0 {
		query.Set("max_stretch_matches", strconv.Itoa(limits.StretchMatches))
	}
	if limits.LaterMatches > 0 {
		query.Set("max_later_matches", strconv.Itoa(limits.LaterMatches))
	}
	if limits.LaterOffset > 0 {
		query.Set("later_offset", strconv.Itoa(limits.LaterOffset))
	}
}

func searchPath(searchID string) string {
	if !identifierPattern.MatchString(searchID) {
		return ""
	}
	return "/api/agent/searches/" + searchID
}

func validAPIPath(path string) bool {
	return path == "/api/agent/searches" || strings.HasPrefix(path, "/api/agent/searches/") &&
		!strings.Contains(path, "..") && !strings.ContainsAny(path, "?#\\")
}

func hasKeys(value map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func boundedUserAgent(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 256 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func (result Result) String() string {
	id := ""
	if result.SearchID != nil {
		id = *result.SearchID
	}
	return fmt.Sprintf("agent search state=%s id=%s", result.State, id)
}

func (Result) GoString() string { return "agentapi.Result{bounded public fields}" }
