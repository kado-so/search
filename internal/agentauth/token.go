package agentauth

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kado-so/search/internal/agentkey"
	"github.com/kado-so/search/internal/keystore"
)

const privateKeyJWTAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

type privateKeyJWTHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type privateKeyJWTClaims struct {
	Audience  string `json:"aud"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	Issuer    string `json:"iss"`
	JTI       string `json:"jti"`
	Subject   string `json:"sub"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

type tokenJWKS struct {
	Keys []tokenSigningJWK `json:"keys"`
}

type tokenSigningJWK struct {
	Algorithm string `json:"alg"`
	Curve     string `json:"crv"`
	KeyID     string `json:"kid"`
	KeyType   string `json:"kty"`
	Use       string `json:"use"`
	X         string `json:"x"`
}

type accessTokenHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type accessTokenClaims struct {
	Issuer         string `json:"iss"`
	Subject        string `json:"sub"`
	Audience       string `json:"aud"`
	ClientID       string `json:"client_id"`
	PrincipalType  string `json:"principal_type"`
	AgentSessionID string `json:"agent_session_id"`
	SessionMode    string `json:"session_mode"`
	Scope          string `json:"scope"`
	IssuedAt       int64  `json:"iat"`
	NotBefore      int64  `json:"nbf"`
	ExpiresAt      int64  `json:"exp"`
	JTI            string `json:"jti"`
}

var tokenResponseFields = []string{"access_token", "token_type", "expires_in", "scope"}
var tokenJWKSFields = []string{"keys"}
var tokenSigningJWKFields = []string{"alg", "crv", "kid", "kty", "use", "x"}
var accessTokenHeaderFields = []string{"alg", "kid", "typ"}
var accessTokenClaimFields = []string{
	"iss",
	"sub",
	"aud",
	"client_id",
	"principal_type",
	"agent_session_id",
	"session_mode",
	"scope",
	"iat",
	"nbf",
	"exp",
	"jti",
}
var signingKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// AcquireToken performs deterministic admission with a fresh memory-only
// session key, proves possession, and exchanges a private_key_jwt assertion
// for a verified short-lived bearer token.
func (client *Client) AcquireToken(
	ctx context.Context,
	store keystore.Store,
	request Request,
) (SessionToken, error) {
	if request.Mode != AuthenticateOnly && request.Mode != CreateIfMissing {
		return SessionToken{}, newProtocolError(ErrProtocol, nil)
	}
	if request.Mode == AuthenticateOnly {
		if _, err := client.AuthenticateOrEnroll(ctx, store, request); err != nil {
			return SessionToken{}, err
		}
	}
	metadata, err := client.Discover(ctx)
	if err != nil {
		return SessionToken{}, err
	}
	if request.Mode == CreateIfMissing && !metadata.AutonomousEnrollment {
		return SessionToken{}, newProtocolError(ErrAuthentication, nil)
	}
	management, err := loadOrCreateManagementSigner(store, request.Mode)
	if err != nil {
		return SessionToken{}, err
	}
	session, err := agentkey.GenerateSessionSigner()
	if err != nil {
		return SessionToken{}, newProtocolError(ErrAuthentication, err)
	}
	authorization, err := client.authorizeSession(ctx, metadata, management, session)
	if err != nil {
		return SessionToken{}, err
	}
	return client.exchangeToken(ctx, metadata, authorization, session)
}

func (client *Client) exchangeToken(
	ctx context.Context,
	metadata Metadata,
	authorization sessionAuthorization,
	session crypto.Signer,
) (SessionToken, error) {
	jti, err := client.randomBase64URL(16)
	if err != nil {
		return SessionToken{}, err
	}
	now := client.now().UTC()
	assertion, err := signCompactJWT(
		client.random,
		session,
		privateKeyJWTHeader{
			Algorithm: "EdDSA",
			KeyID:     authorization.SessionCredentialID,
			Type:      "JWT",
		},
		privateKeyJWTClaims{
			Audience:  metadata.TokenEndpoint,
			ExpiresAt: now.Add(client.limits.MaxProofLifetime).Unix(),
			IssuedAt:  now.Unix(),
			Issuer:    authorization.ClientID,
			JTI:       jti,
			Subject:   authorization.ClientID,
		},
	)
	if err != nil {
		return SessionToken{}, err
	}
	form := url.Values{
		"client_assertion":      {assertion},
		"client_assertion_type": {privateKeyJWTAssertionType},
		"client_id":             {authorization.ClientID},
		"grant_type":            {"client_credentials"},
		"resource":              {metadata.Resource},
		"scope":                 {strings.Join(authorization.GrantedScopes, " ")},
	}
	body := []byte(form.Encode())
	defer clear(body)
	status, encoded, err := client.doJSON(
		ctx,
		http.MethodPost,
		metadata.TokenEndpoint,
		body,
		"application/x-www-form-urlencoded",
	)
	if err != nil {
		return SessionToken{}, err
	}
	if status != http.StatusOK {
		return SessionToken{}, classifyTokenFailure(status, encoded)
	}
	var response tokenResponse
	if err := decodeExactJSONObject(encoded, &response, tokenResponseFields); err != nil {
		return SessionToken{}, newProtocolError(ErrProtocol, err)
	}
	sessionExpiresAt, err := time.Parse(time.RFC3339Nano, authorization.SessionExpiresAt)
	if err != nil {
		return SessionToken{}, newProtocolError(ErrProtocol, err)
	}
	if response.TokenType != "Bearer" ||
		response.Scope != strings.Join(authorization.GrantedScopes, " ") ||
		response.ExpiresIn <= 0 ||
		response.ExpiresIn > int64(client.limits.MaxAccessTokenLifetime/time.Second) ||
		response.AccessToken == "" ||
		len(response.AccessToken) > 32*1024 ||
		strings.ContainsAny(response.AccessToken, " \t\r\n") {
		return SessionToken{}, newProtocolError(ErrProtocol, nil)
	}
	claims, err := client.verifyAccessToken(
		ctx,
		metadata,
		authorization,
		response,
		sessionExpiresAt,
	)
	if err != nil {
		return SessionToken{}, err
	}
	return SessionToken{
		PrincipalID:         authorization.PrincipalID,
		CredentialID:        authorization.CredentialID,
		ClientID:            authorization.ClientID,
		SessionID:           authorization.SessionID,
		SessionCredentialID: authorization.SessionCredentialID,
		SessionExpiresAt:    sessionExpiresAt,
		AccessExpiresAt:     time.Unix(claims.ExpiresAt, 0).UTC(),
		Scopes:              append([]string(nil), authorization.GrantedScopes...),
		accessToken:         response.AccessToken,
	}, nil
}

func signCompactJWT(
	random cryptoRandReader,
	signer crypto.Signer,
	header privateKeyJWTHeader,
	claims privateKeyJWTClaims,
) (string, error) {
	protected, err := json.Marshal(header)
	if err != nil {
		return "", newProtocolError(ErrProtocol, err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", newProtocolError(ErrProtocol, err)
	}
	protectedText := rawBase64URL.EncodeToString(protected)
	payloadText := rawBase64URL.EncodeToString(payload)
	signingInput := protectedText + "." + payloadText
	signature, err := signer.Sign(random, []byte(signingInput), crypto.Hash(0))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return "", newProtocolError(ErrAuthentication, err)
	}
	return signingInput + "." + rawBase64URL.EncodeToString(signature), nil
}

type cryptoRandReader interface {
	Read([]byte) (int, error)
}

func (client *Client) verifyAccessToken(
	ctx context.Context,
	metadata Metadata,
	authorization sessionAuthorization,
	response tokenResponse,
	sessionExpiresAt time.Time,
) (accessTokenClaims, error) {
	parts := strings.Split(response.AccessToken, ".")
	if len(parts) != 3 {
		return accessTokenClaims{}, newProtocolError(ErrProtocol, nil)
	}
	protected, err := decodeBase64URL(parts[0], 2, 4*1024)
	if err != nil {
		return accessTokenClaims{}, err
	}
	payload, err := decodeBase64URL(parts[1], 2, 8*1024)
	if err != nil {
		return accessTokenClaims{}, err
	}
	signature, err := decodeBase64URL(parts[2], ed25519.SignatureSize, ed25519.SignatureSize)
	if err != nil {
		return accessTokenClaims{}, err
	}
	var header accessTokenHeader
	if err := decodeExactJSONObject(protected, &header, accessTokenHeaderFields); err != nil ||
		header.Algorithm != "EdDSA" ||
		header.Type != "at+jwt" ||
		!signingKeyIDPattern.MatchString(header.KeyID) {
		return accessTokenClaims{}, newProtocolError(ErrProtocol, err)
	}
	var claims accessTokenClaims
	if err := decodeExactJSONObject(payload, &claims, accessTokenClaimFields); err != nil {
		return accessTokenClaims{}, newProtocolError(ErrProtocol, err)
	}
	key, err := client.fetchTokenSigningKey(ctx, metadata.JWKSURI, header.KeyID)
	if err != nil {
		return accessTokenClaims{}, err
	}
	public, err := rawBase64URL.DecodeString(key.X)
	if err != nil || len(public) != ed25519.PublicKeySize ||
		!ed25519.Verify(
			ed25519.PublicKey(public),
			[]byte(parts[0]+"."+parts[1]),
			signature,
		) {
		return accessTokenClaims{}, newProtocolError(ErrProtocol, err)
	}
	now := client.now().UTC()
	if claims.Issuer != metadata.Issuer ||
		claims.Subject != authorization.PrincipalID ||
		claims.Audience != metadata.Resource ||
		claims.ClientID != authorization.ClientID ||
		claims.PrincipalType != "agent" ||
		claims.AgentSessionID != authorization.SessionID ||
		claims.SessionMode != "autonomous" ||
		claims.Scope != response.Scope ||
		claims.NotBefore != claims.IssuedAt ||
		claims.ExpiresAt <= claims.IssuedAt ||
		claims.ExpiresAt-claims.IssuedAt != response.ExpiresIn ||
		claims.IssuedAt > now.Add(client.limits.MaxClockSkew).Unix() ||
		claims.NotBefore > now.Add(client.limits.MaxClockSkew).Unix() ||
		claims.ExpiresAt <= now.Add(-client.limits.MaxClockSkew).Unix() ||
		claims.ExpiresAt > sessionExpiresAt.Unix() ||
		claims.ExpiresAt-claims.IssuedAt >
			int64(client.limits.MaxAccessTokenLifetime/time.Second) ||
		!validAccessTokenJTI(claims.JTI) {
		return accessTokenClaims{}, newProtocolError(ErrProtocol, nil)
	}
	return claims, nil
}

func (client *Client) fetchTokenSigningKey(
	ctx context.Context,
	endpoint string,
	keyID string,
) (tokenSigningJWK, error) {
	status, encoded, err := client.doJSON(ctx, http.MethodGet, endpoint, nil, "")
	if err != nil {
		return tokenSigningJWK{}, err
	}
	if status != http.StatusOK {
		return tokenSigningJWK{}, newProtocolError(ErrProtocol, nil)
	}
	var document tokenJWKS
	if err := decodeExactJSONObject(encoded, &document, tokenJWKSFields); err != nil ||
		len(document.Keys) == 0 ||
		len(document.Keys) > 16 {
		return tokenSigningJWK{}, newProtocolError(ErrProtocol, err)
	}
	seen := make(map[string]struct{}, len(document.Keys))
	var matched *tokenSigningJWK
	for index := range document.Keys {
		key := &document.Keys[index]
		keyBytes, marshalErr := json.Marshal(key)
		if marshalErr != nil {
			return tokenSigningJWK{}, newProtocolError(ErrProtocol, marshalErr)
		}
		var exact tokenSigningJWK
		if err := decodeExactJSONObject(keyBytes, &exact, tokenSigningJWKFields); err != nil ||
			exact.Algorithm != "EdDSA" ||
			exact.Curve != "Ed25519" ||
			exact.KeyType != "OKP" ||
			exact.Use != "sig" ||
			!signingKeyIDPattern.MatchString(exact.KeyID) {
			return tokenSigningJWK{}, newProtocolError(ErrProtocol, err)
		}
		if _, duplicate := seen[exact.KeyID]; duplicate {
			return tokenSigningJWK{}, newProtocolError(ErrProtocol, nil)
		}
		seen[exact.KeyID] = struct{}{}
		if _, err := decodeBase64URL(exact.X, ed25519.PublicKeySize, ed25519.PublicKeySize); err != nil {
			return tokenSigningJWK{}, err
		}
		if exact.KeyID == keyID {
			value := exact
			matched = &value
		}
	}
	if matched == nil {
		return tokenSigningJWK{}, newProtocolError(ErrProtocol, nil)
	}
	return *matched, nil
}

func classifyTokenFailure(status int, encoded []byte) error {
	var response struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := decodeExactJSONObject(
		encoded,
		&response,
		[]string{"error", "error_description"},
	); err != nil {
		return newProtocolError(ErrAuthentication, err)
	}
	if response.Error == "" ||
		response.ErrorDescription == "" ||
		len(response.ErrorDescription) > 1024 {
		return newProtocolError(ErrAuthentication, nil)
	}
	switch response.Error {
	case "rate_limited":
		if status == http.StatusTooManyRequests {
			return newProtocolError(ErrTokenRateLimited, nil)
		}
	case "invalid_client":
		if status == http.StatusUnauthorized {
			return newProtocolError(ErrAuthentication, nil)
		}
	case "invalid_request", "invalid_scope", "invalid_target", "unsupported_grant_type":
		if status == http.StatusBadRequest {
			return newProtocolError(ErrAuthentication, nil)
		}
	}
	return newProtocolError(ErrAuthentication, nil)
}

func validAccessTokenJTI(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
