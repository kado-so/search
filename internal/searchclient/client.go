package searchclient

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
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/searchcontract"
)

type Client struct {
	base          *url.URL
	searchPath    string
	httpClient    *http.Client
	authorization AuthorizationSource
	limits        Limits
	wait          func(context.Context, time.Duration) error
}

// New constructs a no-redirect, no-cookie Search client on the same exact
// HTTPS resource and authorization-server boundary.
func New(
	base *url.URL,
	httpClient *http.Client,
	authorization AuthorizationSource,
	limits Limits,
) (*Client, error) {
	validated, err := validateBase(base)
	if err != nil || authorization == nil || !validLimits(limits) {
		return nil, newError(
			"search_client_invalid",
			"Search client configuration is invalid.",
			0,
			false,
			ErrProtocol,
		)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cloned := *httpClient
	cloned.Jar = nil
	if cloned.Timeout <= 0 || cloned.Timeout > limits.MaxRequestTime {
		cloned.Timeout = limits.MaxRequestTime
	}
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return ErrRedirect
	}
	searchPath := strings.TrimSuffix(validated.Path, "/") + "/search"
	return &Client{
		base:          validated,
		searchPath:    searchPath,
		httpClient:    &cloned,
		authorization: authorization,
		limits:        limits,
		wait:          waitContext,
	}, nil
}

// Search starts or reuses the globally cached canonical query.
func (client *Client) Search(ctx context.Context, query string) (Document, error) {
	if err := validateQuery(query); err != nil {
		return Document{}, err
	}
	endpoint := *client.base
	endpoint.Path = client.searchPath
	endpoint.RawQuery = url.Values{"q": {query}}.Encode()
	if len(endpoint.RawQuery) > 8*1024 {
		return Document{}, newError(
			"search_query_too_large",
			"Search query is too large.",
			0,
			false,
			ErrProtocol,
		)
	}
	return client.get(ctx, &endpoint, query)
}

// Status follows the document's self link and performs a lifecycle status
// operation for the exact opaque Search ID.
func (client *Client) Status(ctx context.Context, document Document) (Document, error) {
	if err := client.validateDocumentIdentity(document); err != nil {
		return Document{}, err
	}
	endpoint := *document.self
	parameters := endpoint.Query()
	parameters.Del("cursor")
	parameters.Del("page")
	parameters.Set("operation", "status")
	parameters.Set("search_id", document.SearchID)
	endpoint.RawQuery = parameters.Encode()
	return client.get(ctx, &endpoint, document.Query)
}

// Clarify submits one answer to the exact Search represented by document.
func (client *Client) Clarify(
	ctx context.Context,
	document Document,
	answer string,
) (Document, error) {
	if err := client.validateDocumentIdentity(document); err != nil {
		return Document{}, err
	}
	if document.Status != StatusNeedsInput || document.Question == nil {
		return Document{}, lifecycleError("Search is not waiting for clarification.")
	}
	if !validPublicText(answer, 200) {
		return Document{}, newError(
			"search_answer_invalid",
			"Search clarification answer is invalid.",
			0,
			false,
			ErrProtocol,
		)
	}
	return client.post(ctx, document, url.Values{
		"operation":   {"clarify"},
		"q":           {document.Query},
		"search_id":   {document.SearchID},
		"question_id": {document.Question.ID},
		"answer":      {answer},
	})
}

// Cancel requests cancellation for the exact in-flight Search.
func (client *Client) Cancel(ctx context.Context, document Document) (Document, error) {
	if err := client.validateDocumentIdentity(document); err != nil {
		return Document{}, err
	}
	if document.Status != StatusQueued &&
		document.Status != StatusRunning &&
		document.Status != StatusNeedsInput {
		return Document{}, lifecycleError("Search is not cancelable in its current state.")
	}
	return client.post(ctx, document, url.Values{
		"operation": {"cancel"},
		"q":         {document.Query},
		"search_id": {document.SearchID},
	})
}

// Retry requests one server-defined retry for a failed or canceled Search.
func (client *Client) Retry(ctx context.Context, document Document) (Document, error) {
	if err := client.validateDocumentIdentity(document); err != nil {
		return Document{}, err
	}
	if document.Status != StatusFailed && document.Status != StatusCanceled {
		return Document{}, lifecycleError("Search is not retryable in its current state.")
	}
	return client.post(ctx, document, url.Values{
		"operation": {"retry"},
		"q":         {document.Query},
		"search_id": {document.SearchID},
	})
}

// Next follows the opaque next relation exactly. It never reconstructs a
// cursor or page number.
func (client *Client) Next(ctx context.Context, document Document) (Document, error) {
	if err := client.validateDocumentIdentity(document); err != nil {
		return Document{}, err
	}
	if document.Status != StatusComplete || document.next == nil {
		return Document{}, lifecycleError("Search has no next page.")
	}
	return client.get(ctx, document.next, document.Query)
}

func (client *Client) post(
	ctx context.Context,
	document Document,
	values url.Values,
) (Document, error) {
	endpoint := *document.self
	endpoint.RawQuery = ""
	endpoint.ForceQuery = false
	body := []byte(values.Encode())
	defer clear(body)
	return client.do(ctx, http.MethodPost, &endpoint, body, false, document.Query)
}

func (client *Client) get(
	ctx context.Context,
	endpoint *url.URL,
	expectedQuery string,
) (Document, error) {
	return client.do(ctx, http.MethodGet, endpoint, nil, true, expectedQuery)
}

func (client *Client) do(
	ctx context.Context,
	method string,
	endpoint *url.URL,
	body []byte,
	safeRetry bool,
	expectedQuery string,
) (Document, error) {
	if err := client.validateRequestURL(endpoint, method, expectedQuery); err != nil {
		return Document{}, err
	}
	attempts := 1
	if safeRetry {
		attempts = client.limits.MaxGETAttempts
	}
	authRefreshed := false
	forceRefresh := false
	for attempt := 0; attempt < attempts; attempt++ {
		for {
			authorization, err := client.authorization.Authorization(ctx, forceRefresh)
			forceRefresh = false
			if err != nil {
				return Document{}, newError(
					"search_authentication_failed",
					"Could not authenticate the Search request.",
					0,
					false,
					ErrAuthentication,
				)
			}
			if !validAuthorization(authorization) {
				return Document{}, newError(
					"search_authentication_failed",
					"Could not authenticate the Search request.",
					0,
					false,
					ErrAuthentication,
				)
			}
			request, err := http.NewRequestWithContext(
				ctx,
				method,
				endpoint.String(),
				bytes.NewReader(body),
			)
			if err != nil {
				return Document{}, protocolError()
			}
			request.Header.Set("Accept", CanonicalMediaType)
			request.Header.Set("Authorization", authorization)
			if method == http.MethodPost {
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}

			response, requestErr := client.httpClient.Do(request)
			if requestErr != nil {
				if errors.Is(requestErr, ErrRedirect) {
					return Document{}, newError(
						"search_redirect_rejected",
						"Search server redirect was rejected.",
						0,
						false,
						ErrRedirect,
					)
				}
				if !safeRetry || attempt+1 >= attempts {
					if errors.Is(requestErr, context.DeadlineExceeded) ||
						errors.Is(requestErr, context.Canceled) {
						return Document{}, requestErr
					}
					return Document{}, newError(
						"search_unavailable",
						"Search is temporarily unavailable.",
						0,
						true,
						ErrProtocol,
					)
				}
				break
			}

			if response.StatusCode == http.StatusUnauthorized && !authRefreshed {
				encoded, err := readAndCloseBounded(
					response.Body,
					client.limits.MaxErrorBytes,
				)
				if err != nil ||
					validateNoAuthorizationReflection(encoded, authorization) != nil {
					return Document{}, protocolError()
				}
				authRefreshed = true
				forceRefresh = true
				continue
			}
			if safeRetry &&
				(response.StatusCode == http.StatusBadGateway ||
					response.StatusCode == http.StatusServiceUnavailable ||
					response.StatusCode == http.StatusGatewayTimeout) &&
				attempt+1 < attempts {
				delay := retryDelay(response.Header.Get("Retry-After"))
				encoded, err := readAndCloseBounded(
					response.Body,
					client.limits.MaxErrorBytes,
				)
				if err != nil ||
					validateNoAuthorizationReflection(encoded, authorization) != nil {
					return Document{}, protocolError()
				}
				if err := client.wait(ctx, delay); err != nil {
					return Document{}, err
				}
				break
			}
			return client.decodeResponse(response, authorization, expectedQuery)
		}
	}
	return Document{}, protocolError()
}

func (client *Client) decodeResponse(
	response *http.Response,
	authorization string,
	expectedQuery string,
) (Document, error) {
	defer func() { _ = response.Body.Close() }()
	mediaType, parameters, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		return Document{}, protocolError()
	}
	if response.StatusCode == http.StatusOK {
		if mediaType != CanonicalMediaType || !validCharset(parameters) {
			return Document{}, protocolError()
		}
		encoded, err := readBounded(response.Body, client.limits.MaxDocumentBytes)
		if err != nil ||
			validateNoAuthorizationReflection(encoded, authorization) != nil {
			return Document{}, protocolError()
		}
		return client.decodeDocument(encoded, expectedQuery)
	}
	if mediaType != "application/problem+json" && mediaType != "application/json" {
		return Document{}, protocolError()
	}
	encoded, err := readBounded(response.Body, client.limits.MaxErrorBytes)
	if err != nil ||
		validateNoAuthorizationReflection(encoded, authorization) != nil {
		return Document{}, protocolError()
	}
	if response.StatusCode == http.StatusUnauthorized {
		return Document{}, newError(
			"search_authentication_failed",
			"Search authentication was rejected.",
			response.StatusCode,
			false,
			ErrAuthentication,
		)
	}
	problem, err := decodeProblem(encoded)
	if err != nil {
		return Document{}, protocolError()
	}
	return Document{}, newError(
		problem.Code,
		problem.Message,
		response.StatusCode,
		problem.Retryable,
		nil,
	)
}

func validateNoAuthorizationReflection(
	encoded []byte,
	authorization string,
) error {
	token := strings.TrimPrefix(authorization, "Bearer ")
	if token == "" ||
		bytes.Contains(encoded, []byte(authorization)) ||
		bytes.Contains(encoded, []byte(token)) ||
		!utf8.Valid(encoded) ||
		!validJSONSurrogates(encoded) {
		return ErrProtocol
	}

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	depth := 0
	complete := false
	for {
		value, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if complete && depth == 0 {
				return nil
			}
			return ErrProtocol
		}
		if err != nil || complete {
			return ErrProtocol
		}

		switch value := value.(type) {
		case json.Delim:
			switch value {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth < 0 {
					return ErrProtocol
				}
				if depth == 0 {
					complete = true
				}
			default:
				return ErrProtocol
			}
		case string:
			if strings.Contains(value, authorization) ||
				strings.Contains(value, token) {
				return ErrProtocol
			}
			if depth == 0 {
				complete = true
			}
		default:
			if depth == 0 {
				complete = true
			}
		}
	}
}

func validJSONSurrogates(encoded []byte) bool {
	for index := 0; index < len(encoded); index++ {
		if encoded[index] != '"' {
			continue
		}
		for index++; index < len(encoded); index++ {
			switch encoded[index] {
			case '"':
				goto nextString
			case '\\':
				index++
				if index >= len(encoded) {
					return false
				}
				if encoded[index] != 'u' {
					continue
				}
				codeUnit, ok := jsonHexCodeUnit(encoded, index+1)
				if !ok {
					return false
				}
				index += 4
				switch {
				case codeUnit >= 0xd800 && codeUnit <= 0xdbff:
					if index+6 >= len(encoded) ||
						encoded[index+1] != '\\' ||
						encoded[index+2] != 'u' {
						return false
					}
					low, ok := jsonHexCodeUnit(encoded, index+3)
					if !ok || low < 0xdc00 || low > 0xdfff {
						return false
					}
					index += 6
				case codeUnit >= 0xdc00 && codeUnit <= 0xdfff:
					return false
				}
			}
		}
		return false
	nextString:
		continue
	}
	return true
}

func jsonHexCodeUnit(encoded []byte, start int) (uint16, bool) {
	if start < 0 || start+4 > len(encoded) {
		return 0, false
	}
	var output uint16
	for _, value := range encoded[start : start+4] {
		output <<= 4
		switch {
		case value >= '0' && value <= '9':
			output |= uint16(value - '0')
		case value >= 'a' && value <= 'f':
			output |= uint16(value-'a') + 10
		case value >= 'A' && value <= 'F':
			output |= uint16(value-'A') + 10
		default:
			return 0, false
		}
	}
	return output, true
}

func (client *Client) decodeDocument(
	encoded []byte,
	expectedQuery string,
) (Document, error) {
	validated, err := searchcontract.Validate(encoded)
	if err != nil {
		var unsupported *searchcontract.UnsupportedVersionError
		if errors.As(err, &unsupported) {
			return Document{}, newError(
				"search_document_version_unsupported",
				unsupported.Error(),
				0,
				false,
				ErrUnsupportedVersion,
			)
		}
		return Document{}, protocolError()
	}
	if validated.Search.Query != expectedQuery {
		return Document{}, protocolError()
	}
	self, err := client.validateDocumentLink(
		validated.Links.Self,
		false,
		expectedQuery,
	)
	if err != nil {
		return Document{}, err
	}
	var next, previous *url.URL
	if validated.ResultSet != nil {
		if _, err := client.validateDocumentLink(
			validated.ResultSet.Links.Self,
			false,
			expectedQuery,
		); err != nil {
			return Document{}, err
		}
		if validated.ResultSet.Links.Next != nil {
			next, err = client.validateDocumentLink(
				*validated.ResultSet.Links.Next,
				true,
				expectedQuery,
			)
			if err != nil {
				return Document{}, err
			}
		}
		if validated.ResultSet.Links.Previous != nil {
			previous, err = client.validateDocumentLink(
				*validated.ResultSet.Links.Previous,
				true,
				expectedQuery,
			)
			if err != nil {
				return Document{}, err
			}
		}
	}
	var question *Question
	if validated.State.Question != nil {
		question = &Question{
			ID:      validated.State.Question.ID,
			Prompt:  validated.State.Question.Prompt,
			Options: append([]string(nil), validated.State.Question.Options...),
		}
	}
	var failure *Failure
	if validated.State.Error != nil {
		failure = &Failure{
			Code:      boundedCode(validated.State.Error.Code),
			Message:   diagnostic.TerminalSafeText(validated.State.Error.Message, 280),
			Retryable: validated.State.Error.Retryable,
		}
	}
	return Document{
		raw:           append([]byte(nil), encoded...),
		self:          self,
		next:          next,
		previous:      previous,
		SchemaVersion: validated.SchemaVersion,
		SearchID:      validated.Search.ID,
		Query:         validated.Search.Query,
		Status:        validated.State.Status,
		Question:      question,
		Failure:       failure,
	}, nil
}

func (client *Client) validateDocumentIdentity(document Document) error {
	if document.self == nil ||
		!validOpaqueID(document.SearchID) ||
		!validPublicText(document.Query, 2_000) {
		return protocolError()
	}
	if _, err := client.validateDocumentLink(
		document.self.String(),
		false,
		document.Query,
	); err != nil {
		return err
	}
	for _, relation := range []*url.URL{document.next, document.previous} {
		if relation == nil {
			continue
		}
		if _, err := client.validateDocumentLink(
			relation.String(),
			true,
			document.Query,
		); err != nil {
			return err
		}
	}
	return nil
}

func (client *Client) validateDocumentLink(
	raw string,
	pageRelation bool,
	expectedQuery string,
) (*url.URL, error) {
	parsed, err := client.validateServerLink(raw)
	if err != nil {
		return nil, err
	}
	parameters := parsed.Query()
	if parameters.Get("q") != expectedQuery ||
		parameters.Has("operation") ||
		parameters.Has("search_id") {
		return nil, protocolError()
	}
	if pageRelation && parameters.Has("cursor") == parameters.Has("page") {
		return nil, protocolError()
	}
	return parsed, nil
}

func (client *Client) validateRequestURL(
	endpoint *url.URL,
	method string,
	expectedQuery string,
) error {
	if endpoint == nil || !validPublicText(expectedQuery, 2_000) {
		return protocolError()
	}
	if method == http.MethodPost {
		if !client.sameSearchEndpoint(endpoint) ||
			endpoint.RawQuery != "" ||
			endpoint.ForceQuery {
			return protocolError()
		}
		return nil
	}
	validated, err := client.validateServerLink(endpoint.String())
	if err != nil {
		return err
	}
	parameters := validated.Query()
	if parameters.Get("q") != expectedQuery {
		return protocolError()
	}
	if operation := parameters.Get("operation"); operation != "" {
		if operation != "status" ||
			len(parameters["operation"]) != 1 ||
			len(parameters["search_id"]) != 1 ||
			!validOpaqueID(parameters.Get("search_id")) ||
			parameters.Has("cursor") ||
			parameters.Has("page") {
			return protocolError()
		}
	}
	return nil
}

func (client *Client) validateServerLink(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > 2_048 || strings.ContainsFunc(raw, unicode.IsControl) {
		return nil, protocolError()
	}
	parsed, err := url.Parse(raw)
	if err != nil ||
		parsed.String() != raw ||
		!client.sameSearchEndpoint(parsed) {
		return nil, newError(
			"search_link_rejected",
			"Search server returned an untrusted link.",
			0,
			false,
			ErrProtocol,
		)
	}
	parameters := parsed.Query()
	if len(parameters["q"]) != 1 ||
		!validPublicText(parameters.Get("q"), 2_000) ||
		len(parameters) > 3 {
		return nil, protocolError()
	}
	for key, values := range parameters {
		switch key {
		case "q":
		case "cursor":
			if len(values) != 1 || !validOpaqueID(values[0]) || parameters.Has("page") {
				return nil, protocolError()
			}
		case "page":
			if len(values) != 1 || !validPage(values[0]) || parameters.Has("cursor") {
				return nil, protocolError()
			}
		case "operation":
			if len(values) != 1 || values[0] != "status" {
				return nil, protocolError()
			}
		case "search_id":
			if len(values) != 1 || !validOpaqueID(values[0]) {
				return nil, protocolError()
			}
		default:
			return nil, protocolError()
		}
	}
	return parsed, nil
}

func (client *Client) sameSearchEndpoint(endpoint *url.URL) bool {
	return endpoint != nil &&
		endpoint.Scheme == client.base.Scheme &&
		endpoint.Host == client.base.Host &&
		endpoint.User == nil &&
		endpoint.Opaque == "" &&
		endpoint.Fragment == "" &&
		endpoint.RawPath == "" &&
		endpoint.Path == client.searchPath
}

func validateBase(input *url.URL) (*url.URL, error) {
	if input == nil {
		return nil, ErrProtocol
	}
	cloned := *input
	if cloned.Scheme != "https" ||
		cloned.Hostname() == "" ||
		cloned.User != nil ||
		cloned.Opaque != "" ||
		cloned.RawQuery != "" ||
		cloned.ForceQuery ||
		cloned.Fragment != "" ||
		cloned.RawPath != "" ||
		strings.ContainsFunc(cloned.String(), unicode.IsControl) {
		return nil, ErrProtocol
	}
	if cloned.Path == "" {
		cloned.Path = "/"
	}
	if cloned.Path != "/" &&
		(strings.HasSuffix(cloned.Path, "/") ||
			strings.Contains(cloned.Path, "//") ||
			strings.Contains(cloned.Path, "\\") ||
			strings.Contains(cloned.EscapedPath(), "%") ||
			!validBasePath(cloned.Path)) {
		return nil, ErrProtocol
	}
	if strings.HasSuffix(cloned.Host, ":") {
		return nil, ErrProtocol
	}
	if port := cloned.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65_535 {
			return nil, ErrProtocol
		}
	}
	return &cloned, nil
}

func validBasePath(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, character := range segment {
			if character >= 'a' && character <= 'z' ||
				character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' ||
				character == '-' ||
				character == '.' ||
				character == '_' ||
				character == '~' {
				continue
			}
			return false
		}
	}
	return true
}

func validLimits(limits Limits) bool {
	return limits.MaxDocumentBytes >= 64*1024 &&
		limits.MaxDocumentBytes <= 16*1024*1024 &&
		limits.MaxErrorBytes >= 1_024 &&
		limits.MaxErrorBytes <= 256*1024 &&
		limits.MaxPages >= 1 &&
		limits.MaxPages <= 1_000 &&
		limits.MaxGETAttempts >= 1 &&
		limits.MaxGETAttempts <= 3 &&
		limits.MaxLifecycleOperations >= 1 &&
		limits.MaxLifecycleOperations <= 10_000 &&
		limits.MaxClarifications >= 1 &&
		limits.MaxClarifications <= 64 &&
		limits.MaxRequestTime > 0 &&
		limits.MaxRequestTime <= 2*time.Minute
}

func validAuthorization(value string) bool {
	if len(value) < len("Bearer x") || len(value) > 32*1024 {
		return false
	}
	if !strings.HasPrefix(value, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(value, "Bearer ")
	return token != "" && !strings.ContainsAny(token, " \t\r\n,")
}

func validCharset(parameters map[string]string) bool {
	if len(parameters) == 0 {
		return true
	}
	return len(parameters) == 1 && strings.EqualFold(parameters["charset"], "utf-8")
}

func validateQuery(query string) error {
	if !validPublicText(query, 2_000) {
		return newError(
			"search_query_invalid",
			"Search query must be valid UTF-8 between 1 and 2000 bytes.",
			0,
			false,
			ErrProtocol,
		)
	}
	return nil
}

func validPublicText(value string, maximum int) bool {
	return value != "" &&
		utf8.ValidString(value) &&
		len([]byte(value)) <= maximum &&
		!strings.ContainsRune(value, '\x00')
}

func validOpaqueID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			index > 0 && (character == '.' ||
				character == '_' ||
				character == '~' ||
				character == '-') {
			continue
		}
		return false
	}
	return true
}

func validPage(value string) bool {
	if value == "" || value[0] == '0' {
		return false
	}
	number, err := strconv.ParseUint(value, 10, 31)
	return err == nil && number > 0
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	encoded, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(encoded)) > maximum {
		return nil, ErrProtocol
	}
	return encoded, nil
}

func readAndCloseBounded(body io.ReadCloser, maximum int64) ([]byte, error) {
	defer func() { _ = body.Close() }()
	return readBounded(body, maximum)
}

func retryDelay(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err == nil && seconds >= 0 && seconds <= 2 {
		return time.Duration(seconds) * time.Second
	}
	return 100 * time.Millisecond
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func lifecycleError(message string) error {
	return newError(
		"search_operation_invalid",
		message,
		0,
		false,
		ErrProtocol,
	)
}

func protocolError() error {
	return newError(
		"search_protocol_failed",
		"Search server returned an invalid response.",
		0,
		false,
		ErrProtocol,
	)
}

type problemEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

type decodedProblem struct {
	Code      string
	Message   string
	Retryable bool
}

func decodeProblem(encoded []byte) (decodedProblem, error) {
	if err := rejectDuplicateMembers(encoded); err != nil {
		return decodedProblem{}, err
	}
	var problem problemEnvelope
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&problem); err != nil {
		return decodedProblem{}, err
	}
	if problem.Error.Code == "" ||
		len(problem.Error.Code) > 96 ||
		!validPublicText(problem.Error.Message, 280) {
		return decodedProblem{}, ErrProtocol
	}
	return decodedProblem{
		Code:      problem.Error.Code,
		Message:   problem.Error.Message,
		Retryable: problem.Error.Retryable,
	}, nil
}

func boundedCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var output strings.Builder
	for _, character := range value {
		if output.Len() >= 96 {
			break
		}
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' {
			output.WriteRune(character)
		} else {
			output.WriteByte('_')
		}
	}
	return strings.Trim(output.String(), "_")
}

func rejectDuplicateMembers(encoded []byte) error {
	if !utf8.Valid(encoded) {
		return ErrProtocol
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrProtocol
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrProtocol
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrProtocol
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
			return ErrProtocol
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
			return ErrProtocol
		}
	default:
		return fmt.Errorf("invalid JSON delimiter")
	}
	return nil
}
