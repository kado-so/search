package agentauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	protectedResourceMetadataPath = ".well-known/oauth-protected-resource"
	authorizationMetadataPath     = ".well-known/oauth-authorization-server"
)

type Client struct {
	base       *url.URL
	resourceID string
	issuer     *url.URL
	httpClient *http.Client
	limits     Limits
	now        func() time.Time
	random     io.Reader
}

type protectedResourceMetadata struct {
	Resource                  string   `json:"resource"`
	AuthorizationServers      []string `json:"authorization_servers"`
	ScopesSupported           []string `json:"scopes_supported"`
	BearerMethodsSupported    []string `json:"bearer_methods_supported"`
	AgentPrincipalMetadataURI string   `json:"agent_principal_metadata_uri"`
}

type authorizationMetadata struct {
	Issuer                    string   `json:"issuer"`
	TokenEndpoint             string   `json:"token_endpoint"`
	JWKSURI                   string   `json:"jwks_uri"`
	TokenAuthMethods          []string `json:"token_endpoint_auth_methods_supported"`
	GrantTypes                []string `json:"grant_types_supported"`
	ProtocolVersions          []string `json:"agent_principal_protocol_versions_supported"`
	NonceEndpoint             string   `json:"agent_nonce_endpoint"`
	EnrollmentEndpoint        string   `json:"agent_enrollment_endpoint"`
	AdmissionEndpoint         string   `json:"agent_admission_endpoint"`
	CredentialEndpoint        string   `json:"agent_credential_endpoint"`
	AutonomousEnrollment      *bool    `json:"agent_autonomous_enrollment_supported"`
	KeyAlgorithms             []string `json:"agent_key_algorithms_supported"`
	JWSAlgorithms             []string `json:"agent_jws_algorithms_supported"`
	AdmissionChallengeTypes   []string `json:"agent_admission_challenge_types_supported"`
	AgentPrincipalMetadataURI string   `json:"agent_principal_metadata_uri"`
}

type agentProtocolMetadata struct {
	Issuer                  string   `json:"issuer"`
	ProtectedResource       string   `json:"protected_resource"`
	ProtocolVersions        []string `json:"agent_principal_protocol_versions_supported"`
	NonceEndpoint           string   `json:"agent_nonce_endpoint"`
	EnrollmentEndpoint      string   `json:"agent_enrollment_endpoint"`
	AdmissionEndpoint       string   `json:"agent_admission_endpoint"`
	CredentialEndpoint      string   `json:"agent_credential_endpoint"`
	AutonomousEnrollment    *bool    `json:"agent_autonomous_enrollment_supported"`
	KeyAlgorithms           []string `json:"agent_key_algorithms_supported"`
	JWSAlgorithms           []string `json:"agent_jws_algorithms_supported"`
	AdmissionChallengeTypes []string `json:"agent_admission_challenge_types_supported"`
}

var protectedResourceMetadataFields = []string{
	"resource",
	"authorization_servers",
	"scopes_supported",
	"bearer_methods_supported",
	"agent_principal_metadata_uri",
}

var authorizationMetadataFields = []string{
	"issuer",
	"token_endpoint",
	"jwks_uri",
	"token_endpoint_auth_methods_supported",
	"grant_types_supported",
	"agent_principal_protocol_versions_supported",
	"agent_nonce_endpoint",
	"agent_enrollment_endpoint",
	"agent_admission_endpoint",
	"agent_credential_endpoint",
	"agent_autonomous_enrollment_supported",
	"agent_key_algorithms_supported",
	"agent_jws_algorithms_supported",
	"agent_admission_challenge_types_supported",
	"agent_principal_metadata_uri",
}

var agentProtocolMetadataFields = []string{
	"issuer",
	"protected_resource",
	"agent_principal_protocol_versions_supported",
	"agent_nonce_endpoint",
	"agent_enrollment_endpoint",
	"agent_admission_endpoint",
	"agent_credential_endpoint",
	"agent_autonomous_enrollment_supported",
	"agent_key_algorithms_supported",
	"agent_jws_algorithms_supported",
	"agent_admission_challenge_types_supported",
}

func NewClient(
	base *url.URL,
	httpClient *http.Client,
	limits Limits,
	random io.Reader,
) (*Client, error) {
	validatedBase, err := validateServiceBase(base)
	if err != nil {
		return nil, newProtocolError(ErrDiscovery, err)
	}
	if err := validateLimits(limits); err != nil {
		return nil, newProtocolError(ErrProtocol, err)
	}
	if random == nil {
		return nil, newProtocolError(ErrProtocol, nil)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cloned := *httpClient
	cloned.Jar = nil
	if cloned.Timeout <= 0 || cloned.Timeout > limits.MaxHTTPTimeout {
		cloned.Timeout = limits.MaxHTTPTimeout
	}
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return ErrRedirect
	}
	issuer := issuerFromBase(validatedBase)
	return &Client{
		base:       validatedBase,
		resourceID: issuer.String(),
		issuer:     issuer,
		httpClient: &cloned,
		limits:     limits,
		now:        time.Now,
		random:     random,
	}, nil
}

func validateLimits(limits Limits) error {
	if limits.MaxResponseBytes < 1024 ||
		limits.MaxResponseBytes > 1024*1024 ||
		limits.MaxClockSkew < 0 ||
		limits.MaxClockSkew > 5*time.Minute ||
		limits.MaxProofLifetime <= 0 ||
		limits.MaxProofLifetime > 60*time.Second ||
		limits.MaxHTTPTimeout <= 0 ||
		limits.MaxHTTPTimeout > 2*time.Minute ||
		limits.MaxChallengeLifetime <= 0 ||
		limits.MaxChallengeLifetime > 10*time.Minute ||
		limits.MaxArgonMemoryKiB < 8 ||
		limits.MaxArgonMemoryKiB > 128*1024 ||
		limits.MaxArgonPasses < 1 ||
		limits.MaxArgonPasses > 4 ||
		limits.MaxArgonParallelism < 1 ||
		limits.MaxArgonParallelism > 4 ||
		limits.MaxArgonAttempts < 1 ||
		limits.MaxArgonAttempts > 10_000 ||
		limits.MaxArgonElapsed <= 0 ||
		limits.MaxArgonElapsed > 10*time.Minute ||
		limits.MaxSessionLifetime <= 0 ||
		limits.MaxSessionLifetime > 24*time.Hour ||
		limits.MaxAccessTokenLifetime <= 0 ||
		limits.MaxAccessTokenLifetime > 5*time.Minute {
		return errors.New("invalid agent authentication limits")
	}
	return nil
}

func (client *Client) Discover(ctx context.Context) (Metadata, error) {
	var protected protectedResourceMetadata
	if err := client.fetchJSON(
		ctx,
		appendEndpoint(client.base, protectedResourceMetadataPath),
		&protected,
		protectedResourceMetadataFields,
	); err != nil {
		return Metadata{}, newProtocolError(ErrDiscovery, err)
	}
	if protected.Resource != client.resourceID ||
		len(protected.AuthorizationServers) != 1 ||
		protected.AuthorizationServers[0] != client.issuer.String() ||
		!containsAll(protected.ScopesSupported, autonomousSearchScopes) ||
		!contains(protected.BearerMethodsSupported, "header") {
		return Metadata{}, newProtocolError(ErrDiscovery, nil)
	}

	var authorization authorizationMetadata
	if err := client.fetchJSON(
		ctx,
		appendEndpoint(client.issuer, authorizationMetadataPath),
		&authorization,
		authorizationMetadataFields,
	); err != nil {
		return Metadata{}, newProtocolError(ErrDiscovery, err)
	}
	if authorization.Issuer != client.issuer.String() ||
		authorization.AutonomousEnrollment == nil ||
		!contains(authorization.ProtocolVersions, ProtocolVersion) ||
		!contains(authorization.KeyAlgorithms, "Ed25519") ||
		!contains(authorization.JWSAlgorithms, "EdDSA") ||
		!contains(authorization.TokenAuthMethods, "private_key_jwt") ||
		!contains(authorization.GrantTypes, "client_credentials") ||
		!contains(authorization.AdmissionChallengeTypes, ProofAlgorithm) {
		return Metadata{}, newProtocolError(ErrDiscovery, nil)
	}
	tokenEndpoint, err := validateAdvertisedEndpoint(authorization.TokenEndpoint, client.issuer)
	if err != nil {
		return Metadata{}, newProtocolError(ErrDiscovery, err)
	}
	jwksURI, err := validateAdvertisedEndpoint(authorization.JWKSURI, client.issuer)
	if err != nil {
		return Metadata{}, newProtocolError(ErrDiscovery, err)
	}
	nonceEndpoint, err := validateAdvertisedEndpoint(authorization.NonceEndpoint, client.issuer)
	if err != nil {
		return Metadata{}, newProtocolError(ErrDiscovery, err)
	}
	enrollmentEndpoint, err := validateAdvertisedEndpoint(
		authorization.EnrollmentEndpoint,
		client.issuer,
	)
	if err != nil {
		return Metadata{}, newProtocolError(ErrDiscovery, err)
	}
	admissionEndpoint, err := validateAdvertisedEndpoint(
		authorization.AdmissionEndpoint,
		client.issuer,
	)
	if err != nil {
		return Metadata{}, newProtocolError(ErrDiscovery, err)
	}
	credentialEndpoint, err := validateAdvertisedEndpoint(
		authorization.CredentialEndpoint,
		client.issuer,
	)
	if err != nil {
		return Metadata{}, newProtocolError(ErrDiscovery, err)
	}
	agentMetadataURI, err := validateAdvertisedEndpoint(
		authorization.AgentPrincipalMetadataURI,
		client.issuer,
	)
	if err != nil ||
		protected.AgentPrincipalMetadataURI != authorization.AgentPrincipalMetadataURI {
		return Metadata{}, newProtocolError(ErrDiscovery, err)
	}

	var extension agentProtocolMetadata
	if err := client.fetchJSON(
		ctx,
		agentMetadataURI.String(),
		&extension,
		agentProtocolMetadataFields,
	); err != nil {
		return Metadata{}, newProtocolError(ErrDiscovery, err)
	}
	if extension.Issuer != client.issuer.String() ||
		extension.ProtectedResource != client.resourceID ||
		extension.AutonomousEnrollment == nil ||
		*extension.AutonomousEnrollment != *authorization.AutonomousEnrollment ||
		!equalStringSets(extension.ProtocolVersions, authorization.ProtocolVersions) ||
		!equalStringSets(extension.KeyAlgorithms, authorization.KeyAlgorithms) ||
		!equalStringSets(extension.JWSAlgorithms, authorization.JWSAlgorithms) ||
		!equalStringSets(
			extension.AdmissionChallengeTypes,
			authorization.AdmissionChallengeTypes,
		) ||
		extension.NonceEndpoint != authorization.NonceEndpoint ||
		extension.EnrollmentEndpoint != authorization.EnrollmentEndpoint ||
		extension.AdmissionEndpoint != authorization.AdmissionEndpoint ||
		extension.CredentialEndpoint != authorization.CredentialEndpoint {
		return Metadata{}, newProtocolError(ErrDiscovery, nil)
	}
	return Metadata{
		Resource:             client.resourceID,
		Issuer:               client.issuer.String(),
		TokenEndpoint:        tokenEndpoint.String(),
		JWKSURI:              jwksURI.String(),
		NonceEndpoint:        nonceEndpoint.String(),
		EnrollmentEndpoint:   enrollmentEndpoint.String(),
		AdmissionEndpoint:    admissionEndpoint.String(),
		CredentialEndpoint:   credentialEndpoint.String(),
		AgentMetadataURI:     agentMetadataURI.String(),
		AutonomousEnrollment: *authorization.AutonomousEnrollment,
	}, nil
}

func (client *Client) fetchNonce(ctx context.Context, endpoint string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
		return "", newProtocolError(ErrProtocol, err)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, ErrRedirect) {
			return "", newProtocolError(ErrRedirect, err)
		}
		return "", newProtocolError(ErrProtocol, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", newProtocolError(ErrProtocol, nil)
	}
	nonce := response.Header.Get("Replay-Nonce")
	if _, err := decodeBase64URL(nonce, 24, 24); err != nil {
		return "", newProtocolError(ErrProtocol, err)
	}
	return nonce, nil
}

func (client *Client) fetchJSON(
	ctx context.Context,
	endpoint string,
	destination any,
	fields []string,
) error {
	status, encoded, err := client.doJSON(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return newProtocolError(ErrProtocol, nil)
	}
	if err := decodeExactJSONObject(encoded, destination, fields); err != nil {
		return newProtocolError(ErrProtocol, err)
	}
	return nil
}

func (client *Client) doJSON(
	ctx context.Context,
	method string,
	endpoint string,
	body []byte,
	contentType string,
) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, ErrRedirect) {
			return 0, nil, newProtocolError(ErrRedirect, err)
		}
		return 0, nil, newProtocolError(ErrProtocol, err)
	}
	defer func() { _ = response.Body.Close() }()
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

func validateServiceBase(input *url.URL) (*url.URL, error) {
	if input == nil {
		return nil, errors.New("service URL is missing")
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
		strings.Contains(cloned.EscapedPath(), "%") ||
		strings.ContainsFunc(cloned.String(), unicode.IsControl) {
		return nil, errors.New("service URL is invalid")
	}
	if cloned.Path == "" {
		cloned.Path = "/"
	}
	if cloned.Path != "/" &&
		(strings.HasSuffix(cloned.Path, "/") || !validCanonicalPath(cloned.Path)) {
		return nil, errors.New("service URL path is invalid")
	}
	if strings.HasSuffix(cloned.Host, ":") {
		return nil, errors.New("service URL port is invalid")
	}
	if port := cloned.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65_535 {
			return nil, errors.New("service URL port is invalid")
		}
	}
	return &cloned, nil
}

func issuerFromBase(base *url.URL) *url.URL {
	issuer := *base
	if issuer.Path == "/" {
		issuer.Path = ""
	}
	return &issuer
}

func appendEndpoint(base *url.URL, segment string) string {
	appended := *base
	appended.Path = strings.TrimSuffix(appended.Path, "/") + "/" + segment
	return appended.String()
}

func validateAdvertisedEndpoint(raw string, issuer *url.URL) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil ||
		parsed.String() != raw ||
		parsed.Scheme != "https" ||
		parsed.Host != issuer.Host ||
		parsed.User != nil ||
		parsed.Opaque != "" ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" ||
		parsed.RawPath != "" ||
		parsed.Path == "" ||
		parsed.Path == "/" ||
		!validCanonicalPath(parsed.Path) ||
		strings.ContainsFunc(raw, unicode.IsControl) {
		return nil, errors.New("advertised endpoint is invalid")
	}
	prefix := strings.TrimSuffix(issuer.Path, "/")
	if prefix != "" && !strings.HasPrefix(parsed.Path, prefix+"/") {
		return nil, errors.New("advertised endpoint escapes issuer path")
	}
	return parsed, nil
}

func validCanonicalPath(path string) bool {
	if !strings.HasPrefix(path, "/") ||
		strings.HasSuffix(path, "/") ||
		strings.Contains(path, "//") ||
		strings.Contains(path, "\\") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, character := range segment {
			if character > unicode.MaxASCII || unicode.IsControl(character) || character == ' ' {
				return false
			}
		}
	}
	return true
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsAll(values, expected []string) bool {
	for _, value := range expected {
		if !contains(values, value) {
			return false
		}
	}
	return true
}

func equalStringSets(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	rightSeen := make(map[string]struct{}, len(right))
	for _, value := range right {
		if _, found := seen[value]; !found {
			return false
		}
		if _, duplicate := rightSeen[value]; duplicate {
			return false
		}
		rightSeen[value] = struct{}{}
	}
	return true
}
