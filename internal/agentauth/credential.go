package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/kado-so/search/internal/agentkey"
	"github.com/kado-so/search/internal/keystore"
)

const (
	credentialProofType       = "agent-credential+jws"
	credentialStatusOperation = "credential-status"
	credentialRevokeOperation = "revoke-current-credential"
)

type credentialPayload struct {
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	Issuer    string `json:"issuer"`
	JTI       string `json:"jti"`
	Operation string `json:"operation"`
	Version   string `json:"version"`
}

type credentialResponse struct {
	Status       string `json:"status"`
	PrincipalID  string `json:"principal_id"`
	CredentialID string `json:"credential_id"`
	ClientID     string `json:"client_id"`
}

var credentialResponseFields = []string{
	"status",
	"principal_id",
	"credential_id",
	"client_id",
}

// CredentialStatus reports the server-authoritative state of the current
// installation credential. It does not create a key when none is installed.
func (client *Client) CredentialStatus(
	ctx context.Context,
	store keystore.Store,
) (CredentialStatus, error) {
	stored, err := loadStoredManagementSigner(store)
	if err != nil {
		return CredentialStatus{}, err
	}
	defer stored.Destroy()
	return client.manageCredential(ctx, stored.Signer(), credentialStatusOperation)
}

// RevokeCurrentCredential permanently revokes the selected agent identity and
// deletes its local management key only after the server confirms the revoked
// state. A missing local key is an idempotent successful no-op.
func (client *Client) RevokeCurrentCredential(
	ctx context.Context,
	store keystore.Store,
) (CredentialStatus, error) {
	stored, err := loadStoredManagementSigner(store)
	if errors.Is(err, ErrCredentialNotFound) {
		return CredentialStatus{Status: StatusNotConfigured}, nil
	}
	if err != nil {
		return CredentialStatus{}, err
	}
	defer stored.Destroy()
	status, err := client.manageCredential(ctx, stored.Signer(), credentialRevokeOperation)
	if err != nil {
		return CredentialStatus{}, err
	}
	deleted, err := stored.DeleteIfCurrent(store)
	if err != nil {
		return CredentialStatus{}, newProtocolError(ErrCredentialCleanup, err)
	}
	if !deleted {
		return CredentialStatus{}, newProtocolError(ErrCredentialChanged, nil)
	}
	return status, nil
}

func loadStoredManagementSigner(
	store keystore.Store,
) (*agentkey.StoredManagementSigner, error) {
	if store == nil {
		return nil, newProtocolError(ErrCredentialNotFound, nil)
	}
	management, err := agentkey.LoadStoredManagementSigner(store)
	if errors.Is(err, keystore.ErrNotFound) {
		return nil, newProtocolError(ErrCredentialNotFound, err)
	}
	if err != nil {
		return nil, newProtocolError(ErrAuthentication, err)
	}
	return management, nil
}

func (client *Client) manageCredential(
	ctx context.Context,
	management *agentkey.ManagementSigner,
	operation string,
) (CredentialStatus, error) {
	if operation != credentialStatusOperation && operation != credentialRevokeOperation {
		return CredentialStatus{}, newProtocolError(ErrProtocol, nil)
	}
	metadata, err := client.Discover(ctx)
	if err != nil {
		return CredentialStatus{}, err
	}
	managementJWK, err := publicJWK(management)
	if err != nil {
		return CredentialStatus{}, err
	}
	nonce, err := client.fetchNonce(ctx, metadata.NonceEndpoint)
	if err != nil {
		return CredentialStatus{}, err
	}
	jti, err := client.randomBase64URL(16)
	if err != nil {
		return CredentialStatus{}, err
	}
	now := client.now().UTC()
	payloadBytes, err := json.Marshal(credentialPayload{
		ExpiresAt: now.Add(client.limits.MaxProofLifetime).Unix(),
		IssuedAt:  now.Unix(),
		Issuer:    metadata.Issuer,
		JTI:       jti,
		Operation: operation,
		Version:   ProtocolVersion,
	})
	if err != nil {
		return CredentialStatus{}, newProtocolError(ErrProtocol, err)
	}
	proof, err := signFlattenedJWS(client.random, management, payloadBytes, protectedHeader{
		Type:  credentialProofType,
		Alg:   "EdDSA",
		JWK:   &managementJWK,
		Nonce: nonce,
		URL:   metadata.CredentialEndpoint,
	})
	if err != nil {
		return CredentialStatus{}, err
	}
	proofBytes, err := json.Marshal(proof)
	if err != nil {
		return CredentialStatus{}, newProtocolError(ErrProtocol, err)
	}
	statusCode, responseBytes, err := client.doJSON(
		ctx,
		http.MethodPost,
		metadata.CredentialEndpoint,
		proofBytes,
		"application/jose+json",
	)
	if err != nil {
		return CredentialStatus{}, err
	}
	if statusCode != http.StatusOK {
		return CredentialStatus{}, classifyCredentialFailure(statusCode, responseBytes)
	}
	var response credentialResponse
	if err := decodeExactJSONObject(
		responseBytes,
		&response,
		credentialResponseFields,
	); err != nil {
		return CredentialStatus{}, newProtocolError(ErrProtocol, err)
	}
	validStatus := response.Status == StatusActive || response.Status == StatusRevoked
	if operation == credentialRevokeOperation {
		validStatus = response.Status == StatusRevoked
	}
	if !validStatus ||
		!validIdentifier(response.PrincipalID) ||
		!validIdentifier(response.CredentialID) ||
		!validIdentifier(response.ClientID) {
		return CredentialStatus{}, newProtocolError(ErrProtocol, nil)
	}
	return CredentialStatus{
		Status:       response.Status,
		PrincipalID:  response.PrincipalID,
		CredentialID: response.CredentialID,
		ClientID:     response.ClientID,
	}, nil
}

func classifyCredentialFailure(status int, encoded []byte) error {
	var response errorResponse
	if err := decodeExactJSONObject(encoded, &response, errorResponseFields); err != nil {
		return newProtocolError(ErrAuthentication, err)
	}
	if response.Error == "" ||
		response.ErrorDescription == "" ||
		len(response.ErrorDescription) > 1024 {
		return newProtocolError(ErrAuthentication, nil)
	}
	if response.Error == "credential_revoked" &&
		(status == http.StatusUnauthorized || status == http.StatusForbidden) {
		return newProtocolError(ErrCredentialRevoked, nil)
	}
	return newProtocolError(ErrAuthentication, nil)
}
