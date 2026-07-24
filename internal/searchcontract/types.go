package searchcontract

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrInvalid = errors.New("Search Document contract validation failed")

type UnsupportedVersionError struct {
	Major uint64
}

func (failure *UnsupportedVersionError) Error() string {
	return fmt.Sprintf(
		"Search Document major version v%d is not supported.",
		failure.Major,
	)
}

func (*UnsupportedVersionError) Unwrap() error {
	return ErrInvalid
}

type Document struct {
	Context       string     `json:"@context"`
	ID            string     `json:"@id"`
	Type          string     `json:"@type"`
	SchemaVersion string     `json:"schema_version"`
	Search        Search     `json:"search"`
	State         State      `json:"state"`
	ResultSet     *ResultSet `json:"result_set,omitempty"`
	Links         Links      `json:"links"`
	Metadata      Metadata   `json:"metadata"`
}

type Search struct {
	ID          string  `json:"id"`
	Query       string  `json:"query"`
	CreatedAt   string  `json:"created_at"`
	StartedAt   *string `json:"started_at,omitempty"`
	CompletedAt *string `json:"completed_at,omitempty"`
}

type State struct {
	Status   string    `json:"status"`
	Progress *Progress `json:"progress,omitempty"`
	Question *Question `json:"question,omitempty"`
	Error    *Failure  `json:"error,omitempty"`
	Reason   *string   `json:"reason,omitempty"`
}

type Progress struct {
	Percent int    `json:"percent"`
	Message string `json:"message"`
}

type Question struct {
	ID      string   `json:"id"`
	Prompt  string   `json:"prompt"`
	Options []string `json:"options"`
}

type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type ResultSet struct {
	Type       string      `json:"@type"`
	ResultType string      `json:"result_type"`
	DataSchema *string     `json:"data_schema,omitempty"`
	Items      []Item      `json:"items"`
	Pagination Pagination  `json:"pagination"`
	Links      ResultLinks `json:"links"`
}

type Item struct {
	Type       string          `json:"@type"`
	ID         string          `json:"id"`
	Position   int             `json:"position"`
	ItemType   string          `json:"type"`
	Name       string          `json:"name"`
	Summary    string          `json:"summary"`
	DataSchema *string         `json:"data_schema,omitempty"`
	Data       json.RawMessage `json:"data"`
}

type Pagination struct {
	Kind           string  `json:"kind"`
	Page           *int    `json:"page,omitempty"`
	PageSize       int     `json:"page_size"`
	Returned       int     `json:"returned"`
	Total          *int    `json:"total"`
	HasMore        bool    `json:"has_more"`
	NextCursor     *string `json:"next_cursor,omitempty"`
	PreviousCursor *string `json:"previous_cursor,omitempty"`
	NextPage       *int    `json:"next_page,omitempty"`
	PreviousPage   *int    `json:"previous_page,omitempty"`
}

type ResultLinks struct {
	Self     string  `json:"self"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
}

type Links struct {
	Self    string `json:"self"`
	Schema  string `json:"schema"`
	Context string `json:"context"`
}

type Metadata struct {
	Revision    int             `json:"revision"`
	GeneratedAt string          `json:"generated_at"`
	Extensions  json.RawMessage `json:"extensions"`
}
