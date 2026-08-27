package searchcontract

import (
	"encoding/json"
	"net/url"
	"time"
)

var semanticIssueCodes = []string{
	"RESULT_RETURNED_COUNT_MISMATCH",
	"RESULT_RETURNED_EXCEEDS_PAGE_SIZE",
	"RESULT_TOTAL_BELOW_RETURNED",
	"PAGE_TOTAL_BELOW_OFFSET",
	"RESULT_SELF_LINK_MISMATCH",
	"CURSOR_PREVIOUS_LINK_MISMATCH",
	"PAGE_PREVIOUS_REFERENCE_MISMATCH",
	"PAGE_NEXT_REFERENCE_MISMATCH",
	"PAGE_HAS_MORE_TOTAL_MISMATCH",
	"PAGE_PREVIOUS_LINK_SELF_REFERENCE",
	"PAGE_NEXT_LINK_SELF_REFERENCE",
	"PAGE_RELATION_LINK_DUPLICATE",
	"ITEM_POSITION_DUPLICATE",
	"ITEM_POSITION_NOT_INCREASING",
	"USE_AGENT_CARD_INVALID",
	"SEARCH_STARTED_BEFORE_CREATED",
	"SEARCH_COMPLETED_BEFORE_STARTED",
	"SEARCH_COMPLETED_BEFORE_CREATED",
	"TIMESTAMP_INVALID",
}

var semanticIssueCodesV2 = append(append([]string{}, semanticIssueCodes...),
	"QUESTION_ID_DUPLICATE",
	"SEARCH_PARENT_SELF_REFERENCE",
	"SEARCH_ROOT_SELF_REFERENCE",
)

type semanticArtifact struct {
	SchemaVersion        string `json:"schema_version"`
	SemanticRulesVersion string `json:"semantic_rules_version"`
	Rules                []struct {
		Code string `json:"code"`
	} `json:"rules"`
}

func validateSemanticArtifact(assets releasedAssets) error {
	var artifact semanticArtifact
	codes := semanticIssueCodes
	if assets.identity.schemaVersion == SchemaVersionV2 {
		codes = semanticIssueCodesV2
	}
	if err := decodeJSON(assets.semantics, &artifact); err != nil ||
		artifact.SchemaVersion != assets.identity.schemaVersion ||
		artifact.SemanticRulesVersion != assets.identity.semanticRules ||
		len(artifact.Rules) != len(codes) {
		return ErrInvalid
	}
	for index, code := range codes {
		if artifact.Rules[index].Code != code {
			return ErrInvalid
		}
	}
	return nil
}

func validateSemantics(document Document, identity contractIdentity) error {
	createdAt, createdOK := canonicalTime(document.Search.CreatedAt)
	_, generatedOK := canonicalTime(document.Metadata.GeneratedAt)
	if !createdOK || !generatedOK {
		return ErrInvalid
	}
	var startedAt, completedAt time.Time
	if document.Search.StartedAt != nil {
		var ok bool
		startedAt, ok = canonicalTime(*document.Search.StartedAt)
		if !ok || startedAt.Before(createdAt) {
			return ErrInvalid
		}
	}
	if document.Search.CompletedAt != nil {
		var ok bool
		completedAt, ok = canonicalTime(*document.Search.CompletedAt)
		if !ok {
			return ErrInvalid
		}
		if document.Search.StartedAt != nil {
			if completedAt.Before(startedAt) {
				return ErrInvalid
			}
		} else if completedAt.Before(createdAt) {
			return ErrInvalid
		}
	}

	resultSet := document.ResultSet
	if resultSet == nil {
		return nil
	}
	pagination := resultSet.Pagination
	if pagination.Returned != len(resultSet.Items) ||
		pagination.Returned > pagination.PageSize ||
		pagination.Total != nil && *pagination.Total < pagination.Returned ||
		resultSet.Links.Self != document.Links.Self {
		return ErrInvalid
	}
	if pagination.Kind == "cursor" &&
		(pagination.PreviousCursor == nil) != (resultSet.Links.Previous == nil) {
		return ErrInvalid
	}
	if pagination.Kind == "page" {
		if pagination.Page == nil {
			return ErrInvalid
		}
		page := *pagination.Page
		offset := int64(page-1)*int64(pagination.PageSize) +
			int64(pagination.Returned)
		if pagination.Total != nil &&
			(int64(*pagination.Total) < offset ||
				pagination.HasMore != (offset < int64(*pagination.Total))) {
			return ErrInvalid
		}
		previousValid := page == 1 &&
			pagination.PreviousPage == nil &&
			resultSet.Links.Previous == nil
		if page > 1 {
			previousValid = pagination.PreviousPage != nil &&
				*pagination.PreviousPage == page-1 &&
				resultSet.Links.Previous != nil
		}
		nextValid := !pagination.HasMore &&
			pagination.NextPage == nil &&
			resultSet.Links.Next == nil
		if pagination.HasMore {
			nextValid = pagination.NextPage != nil &&
				*pagination.NextPage == page+1 &&
				resultSet.Links.Next != nil
		}
		if !previousValid ||
			!nextValid ||
			resultSet.Links.Previous != nil &&
				*resultSet.Links.Previous == resultSet.Links.Self ||
			resultSet.Links.Next != nil &&
				*resultSet.Links.Next == resultSet.Links.Self ||
			resultSet.Links.Previous != nil &&
				resultSet.Links.Next != nil &&
				*resultSet.Links.Previous == *resultSet.Links.Next {
			return ErrInvalid
		}
	}

	seen := make(map[int]struct{}, len(resultSet.Items))
	previous := 0
	for index, item := range resultSet.Items {
		if _, duplicate := seen[item.Position]; duplicate ||
			index > 0 && item.Position <= previous {
			return ErrInvalid
		}
		seen[item.Position] = struct{}{}
		previous = item.Position
		if !json.Valid(item.Data) {
			return ErrInvalid
		}
		if item.Use != nil && !validUse(*item.Use) {
			return ErrInvalid
		}
	}
	if identity.schemaVersion == SchemaVersionV2 {
		if document.Search.ParentExecutionRef != nil &&
			*document.Search.ParentExecutionRef == document.Search.ID ||
			document.Search.RootExecutionRef != nil &&
				*document.Search.RootExecutionRef == document.Search.ID {
			return ErrInvalid
		}
		seenQuestions := make(map[string]struct{}, len(document.Questions))
		for _, question := range document.Questions {
			if _, duplicate := seenQuestions[question.ID]; duplicate {
				return ErrInvalid
			}
			seenQuestions[question.ID] = struct{}{}
		}
	}
	return nil
}

func validUse(value Use) bool {
	if value.Protocol != "a2a" {
		return false
	}
	parsed, err := url.Parse(value.AgentCard)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func canonicalTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil
}
