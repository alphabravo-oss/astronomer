package handler

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

const maxRancherQuestions = 2000

var questionPathSegment = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type rancherQuestionsDocument struct {
	Questions []rancherQuestion `json:"questions"`
}

type rancherQuestion struct {
	Variable           string            `json:"variable"`
	Default            any               `json:"default"`
	Description        string            `json:"description"`
	Tooltip            string            `json:"tooltip"`
	Type               string            `json:"type"`
	Label              string            `json:"label"`
	Group              string            `json:"group"`
	Required           bool              `json:"required"`
	Options            []any             `json:"options"`
	Min                *float64          `json:"min"`
	Max                *float64          `json:"max"`
	MinLength          *int              `json:"min_length"`
	MaxLength          *int              `json:"max_length"`
	ShowIf             string            `json:"show_if"`
	ShowSubquestionIf  any               `json:"show_subquestion_if"`
	ShowSubquestionsIf any               `json:"show_subquestions_if"`
	Subquestions       []rancherQuestion `json:"subquestions"`
}

type normalizedQuestionCondition struct {
	Path   string `json:"path"`
	Equals string `json:"equals"`
}

// enrichSchemaWithRancherQuestions overlays Rancher's chart-authored question
// metadata onto the chart schema inferred from values.yaml. The values file
// remains authoritative for paths and types; questions add labels, help,
// enums, bounds, groups and simple conditional visibility.
func enrichSchemaWithRancherQuestions(schema json.RawMessage, raw []byte) json.RawMessage {
	if len(schema) == 0 || len(raw) == 0 {
		return schema
	}
	var root map[string]any
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil
	}
	var document rancherQuestionsDocument
	if err := yaml.Unmarshal(raw, &document); err != nil || len(document.Questions) > maxRancherQuestions {
		return nil
	}
	for _, question := range document.Questions {
		overlayRancherQuestion(root, question, nil, 0)
	}
	root["x-astronomer-questions"] = true
	enriched, err := json.Marshal(root)
	if err != nil {
		return nil
	}
	return enriched
}

func overlayRancherQuestion(root map[string]any, question rancherQuestion, inherited *normalizedQuestionCondition, depth int) {
	if depth > 16 {
		return
	}
	segments := strings.Split(strings.TrimSpace(question.Variable), ".")
	if len(segments) == 0 || len(segments) > 16 {
		return
	}
	for _, segment := range segments {
		if !safeQuestionPathSegment(segment) {
			return
		}
	}
	node := questionSchemaNode(root, segments)
	if node == nil {
		return
	}
	if question.Label != "" {
		node["title"] = question.Label
	}
	description := strings.TrimSpace(question.Description)
	tooltip := strings.TrimSpace(question.Tooltip)
	if description == "" {
		description = tooltip
	} else if tooltip != "" && tooltip != description {
		description += "\n\n" + tooltip
	}
	if description != "" {
		node["description"] = description
	}
	if question.Group != "" {
		node["x-astronomer-group"] = question.Group
	}
	defaultNull, _ := node["x-astronomer-default-null"].(bool)
	if questionType := rancherQuestionJSONType(question.Type); questionType != "" {
		if defaultNull {
			node["type"] = []any{questionType, "null"}
			delete(node, "x-astronomer-default-null")
		} else {
			node["type"] = questionType
		}
	}
	if question.Default != nil {
		node["default"] = question.Default
	}
	if question.Required {
		markQuestionRequired(root, segments)
	}
	if len(question.Options) > 0 {
		options := append([]any(nil), question.Options...)
		// Rancher questions describe the value users may select, while
		// values.yaml remains the effective chart default. Preserve an explicit
		// null default as a valid enum member so merely opening and submitting
		// the chart's configuration does not invalidate its own defaults.
		if defaultNull {
			options = append(options, nil)
		}
		node["enum"] = options
	}
	if question.Min != nil {
		node["minimum"] = *question.Min
	}
	if question.Max != nil {
		node["maximum"] = *question.Max
	}
	if question.MinLength != nil {
		node["minLength"] = *question.MinLength
	}
	if question.MaxLength != nil {
		node["maxLength"] = *question.MaxLength
	}
	if format := rancherQuestionFormat(question.Type); format != "" {
		node["format"] = format
	}
	condition := inherited
	if own := parseSimpleQuestionCondition(question.ShowIf); own != nil {
		condition = own
	}
	if condition != nil {
		node["x-astronomer-show-when"] = condition
	}
	childCondition := condition
	if len(question.Subquestions) > 0 {
		showValue := question.ShowSubquestionIf
		if showValue == nil {
			showValue = question.ShowSubquestionsIf
		}
		if showValue != nil {
			childCondition = &normalizedQuestionCondition{Path: question.Variable, Equals: questionScalarString(showValue)}
		}
	}
	for _, child := range question.Subquestions {
		overlayRancherQuestion(root, child, childCondition, depth+1)
	}
}

func questionSchemaNode(root map[string]any, segments []string) map[string]any {
	cursor := root
	for index, segment := range segments {
		properties, ok := cursor["properties"].(map[string]any)
		if !ok {
			properties = map[string]any{}
			cursor["properties"] = properties
		}
		child, ok := properties[segment].(map[string]any)
		if !ok {
			// questions.yaml is shipped inside the selected chart and is therefore
			// authoritative when values.yaml omits an optional key.
			child = map[string]any{}
			properties[segment] = child
		}
		if index < len(segments)-1 {
			child["type"] = "object"
		}
		cursor = child
	}
	return cursor
}

func safeQuestionPathSegment(segment string) bool {
	if !questionPathSegment.MatchString(segment) {
		return false
	}
	switch segment {
	case "__proto__", "prototype", "constructor":
		return false
	default:
		return true
	}
}

func markQuestionRequired(root map[string]any, segments []string) {
	parent := root
	if len(segments) > 1 {
		parent = questionSchemaNode(root, segments[:len(segments)-1])
		if parent == nil {
			return
		}
	}
	leaf := segments[len(segments)-1]
	required, _ := parent["required"].([]any)
	for _, item := range required {
		if item == leaf {
			return
		}
	}
	parent["required"] = append(required, leaf)
}

func rancherQuestionFormat(questionType string) string {
	switch strings.ToLower(strings.TrimSpace(questionType)) {
	case "multiline", "yaml":
		return "multiline"
	case "password":
		return "password"
	case "hostname", "cidr", "cron", "secret", "configmap", "storageclass", "pvc":
		return strings.ToLower(strings.TrimSpace(questionType))
	case "ip", "ipaddr":
		return "ip"
	default:
		return ""
	}
}

func rancherQuestionJSONType(questionType string) string {
	switch strings.ToLower(strings.TrimSpace(questionType)) {
	case "boolean":
		return "boolean"
	case "int", "integer":
		return "integer"
	case "float", "number":
		return "number"
	case "string", "multiline", "password", "hostname", "ip", "ipaddr", "cidr", "cron", "enum", "radio", "reference", "secret", "configmap", "storageclass", "pvc":
		return "string"
	default:
		return ""
	}
}

func parseSimpleQuestionCondition(raw string) *normalizedQuestionCondition {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "&|()") {
		return nil
	}
	separator := "=="
	if !strings.Contains(raw, separator) {
		separator = "="
	}
	parts := strings.SplitN(raw, separator, 2)
	if len(parts) != 2 {
		return nil
	}
	path := strings.TrimSpace(parts[0])
	for _, segment := range strings.Split(path, ".") {
		if !safeQuestionPathSegment(segment) {
			return nil
		}
	}
	equals := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
	return &normalizedQuestionCondition{Path: path, Equals: equals}
}

func questionScalarString(value any) string {
	switch typed := value.(type) {
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		return typed
	default:
		return ""
	}
}
