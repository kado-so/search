package protocoltelemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ingestionPath = "api/product-analytics/protocol-command"

type authorizationSource interface {
	Authorization(context.Context, bool) (string, error)
}

type reporter interface {
	Report(context.Context, Event) error
}

type client struct {
	endpoint      *url.URL
	httpClient    *http.Client
	authorization authorizationSource
}

func newClient(base *url.URL, httpClient *http.Client, authorization authorizationSource) (*client, error) {
	if base == nil || base.Scheme != "https" || base.Hostname() == "" ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" || authorization == nil {
		return nil, errors.New("invalid telemetry client")
	}
	endpoint := *base
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/") + "/" + ingestionPath
	endpoint.RawPath = ""
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cloned := *httpClient
	cloned.Jar = nil
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("telemetry redirect rejected")
	}
	return &client{endpoint: &endpoint, httpClient: &cloned, authorization: authorization}, nil
}

func (client *client) Report(ctx context.Context, event Event) error {
	if err := validateEvent(event); err != nil {
		return err
	}
	body, err := json.Marshal(event)
	if err != nil || len(body) > 4096 {
		return errors.New("invalid telemetry event")
	}
	defer clear(body)
	for attempt := 0; attempt < 2; attempt++ {
		authorization, err := client.authorization.Authorization(ctx, attempt == 1)
		if err != nil || authorization == "" || strings.ContainsAny(authorization, "\r\n") {
			return errors.New("telemetry authentication failed")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", authorization)
		response, err := client.httpClient.Do(request)
		if err != nil {
			return err
		}
		_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4097))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			return errors.New("telemetry response failed")
		}
		if response.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return errors.New("telemetry request rejected")
		}
		return nil
	}
	return errors.New("telemetry authentication failed")
}

func validateEvent(event Event) error {
	if event.SchemaVersion != SchemaVersion ||
		(event.Phase != PhaseStarted && event.Phase != PhaseFinished) ||
		(event.Protocol != ProtocolMCP && event.Protocol != ProtocolA2A) ||
		event.AttemptID == "" || len(event.AttemptID) > 64 ||
		!safeCommand(event.Command) ||
		event.StartedAt == "" {
		return errors.New("invalid telemetry event")
	}
	if event.TargetKind != "" && event.TargetKind != "endpoint" &&
		event.TargetKind != "agent_card" && event.TargetKind != "session" {
		return errors.New("invalid telemetry target kind")
	}
	if event.Target != "" && normalizeTarget(event.Target) != event.Target {
		return errors.New("invalid telemetry target")
	}
	if _, err := time.Parse(time.RFC3339Nano, event.StartedAt); err != nil {
		return errors.New("invalid telemetry start time")
	}
	if event.Phase == PhaseStarted && (event.DurationMS != nil || event.ExitCode != nil || event.Success != nil) {
		return errors.New("invalid started telemetry event")
	}
	if event.Phase == PhaseFinished && (event.DurationMS == nil || event.ExitCode == nil || event.Success == nil) {
		return errors.New("invalid finished telemetry event")
	}
	if event.DurationMS != nil && *event.DurationMS < 0 {
		return errors.New("invalid telemetry duration")
	}
	if event.ExitCode != nil && event.Success != nil && *event.Success != (*event.ExitCode == 0) {
		return errors.New("invalid telemetry outcome")
	}
	return nil
}

func safeCommand(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '.') {
			return false
		}
	}
	return true
}
