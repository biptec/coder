package main

import (
	"encoding/json"
	"reflect"

	"golang.org/x/xerrors"
)

func scoreSelection(sc scenario, selected selection, tools []toolSpec) (toolMatch, argumentsMatch, schemaValid, forbidden, passed bool) {
	toolMatch = selected.Tool == sc.Expected.Tool
	argumentsMatch = partialJSONMatch(sc.Expected.Arguments, selected.Arguments)
	if tool := findTool(tools, selected.Tool); tool != nil {
		schemaValid = argumentsAdhereToSchema(tool.Parameters, selected.Arguments)
	}
	for _, name := range sc.ForbiddenTools {
		if selected.Tool == name {
			forbidden = true
			break
		}
	}
	passed = toolMatch && argumentsMatch && schemaValid && !forbidden
	return toolMatch, argumentsMatch, schemaValid, forbidden, passed
}

func findTool(tools []toolSpec, name string) *toolSpec {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// argumentsAdhereToSchema checks the parts of JSON Schema used by the Coder MCP
// catalog and intentionally rejects undeclared object properties. The production
// handlers decode into Go structs, where an undeclared field such as a future
// remote host selector would otherwise be silently ignored. Treating it as an
// eval failure prevents an old catalog from receiving credit for an argument it
// cannot actually honor.
func argumentsAdhereToSchema(schema map[string]any, arguments map[string]any) bool {
	return valueAdheresToSchema(schema, arguments)
}

func valueAdheresToSchema(schema map[string]any, value any) bool {
	if enums, ok := schema["enum"].([]any); ok && len(enums) > 0 {
		matched := false
		for _, candidate := range enums {
			if scalarEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	typeName, _ := schema["type"].(string)
	switch typeName {
	case "", "null":
		return true
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		if ok {
			return true
		}
		_, ok = value.(int)
		return ok
	case "integer":
		switch number := value.(type) {
		case float64:
			return number == float64(int64(number))
		case int, int32, int64:
			return true
		default:
			return false
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return false
		}
		itemSchema, _ := schema["items"].(map[string]any)
		if itemSchema == nil {
			return true
		}
		for _, item := range items {
			if !valueAdheresToSchema(itemSchema, item) {
				return false
			}
		}
		return true
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range stringSlice(schema["required"]) {
			if _, exists := object[name]; !exists {
				return false
			}
		}
		if properties == nil {
			return true
		}
		for name, item := range object {
			property, exists := properties[name]
			if !exists {
				return false
			}
			propertySchema, ok := property.(map[string]any)
			if ok && !valueAdheresToSchema(propertySchema, item) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

func partialJSONMatch(expected, actual any) bool {
	if expected == nil {
		return true
	}

	switch want := expected.(type) {
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for key, expectedValue := range want {
			actualValue, exists := got[key]
			if !exists || !partialJSONMatch(expectedValue, actualValue) {
				return false
			}
		}
		return true
	case []any:
		got, ok := actual.([]any)
		if !ok || len(want) != len(got) {
			return false
		}
		for i := range want {
			if !partialJSONMatch(want[i], got[i]) {
				return false
			}
		}
		return true
	default:
		return scalarEqual(want, actual)
	}
}

func scalarEqual(expected, actual any) bool {
	if reflect.DeepEqual(expected, actual) {
		return true
	}
	wantJSON, wantErr := json.Marshal(expected)
	gotJSON, gotErr := json.Marshal(actual)
	return wantErr == nil && gotErr == nil && string(wantJSON) == string(gotJSON)
}

func expectedForCatalog(sc scenario, catalog string) expectedCall {
	if expected, ok := sc.ExpectedByCatalog[catalog]; ok {
		return expected
	}
	return sc.Expected
}

func validateScenarios(set scenarioSet, catalogs map[string][]toolSpec) error {
	if set.Version != 1 {
		return xerrors.Errorf("unsupported scenario version %d", set.Version)
	}
	candidate, ok := catalogs["candidate"]
	if !ok {
		return xerrors.New("candidate catalog is required for scenario validation")
	}
	candidateNames := toolNameSet(candidate)
	seen := map[string]struct{}{}
	for i, sc := range set.Scenarios {
		if sc.ID == "" {
			return xerrors.Errorf("scenario %d has empty id", i)
		}
		if _, exists := seen[sc.ID]; exists {
			return xerrors.Errorf("duplicate scenario id %q", sc.ID)
		}
		seen[sc.ID] = struct{}{}
		if sc.Prompt == "" {
			return xerrors.Errorf("scenario %q has empty prompt", sc.ID)
		}
		if sc.Expected.Tool == "" {
			return xerrors.Errorf("scenario %q has empty expected tool", sc.ID)
		}
		if _, ok := candidateNames[sc.Expected.Tool]; !ok {
			return xerrors.Errorf("scenario %q expects tool %q that is absent from the candidate catalog", sc.ID, sc.Expected.Tool)
		}
		for catalog, expected := range sc.ExpectedByCatalog {
			tools, ok := catalogs[catalog]
			if !ok {
				return xerrors.Errorf("scenario %q references unknown catalog %q", sc.ID, catalog)
			}
			if expected.Tool == "" {
				return xerrors.Errorf("scenario %q has empty expected tool for catalog %q", sc.ID, catalog)
			}
			if _, ok := toolNameSet(tools)[expected.Tool]; !ok {
				return xerrors.Errorf("scenario %q expects tool %q that is absent from catalog %q", sc.ID, expected.Tool, catalog)
			}
		}
		if sc.Weight < 0 {
			return xerrors.Errorf("scenario %q has negative weight", sc.ID)
		}
	}
	return nil
}

func toolNameSet(tools []toolSpec) map[string]struct{} {
	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		names[tool.Name] = struct{}{}
	}
	return names
}
