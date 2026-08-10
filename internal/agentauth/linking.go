package agentauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

type linkAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

type linkMetadata struct {
	Issuer             string `json:"issuer"`
	LinkEndpoint       string `json:"link_endpoint"`
	LinkStatusEndpoint string `json:"link_status_endpoint"`
}

type linkSuccessResponse struct {
	Status string `json:"status"`
}

type linkErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

var linkAuthorizationFields = []string{
	"device_code",
	"user_code",
	"verification_uri",
	"verification_uri_complete",
	"expires_in",
	"interval",
}
var linkMetadataFields = []string{"issuer", "link_endpoint", "link_status_endpoint"}
var linkSuccessFields = []string{"status"}
var linkErrorFields = []string{"error", "error_description"}
var deviceCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var userCodePattern = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$`)

// LinkAccount starts a short-lived device-style user-link authorization and
// polls until the signed-in human approves, denies, or lets it expire.
func (client *Client) LinkAccount(
	ctx context.Context,
	token SessionToken,
	notify func(LinkAuthorization) error,
) (LinkStatus, error) {
	if token.AuthorizationHeader() == "" || notify == nil {
		return LinkStatus{}, newProtocolError(ErrAuthentication, nil)
	}
	metadata, err := client.discoverLinking(ctx)
	if err != nil {
		return LinkStatus{}, err
	}
	status, encoded, err := client.doAuthorizedJSON(
		ctx,
		http.MethodPost,
		metadata.LinkEndpoint,
		nil,
		"",
		token.AuthorizationHeader(),
	)
	if err != nil {
		return LinkStatus{}, err
	}
	if status != http.StatusOK {
		return LinkStatus{}, classifyLinkFailure(status, encoded)
	}
	var response linkAuthorizationResponse
	if err := decodeExactJSONObject(encoded, &response, linkAuthorizationFields); err != nil {
		return LinkStatus{}, newProtocolError(ErrProtocol, err)
	}
	authorization, err := client.validateLinkAuthorization(response)
	if err != nil {
		return LinkStatus{}, err
	}
	if err := notify(authorization); err != nil {
		return LinkStatus{}, err
	}
	return client.pollLink(ctx, metadata.LinkStatusEndpoint, authorization)
}

func (client *Client) validateLinkAuthorization(
	response linkAuthorizationResponse,
) (LinkAuthorization, error) {
	verification, err := validateAdvertisedEndpoint(response.VerificationURI, client.issuer)
	if err != nil || verification.String() != response.VerificationURI {
		return LinkAuthorization{}, newProtocolError(ErrProtocol, err)
	}
	complete, err := url.Parse(response.VerificationURIComplete)
	if err != nil || complete.Scheme != "https" || complete.Host != client.issuer.Host ||
		complete.Path != verification.Path || complete.Query().Get("user_code") != response.UserCode {
		return LinkAuthorization{}, newProtocolError(ErrProtocol, err)
	}
	if !deviceCodePattern.MatchString(response.DeviceCode) ||
		!userCodePattern.MatchString(response.UserCode) ||
		response.ExpiresIn < 1 || response.ExpiresIn > 600 ||
		response.Interval < 1 || response.Interval > 30 {
		return LinkAuthorization{}, newProtocolError(ErrProtocol, nil)
	}
	return LinkAuthorization{
		DeviceCode:              response.DeviceCode,
		UserCode:                response.UserCode,
		VerificationURI:         response.VerificationURI,
		VerificationURIComplete: response.VerificationURIComplete,
		ExpiresIn:               time.Duration(response.ExpiresIn) * time.Second,
		Interval:                time.Duration(response.Interval) * time.Second,
	}, nil
}

func (client *Client) discoverLinking(ctx context.Context) (linkMetadata, error) {
	var response linkMetadata
	if err := client.fetchJSON(
		ctx,
		appendEndpoint(client.issuer, ".well-known/agent-user-linking"),
		&response,
		linkMetadataFields,
	); err != nil {
		return linkMetadata{}, newProtocolError(ErrDiscovery, err)
	}
	if response.Issuer != client.issuer.String() {
		return linkMetadata{}, newProtocolError(ErrDiscovery, nil)
	}
	linkEndpoint, err := validateAdvertisedEndpoint(response.LinkEndpoint, client.issuer)
	if err != nil {
		return linkMetadata{}, newProtocolError(ErrDiscovery, err)
	}
	statusEndpoint, err := validateAdvertisedEndpoint(response.LinkStatusEndpoint, client.issuer)
	if err != nil {
		return linkMetadata{}, newProtocolError(ErrDiscovery, err)
	}
	return linkMetadata{
		Issuer:             response.Issuer,
		LinkEndpoint:       linkEndpoint.String(),
		LinkStatusEndpoint: statusEndpoint.String(),
	}, nil
}

func (client *Client) pollLink(
	ctx context.Context,
	endpoint string,
	authorization LinkAuthorization,
) (LinkStatus, error) {
	deadline := client.now().Add(authorization.ExpiresIn)
	interval := authorization.Interval
	for client.now().Before(deadline) {
		form := url.Values{"device_code": {authorization.DeviceCode}}
		status, encoded, err := client.doAuthorizedJSON(
			ctx,
			http.MethodPost,
			endpoint,
			[]byte(form.Encode()),
			"application/x-www-form-urlencoded",
			"",
		)
		if err != nil {
			return LinkStatus{}, err
		}
		if status == http.StatusOK {
			var response linkSuccessResponse
			if err := decodeExactJSONObject(encoded, &response, linkSuccessFields); err != nil ||
				response.Status != LinkStatusLinked {
				return LinkStatus{}, newProtocolError(ErrProtocol, err)
			}
			return LinkStatus{Status: response.Status}, nil
		}
		var response linkErrorResponse
		if err := decodeExactJSONObject(encoded, &response, linkErrorFields); err != nil ||
			response.ErrorDescription == "" || len(response.ErrorDescription) > 1024 {
			return LinkStatus{}, newProtocolError(ErrProtocol, err)
		}
		switch response.Error {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return LinkStatus{}, newProtocolError(ErrLinkDenied, nil)
		case "expired_token":
			return LinkStatus{}, newProtocolError(ErrLinkExpired, nil)
		default:
			return LinkStatus{}, newProtocolError(ErrAuthentication, nil)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return LinkStatus{}, ctx.Err()
		case <-timer.C:
		}
	}
	return LinkStatus{}, newProtocolError(ErrLinkExpired, nil)
}

func classifyLinkFailure(status int, encoded []byte) error {
	var response linkErrorResponse
	if err := decodeExactJSONObject(encoded, &response, linkErrorFields); err != nil {
		return newProtocolError(ErrAuthentication, err)
	}
	if response.Error == "temporarily_unavailable" && status == http.StatusServiceUnavailable {
		return newProtocolError(ErrAuthentication, nil)
	}
	return newProtocolError(ErrAuthentication, nil)
}

func (client *Client) doAuthorizedJSON(
	ctx context.Context,
	method string,
	endpoint string,
	body []byte,
	contentType string,
	authorization string,
) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, newProtocolError(ErrProtocol, err)
	}
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, ErrRedirect) {
			return 0, nil, newProtocolError(ErrRedirect, err)
		}
		return 0, nil, newProtocolError(ErrProtocol, err)
	}
	defer func() { _ = response.Body.Close() }()
	if !boundedResponseHeaders(response.Header, client.limits.MaxResponseHeaderBytes) {
		return 0, nil, newProtocolError(ErrProtocol, nil)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return 0, nil, newProtocolError(ErrProtocol, err)
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, client.limits.MaxResponseBytes+1))
	if err != nil || int64(len(encoded)) > client.limits.MaxResponseBytes {
		return 0, nil, newProtocolError(ErrProtocol, err)
	}
	return response.StatusCode, encoded, nil
}
