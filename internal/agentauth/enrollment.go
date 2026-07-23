package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/kado-so/search/internal/agentkey"
	"github.com/kado-so/search/internal/keystore"
)

type enrollmentPayload struct {
	ClientNonce     string `json:"client_nonce"`
	CreateIfMissing bool   `json:"create_if_missing"`
	ExpiresAt       int64  `json:"exp"`
	IssuedAt        int64  `json:"iat"`
	Issuer          string `json:"issuer"`
	JTI             string `json:"jti"`
	Operation       string `json:"operation"`
	Version         string `json:"version"`
}

type successResponse struct {
	Status                  string `json:"status"`
	Created                 bool   `json:"created"`
	PrincipalID             string `json:"principal_id"`
	CredentialID            string `json:"credential_id"`
	ClientID                string `json:"client_id"`
	TokenEndpoint           string `json:"token_endpoint"`
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
}

type errorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Retryable        bool   `json:"retryable"`
}

var successResponseFields = []string{
	"status",
	"created",
	"principal_id",
	"credential_id",
	"client_id",
	"token_endpoint",
	"token_endpoint_auth_method",
}

var errorResponseFields = []string{"error", "error_description", "retryable"}

func (client *Client) AuthenticateOrEnroll(
	ctx context.Context,
	store keystore.Store,
	request Request,
) (Result, error) {
	if request.Mode != AuthenticateOnly && request.Mode != CreateIfMissing {
		return Result{}, newProtocolError(ErrProtocol, nil)
	}
	metadata, err := client.Discover(ctx)
	if err != nil {
		return Result{}, err
	}
	if request.Mode == CreateIfMissing && !metadata.AutonomousEnrollment {
		return Result{}, newProtocolError(ErrAuthentication, nil)
	}
	management, err := loadOrCreateManagementSigner(store, request.Mode)
	if err != nil {
		return Result{}, err
	}
	managementJWK, err := publicJWK(management)
	if err != nil {
		return Result{}, err
	}
	nonce, err := client.fetchNonce(ctx, metadata.NonceEndpoint)
	if err != nil {
		return Result{}, err
	}
	clientNonce, err := client.randomBase64URL(16)
	if err != nil {
		return Result{}, err
	}
	jti, err := client.randomBase64URL(16)
	if err != nil {
		return Result{}, err
	}
	now := client.now().UTC()
	payloadBytes, err := json.Marshal(enrollmentPayload{
		ClientNonce:     clientNonce,
		CreateIfMissing: request.Mode == CreateIfMissing,
		ExpiresAt:       now.Add(client.limits.MaxProofLifetime).Unix(),
		IssuedAt:        now.Unix(),
		Issuer:          metadata.Issuer,
		JTI:             jti,
		Operation:       enrollmentOperation,
		Version:         ProtocolVersion,
	})
	if err != nil {
		return Result{}, newProtocolError(ErrProtocol, err)
	}
	proof, err := signFlattenedJWS(client.random, management, payloadBytes, protectedHeader{
		Type:  enrollmentProofType,
		Alg:   "EdDSA",
		JWK:   &managementJWK,
		Nonce: nonce,
		URL:   metadata.EnrollmentEndpoint,
	})
	if err != nil {
		return Result{}, err
	}
	proofBytes, err := json.Marshal(proof)
	if err != nil {
		return Result{}, newProtocolError(ErrProtocol, err)
	}
	status, responseBytes, err := client.doJSON(
		ctx,
		http.MethodPost,
		metadata.EnrollmentEndpoint,
		proofBytes,
		"application/jose+json",
	)
	if err != nil {
		return Result{}, err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return Result{}, classifyEnrollmentFailure(status, responseBytes)
	}
	var response successResponse
	if err := decodeExactJSONObject(responseBytes, &response, successResponseFields); err != nil {
		return Result{}, newProtocolError(ErrProtocol, err)
	}
	if response.Status != "active" ||
		(status == http.StatusOK && response.Created) ||
		(status == http.StatusCreated && !response.Created) ||
		!validIdentifier(response.PrincipalID) ||
		!validIdentifier(response.CredentialID) ||
		!validIdentifier(response.ClientID) ||
		response.TokenEndpoint != metadata.TokenEndpoint ||
		response.TokenEndpointAuthMethod != "private_key_jwt" {
		return Result{}, newProtocolError(ErrProtocol, nil)
	}
	return Result{
		Created:                 response.Created,
		PrincipalID:             response.PrincipalID,
		CredentialID:            response.CredentialID,
		ClientID:                response.ClientID,
		TokenEndpoint:           response.TokenEndpoint,
		TokenEndpointAuthMethod: response.TokenEndpointAuthMethod,
	}, nil
}

func loadOrCreateManagementSigner(
	store keystore.Store,
	mode EnrollmentMode,
) (*agentkey.ManagementSigner, error) {
	if store == nil {
		return nil, newProtocolError(ErrCredentialNotFound, nil)
	}
	signer, err := agentkey.LoadManagementSigner(store)
	if err == nil {
		return signer, nil
	}
	if !errors.Is(err, keystore.ErrNotFound) {
		return nil, newProtocolError(ErrAuthentication, err)
	}
	if mode == AuthenticateOnly {
		return nil, newProtocolError(ErrCredentialNotFound, err)
	}
	signer, _, err = agentkey.LoadOrCreateManagementSigner(store)
	if err != nil {
		return nil, newProtocolError(ErrAuthentication, err)
	}
	return signer, nil
}

func classifyEnrollmentFailure(status int, encoded []byte) error {
	var response errorResponse
	if err := decodeExactJSONObject(encoded, &response, errorResponseFields); err != nil {
		return newProtocolError(ErrAuthentication, err)
	}
	if response.Error == "" ||
		response.ErrorDescription == "" ||
		len(response.ErrorDescription) > 1024 {
		return newProtocolError(ErrAuthentication, nil)
	}
	switch response.Error {
	case "agent_not_found":
		if status == http.StatusNotFound {
			return newProtocolError(ErrAgentNotFound, nil)
		}
	case "admission_required":
		if status == http.StatusForbidden {
			return newProtocolError(ErrAdmissionRequired, nil)
		}
	}
	return newProtocolError(ErrAuthentication, nil)
}

func (client *Client) randomBase64URL(size int) (string, error) {
	random := make([]byte, size)
	if _, err := io.ReadFull(client.random, random); err != nil {
		return "", newProtocolError(ErrAuthentication, err)
	}
	return rawBase64URL.EncodeToString(random), nil
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	return !strings.ContainsFunc(value, func(character rune) bool {
		return character > unicode.MaxASCII ||
			!(unicode.IsLetter(character) ||
				unicode.IsDigit(character) ||
				character == '_' ||
				character == '-')
	})
}
