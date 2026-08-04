// Package searchoutput renders validated Search Documents for CLI consumers.
package searchoutput

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/searchcontract"
	"github.com/mattn/go-runewidth"
)

type Mode string

const (
	ModeHuman Mode = "human"
	ModeJSON  Mode = "json"
	ModeJSONL Mode = "jsonl"

	defaultWidth       = 96
	minimumWidth       = 40
	maximumWidth       = 160
	maximumPages       = 100
	maximumHumanItems  = 100
	maximumHumanBytes  = 2 * 1024 * 1024
	maximumJSONLBytes  = 64 * 1024 * 1024
	maximumDataPreview = 512
)

var ErrOutput = errors.New("Search output projection failed")

type Options struct {
	Mode  Mode
	Width int
}

type validatedPage struct {
	encoded  []byte
	document searchcontract.Document
	raw      rawDocument
}

type rawDocument struct {
	Search    json.RawMessage `json:"search"`
	State     json.RawMessage `json:"state"`
	ResultSet *struct {
		Pagination json.RawMessage `json:"pagination"`
		Links      json.RawMessage `json:"links"`
	} `json:"result_set"`
	Metadata json.RawMessage `json:"metadata"`
}

func Render(
	primary []byte,
	pages [][]byte,
	options Options,
) ([]byte, error) {
	if options.Mode == "" {
		options.Mode = ModeHuman
	}
	if options.Width == 0 {
		options.Width = defaultWidth
	}
	if options.Width < minimumWidth || options.Width > maximumWidth {
		return nil, ErrOutput
	}
	validated, err := validatePages(primary, pages)
	if err != nil {
		return nil, err
	}
	switch options.Mode {
	case ModeJSON:
		return append([]byte(nil), validated[0].encoded...), nil
	case ModeHuman:
		return renderHuman(validated, options.Width)
	case ModeJSONL:
		return renderJSONL(validated)
	default:
		return nil, ErrOutput
	}
}

func validatePages(primary []byte, pages [][]byte) ([]validatedPage, error) {
	if len(pages) > maximumPages {
		return nil, ErrOutput
	}
	all := make([][]byte, 0, len(pages)+1)
	all = append(all, primary)
	for index, page := range pages {
		if index == 0 && bytes.Equal(page, primary) {
			continue
		}
		all = append(all, page)
	}
	if len(all) > maximumPages {
		return nil, ErrOutput
	}
	output := make([]validatedPage, 0, len(all))
	for _, encoded := range all {
		document, err := searchcontract.Validate(encoded)
		if err != nil {
			return nil, err
		}
		if document.State.Question != nil || !supportedStatus(document.State.Status) {
			return nil, ErrOutput
		}
		var raw rawDocument
		if err := json.Unmarshal(encoded, &raw); err != nil {
			return nil, ErrOutput
		}
		output = append(output, validatedPage{
			encoded:  append([]byte(nil), encoded...),
			document: document,
			raw:      raw,
		})
	}
	first := output[0].document
	for index, page := range output {
		if page.document.SchemaVersion != first.SchemaVersion ||
			page.document.Search.ID != first.Search.ID ||
			page.document.Search.Query != first.Search.Query {
			return nil, ErrOutput
		}
		if index > 0 && page.document.State.Status != "complete" {
			return nil, ErrOutput
		}
	}
	return output, nil
}

func renderHuman(pages []validatedPage, width int) ([]byte, error) {
	var output bytes.Buffer
	first := pages[0].document
	writeWrapped(&output, "Query: ", first.Search.Query, width)
	writeWrapped(&output, "Status: ", first.State.Status, width)
	switch first.State.Status {
	case "running":
		if first.State.Progress != nil {
			writeWrapped(
				&output,
				fmt.Sprintf("Progress: %d%% — ", first.State.Progress.Percent),
				first.State.Progress.Message,
				width,
			)
		}
	case "failed":
		if first.State.Error != nil {
			writeWrapped(
				&output,
				"Failure: ",
				first.State.Error.Code+" — "+first.State.Error.Message,
				width,
			)
			fmt.Fprintf(&output, "Retryable: %t\n", first.State.Error.Retryable)
		}
	case "canceled":
		if first.State.Reason != nil {
			writeWrapped(&output, "Reason: ", *first.State.Reason, width)
		}
	}
	if first.State.Status != "complete" {
		if output.Len() > maximumHumanBytes {
			return nil, ErrOutput
		}
		return output.Bytes(), nil
	}

	totalItems := 0
	for _, page := range pages {
		if page.document.ResultSet != nil {
			totalItems += len(page.document.ResultSet.Items)
		}
	}
	fmt.Fprintf(&output, "Pages: %d\nResults: %d\n", len(pages), totalItems)
	renderedItems := 0
	for pageIndex, page := range pages {
		resultSet := page.document.ResultSet
		if resultSet == nil {
			return nil, ErrOutput
		}
		output.WriteByte('\n')
		writeWrapped(
			&output,
			"",
			fmt.Sprintf(
				"Page %d — %s pagination, returned %d/%d, total %s, more %t",
				pageIndex+1,
				resultSet.Pagination.Kind,
				resultSet.Pagination.Returned,
				resultSet.Pagination.PageSize,
				totalText(resultSet.Pagination.Total),
				resultSet.Pagination.HasMore,
			),
			width,
		)
		for _, item := range resultSet.Items {
			if renderedItems >= maximumHumanItems {
				continue
			}
			renderedItems++
			writeWrapped(
				&output,
				fmt.Sprintf("%d. ", item.Position),
				item.Name+" ["+item.ItemType+"]",
				width,
			)
			writeWrapped(&output, "   ", item.Summary, width)
			preview := compactPreview(item.Data, maximumDataPreview)
			writeWrapped(&output, "   Data: ", preview, width)
		}
	}
	if omitted := totalItems - renderedItems; omitted > 0 {
		output.WriteByte('\n')
		writeWrapped(
			&output,
			"",
			fmt.Sprintf(
				"%d additional results omitted from human output; use --jsonl.",
				omitted,
			),
			width,
		)
	}
	if output.Len() > maximumHumanBytes {
		return nil, ErrOutput
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func supportedStatus(status string) bool {
	switch status {
	case "queued", "running", "complete", "failed", "canceled":
		return true
	default:
		return false
	}
}

type jsonlSearch struct {
	Kind          string          `json:"kind"`
	ID            string          `json:"@id"`
	SchemaVersion string          `json:"schema_version"`
	Search        json.RawMessage `json:"search"`
	State         json.RawMessage `json:"state"`
	Metadata      json.RawMessage `json:"metadata"`
}

type jsonlItem struct {
	Kind       string          `json:"kind"`
	PageIndex  int             `json:"page_index"`
	SearchID   string          `json:"search_id"`
	ID         string          `json:"id"`
	Position   int             `json:"position"`
	Type       string          `json:"type"`
	Name       string          `json:"name"`
	Summary    string          `json:"summary"`
	DataSchema *string         `json:"data_schema,omitempty"`
	Data       json.RawMessage `json:"data"`
}

type jsonlPagination struct {
	Kind       string          `json:"kind"`
	PageIndex  int             `json:"page_index"`
	SearchID   string          `json:"search_id"`
	Pagination json.RawMessage `json:"pagination"`
	Links      json.RawMessage `json:"links"`
}

func renderJSONL(pages []validatedPage) ([]byte, error) {
	var output bytes.Buffer
	first := pages[0]
	if err := appendJSONLine(&output, jsonlSearch{
		Kind:          "search",
		ID:            first.document.ID,
		SchemaVersion: first.document.SchemaVersion,
		Search:        first.raw.Search,
		State:         first.raw.State,
		Metadata:      first.raw.Metadata,
	}); err != nil {
		return nil, err
	}
	for pageIndex, page := range pages {
		resultSet := page.document.ResultSet
		if resultSet == nil {
			continue
		}
		for _, item := range resultSet.Items {
			if err := appendJSONLine(&output, jsonlItem{
				Kind:       "result",
				PageIndex:  pageIndex + 1,
				SearchID:   page.document.Search.ID,
				ID:         item.ID,
				Position:   item.Position,
				Type:       item.ItemType,
				Name:       item.Name,
				Summary:    item.Summary,
				DataSchema: item.DataSchema,
				Data:       item.Data,
			}); err != nil {
				return nil, err
			}
		}
		if page.raw.ResultSet == nil {
			return nil, ErrOutput
		}
		if err := appendJSONLine(&output, jsonlPagination{
			Kind:       "pagination",
			PageIndex:  pageIndex + 1,
			SearchID:   page.document.Search.ID,
			Pagination: page.raw.ResultSet.Pagination,
			Links:      page.raw.ResultSet.Links,
		}); err != nil {
			return nil, err
		}
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func appendJSONLine(output *bytes.Buffer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil || output.Len()+len(encoded)+1 > maximumJSONLBytes {
		return ErrOutput
	}
	output.Write(encoded)
	output.WriteByte('\n')
	return nil
}

func totalText(total *int) string {
	if total == nil {
		return "unknown"
	}
	return strconv.Itoa(*total)
}

func compactPreview(value json.RawMessage, maximum int) string {
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, value); err != nil {
		return "[invalid data]"
	}
	text := safeText(compacted.String())
	if len(text) <= maximum {
		return text
	}
	for maximum > 0 && maximum < len(text) && !utf8.RuneStart(text[maximum]) {
		maximum--
	}
	return strings.TrimSpace(text[:maximum]) + "…"
}

func writeWrapped(output *bytes.Buffer, prefix, value string, width int) {
	value = safeText(value)
	available := width - runewidth.StringWidth(prefix)
	if available < 1 {
		available = 1
	}
	lines := wrap(value, available)
	if len(lines) == 0 {
		output.WriteString(prefix)
		output.WriteByte('\n')
		return
	}
	output.WriteString(prefix)
	output.WriteString(lines[0])
	output.WriteByte('\n')
	indent := strings.Repeat(" ", runewidth.StringWidth(prefix))
	for _, line := range lines[1:] {
		output.WriteString(indent)
		output.WriteString(line)
		output.WriteByte('\n')
	}
}

func wrap(value string, width int) []string {
	words := strings.Fields(value)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	var current strings.Builder
	for _, word := range words {
		for runewidth.StringWidth(word) > width {
			head, tail := splitWidth(word, width)
			if current.Len() > 0 {
				lines = append(lines, current.String())
				current.Reset()
			}
			lines = append(lines, head)
			word = tail
		}
		if current.Len() == 0 {
			current.WriteString(word)
			continue
		}
		if runewidth.StringWidth(current.String())+1+runewidth.StringWidth(word) <= width {
			current.WriteByte(' ')
			current.WriteString(word)
			continue
		}
		lines = append(lines, current.String())
		current.Reset()
		current.WriteString(word)
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

func splitWidth(value string, width int) (string, string) {
	var head strings.Builder
	used := 0
	for index, character := range value {
		characterWidth := runewidth.RuneWidth(character)
		if used+characterWidth > width && head.Len() > 0 {
			return head.String(), value[index:]
		}
		head.WriteRune(character)
		used += characterWidth
	}
	return head.String(), ""
}

func safeText(value string) string {
	return diagnostic.TerminalSafeText(value, len(value))
}
