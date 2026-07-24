package searchclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/kado-so/search/internal/diagnostic"
)

type documentEnvelope struct {
	SchemaVersion string          `json:"schema_version"`
	Search        json.RawMessage `json:"search"`
	State         json.RawMessage `json:"state"`
	ResultSet     json.RawMessage `json:"result_set"`
	Links         json.RawMessage `json:"links"`
}

type searchEnvelope struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

type stateEnvelope struct {
	Status   string          `json:"status"`
	Question json.RawMessage `json:"question"`
	Error    json.RawMessage `json:"error"`
}

type questionEnvelope struct {
	ID      string   `json:"id"`
	Prompt  string   `json:"prompt"`
	Options []string `json:"options"`
}

type failureEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type linksEnvelope struct {
	Self string `json:"self"`
}

type resultSetEnvelope struct {
	Links json.RawMessage `json:"links"`
}

type resultLinksEnvelope struct {
	Self     string  `json:"self"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
}

type decodedEnvelope struct {
	SchemaVersion string
	SearchID      string
	Query         string
	Status        string
	Self          string
	ResultSelf    string
	Next          string
	Previous      string
	Question      *Question
	Failure       *Failure
}

func decodeEnvelope(encoded []byte) (decodedEnvelope, error) {
	if err := rejectDuplicateMembers(encoded); err != nil {
		return decodedEnvelope{}, err
	}
	var document documentEnvelope
	if err := decodeOne(encoded, &document); err != nil ||
		document.SchemaVersion != SchemaVersion ||
		len(document.Search) == 0 ||
		len(document.State) == 0 ||
		len(document.Links) == 0 {
		return decodedEnvelope{}, ErrProtocol
	}

	var search searchEnvelope
	var state stateEnvelope
	var links linksEnvelope
	if err := decodeOne(document.Search, &search); err != nil ||
		!validOpaqueID(search.ID) ||
		!validPublicText(search.Query, 2_000) {
		return decodedEnvelope{}, ErrProtocol
	}
	if err := decodeOne(document.State, &state); err != nil ||
		!validState(state.Status) {
		return decodedEnvelope{}, ErrProtocol
	}
	if err := decodeOne(document.Links, &links); err != nil || links.Self == "" {
		return decodedEnvelope{}, ErrProtocol
	}
	output := decodedEnvelope{
		SchemaVersion: document.SchemaVersion,
		SearchID:      search.ID,
		Query:         search.Query,
		Status:        state.Status,
		Self:          links.Self,
	}

	switch state.Status {
	case StatusNeedsInput:
		var question questionEnvelope
		if err := decodeOne(state.Question, &question); err != nil ||
			!validOpaqueID(question.ID) ||
			!validPublicText(question.Prompt, 1_000) ||
			len(question.Options) == 0 ||
			len(question.Options) > 20 {
			return decodedEnvelope{}, ErrProtocol
		}
		for _, option := range question.Options {
			if !validPublicText(option, 200) {
				return decodedEnvelope{}, ErrProtocol
			}
		}
		output.Question = &Question{
			ID:      question.ID,
			Prompt:  question.Prompt,
			Options: append([]string(nil), question.Options...),
		}
	case StatusFailed:
		var failure failureEnvelope
		if err := decodeOne(state.Error, &failure); err != nil ||
			failure.Code == "" ||
			len(failure.Code) > 128 ||
			!validPublicText(failure.Message, 1_000) {
			return decodedEnvelope{}, ErrProtocol
		}
		output.Failure = &Failure{
			Code:      boundedCode(failure.Code),
			Message:   diagnostic.TerminalSafeText(failure.Message, 280),
			Retryable: failure.Retryable,
		}
	case StatusComplete:
		if len(document.ResultSet) == 0 {
			return decodedEnvelope{}, ErrProtocol
		}
		var resultSet resultSetEnvelope
		if err := decodeOne(document.ResultSet, &resultSet); err != nil ||
			len(resultSet.Links) == 0 {
			return decodedEnvelope{}, ErrProtocol
		}
		var resultLinks resultLinksEnvelope
		if err := decodeOne(resultSet.Links, &resultLinks); err != nil ||
			resultLinks.Self == "" {
			return decodedEnvelope{}, ErrProtocol
		}
		output.ResultSelf = resultLinks.Self
		if resultLinks.Next != nil {
			output.Next = *resultLinks.Next
		}
		if resultLinks.Previous != nil {
			output.Previous = *resultLinks.Previous
		}
	}
	return output, nil
}

func decodeOne(encoded []byte, destination any) error {
	if len(encoded) == 0 || bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return ErrProtocol
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrProtocol
	}
	return nil
}

func validState(status string) bool {
	switch status {
	case StatusQueued,
		StatusRunning,
		StatusNeedsInput,
		StatusComplete,
		StatusFailed,
		StatusCanceled:
		return true
	default:
		return false
	}
}
