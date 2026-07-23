package quality

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxModelReviewJSONBytes = 10 << 20

func DecodeVerifierReview(reader io.Reader) (VerifierReview, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxModelReviewJSONBytes+1))
	if err != nil {
		return VerifierReview{}, err
	}
	if len(raw) > maxModelReviewJSONBytes {
		return VerifierReview{}, errors.New("verifier review exceeds 10 MiB")
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return VerifierReview{}, err
	}
	if err := requireFields(document, "verifier review", "decisions"); err != nil {
		return VerifierReview{}, err
	}
	if !isJSONArray(document["decisions"]) {
		return VerifierReview{}, errors.New("verifier review.decisions must be an array")
	}
	var decisions []map[string]json.RawMessage
	if err := json.Unmarshal(document["decisions"], &decisions); err != nil {
		return VerifierReview{}, fmt.Errorf("decode verifier decisions: %w", err)
	}
	for index, decision := range decisions {
		if err := requireFields(decision, fmt.Sprintf("decisions[%d]", index),
			"finding_id", "result", "verification_summary", "uncertainties",
		); err != nil {
			return VerifierReview{}, err
		}
		if !isJSONArray(decision["uncertainties"]) {
			return VerifierReview{}, fmt.Errorf("decisions[%d].uncertainties must be an array", index)
		}
	}
	return DecodeStrict[VerifierReview](bytes.NewReader(raw))
}

var requiredModelReviewFields = []string{
	"activated_rule_families", "inactive_rule_families", "findings", "uninspected_scope", "missing_context", "inspected_context",
}

var requiredFindingFields = []string{
	"id", "rule_id", "code_locations", "production_impact", "minimal_fix",
}

func DecodeModelReview(reader io.Reader) (ModelReview, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxModelReviewJSONBytes+1))
	if err != nil {
		return ModelReview{}, err
	}
	if len(raw) > maxModelReviewJSONBytes {
		return ModelReview{}, errors.New("model review exceeds 10 MiB")
	}
	if err := validateModelReviewShape(raw); err != nil {
		return ModelReview{}, err
	}
	return DecodeStrict[ModelReview](bytes.NewReader(raw))
}

func validateModelReviewShape(raw []byte) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return err
	}
	if err := requireFields(document, "model review", requiredModelReviewFields...); err != nil {
		return err
	}
	for _, field := range []string{"activated_rule_families", "inactive_rule_families", "findings", "uninspected_scope", "missing_context", "inspected_context"} {
		if !isJSONArray(document[field]) {
			return fmt.Errorf("model review.%s must be an array", field)
		}
	}

	var inactive []map[string]json.RawMessage
	if err := json.Unmarshal(document["inactive_rule_families"], &inactive); err != nil {
		return fmt.Errorf("decode inactive_rule_families: %w", err)
	}
	for index, family := range inactive {
		if err := requireFields(family, fmt.Sprintf("inactive_rule_families[%d]", index), "id", "reason"); err != nil {
			return err
		}
	}

	var inspected []map[string]json.RawMessage
	if err := json.Unmarshal(document["inspected_context"], &inspected); err != nil {
		return fmt.Errorf("decode inspected_context: %w", err)
	}
	for index, item := range inspected {
		if err := requireFields(item, fmt.Sprintf("inspected_context[%d]", index), "path", "purpose"); err != nil {
			return err
		}
	}

	var findings []map[string]json.RawMessage
	if err := json.Unmarshal(document["findings"], &findings); err != nil {
		return fmt.Errorf("decode findings: %w", err)
	}
	for index, finding := range findings {
		prefix := fmt.Sprintf("findings[%d]", index)
		if err := requireFields(finding, prefix, requiredFindingFields...); err != nil {
			return err
		}
		if !isJSONArray(finding["code_locations"]) {
			return fmt.Errorf("%s.code_locations must be an array", prefix)
		}
		var locations []map[string]json.RawMessage
		if err := json.Unmarshal(finding["code_locations"], &locations); err != nil {
			return fmt.Errorf("decode %s.code_locations: %w", prefix, err)
		}
		for locationIndex, location := range locations {
			if err := requireFields(location, fmt.Sprintf("%s.code_locations[%d]", prefix, locationIndex), "path", "line"); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireFields(document map[string]json.RawMessage, prefix string, fields ...string) error {
	if document == nil {
		return errors.New(prefix + " must be an object")
	}
	for _, field := range fields {
		value, exists := document[field]
		if !exists {
			return fmt.Errorf("%s.%s is required", prefix, field)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s.%s cannot be null", prefix, field)
		}
	}
	return nil
}

func isJSONArray(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 0 && trimmed[0] == '['
}
