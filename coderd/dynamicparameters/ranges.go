package dynamicparameters

import (
	"math"
	"strconv"

	"github.com/hashicorp/hcl/v2"

	"github.com/coder/coder/v2/coderd/parameterstyling"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/preview/types"
)

type parameterRangeGroup struct {
	min []*types.Parameter
	max []*types.Parameter
}

func validateParameterRanges(parameters []types.Parameter) {
	groups := make(map[string]*parameterRangeGroup)

	for i := range parameters {
		parameter := &parameters[i]
		styling := parameterstyling.PreviewParameterStyling(parameter.Source, codersdk.PreviewParameterStyling{})
		isNumericSlider := string(parameter.Type) == "number" && string(parameter.FormType) == "slider"
		hasSliderStyling := styling.Step != nil ||
			styling.SliderMin != nil ||
			styling.SliderMax != nil ||
			styling.DisplayUnit != "" ||
			styling.RangeGroup != "" ||
			styling.RangeRole != "" ||
			styling.AutoMaxMultiplier != nil

		if hasSliderStyling && !isNumericSlider {
			addParameterRangeDiagnostic(parameter, "Invalid slider parameter", "Slider styling can only be used with numeric slider parameters.")
			continue
		}
		if styling.RangeGroup == "" && styling.RangeRole != "" {
			addParameterRangeDiagnostic(parameter, "Invalid range parameter", "range_role requires range_group.")
			continue
		}
		if !isNumericSlider {
			continue
		}

		minBound, maxBound := parameterSliderBounds(parameter, styling)
		if !isFinite(minBound) || !isFinite(maxBound) || minBound >= maxBound {
			addParameterRangeDiagnostic(parameter, "Invalid slider bounds", "The effective slider minimum must be less than the effective slider maximum.")
			continue
		}
		step := parameterSliderStep(styling)
		if !isFinite(step) || step <= 0 {
			addParameterRangeDiagnostic(parameter, "Invalid slider step", "The slider step must be greater than zero.")
			continue
		}
		if len(parameter.Validations) > 0 &&
			(!isInteger(minBound) || !isInteger(maxBound) || !isInteger(step)) {
			addParameterRangeDiagnostic(
				parameter,
				"Fractional slider values cannot use validation blocks",
				"Use slider_min, slider_max, and step in styling for fractional slider bounds and increments. The numeric validation block only supports integer values.",
			)
		}
		if styling.AutoMaxMultiplier != nil {
			if !isFinite(*styling.AutoMaxMultiplier) || *styling.AutoMaxMultiplier < 1 {
				addParameterRangeDiagnostic(parameter, "Invalid automatic maximum multiplier", "auto_max_multiplier must be greater than or equal to 1.")
				continue
			}
			if styling.RangeGroup == "" || styling.RangeRole != "min" {
				addParameterRangeDiagnostic(parameter, "Invalid automatic maximum multiplier", "auto_max_multiplier can only be set on the min parameter of a range group.")
				continue
			}
		}

		if parameter.Value.Valid() && parameter.Value.IsKnown() && !hcl.Diagnostics(parameter.Diagnostics).HasErrors() {
			value, err := strconv.ParseFloat(parameter.Value.AsString(), 64)
			if err == nil && isFinite(value) {
				if value < minBound {
					addParameterRangeDiagnostic(parameter, "Value below slider minimum", "The value must be greater than or equal to the effective slider minimum.")
				}
				if value > maxBound {
					addParameterRangeDiagnostic(parameter, "Value above slider maximum", "The value must be less than or equal to the effective slider maximum.")
				}
				if !isAlignedToStep(value, minBound, step) {
					addParameterRangeDiagnostic(parameter, "Value does not match slider step", "The value must align with the configured slider step.")
				}
			}
		}

		if styling.RangeGroup == "" {
			continue
		}

		group := groups[styling.RangeGroup]
		if group == nil {
			group = &parameterRangeGroup{}
			groups[styling.RangeGroup] = group
		}
		switch styling.RangeRole {
		case "min":
			group.min = append(group.min, parameter)
		case "max":
			group.max = append(group.max, parameter)
		default:
			addParameterRangeDiagnostic(parameter, "Invalid range role", "range_role must be either min or max.")
		}
	}

	for name, group := range groups {
		if len(group.min) != 1 || len(group.max) != 1 {
			members := append([]*types.Parameter{}, group.min...)
			members = append(members, group.max...)
			for _, parameter := range members {
				addParameterRangeDiagnostic(parameter, "Incomplete parameter range", "Range group "+strconv.Quote(name)+" must define exactly one min and one max parameter.")
			}
			continue
		}

		minParameter := group.min[0]
		maxParameter := group.max[0]
		if hcl.Diagnostics(minParameter.Diagnostics).HasErrors() || hcl.Diagnostics(maxParameter.Diagnostics).HasErrors() {
			continue
		}

		minStyling := parameterstyling.PreviewParameterStyling(minParameter.Source, codersdk.PreviewParameterStyling{})
		maxStyling := parameterstyling.PreviewParameterStyling(maxParameter.Source, codersdk.PreviewParameterStyling{})
		minLower, minUpper := parameterSliderBounds(minParameter, minStyling)
		maxLower, maxUpper := parameterSliderBounds(maxParameter, maxStyling)
		if !equalFloat(minLower, maxLower) ||
			!equalFloat(minUpper, maxUpper) ||
			!equalFloat(parameterSliderStep(minStyling), parameterSliderStep(maxStyling)) ||
			minStyling.DisplayUnit != maxStyling.DisplayUnit {
			addParameterRangeDiagnostic(minParameter, "Mismatched parameter range", "Min and Max parameters in a range group must use identical slider bounds, step, and display unit.")
			addParameterRangeDiagnostic(maxParameter, "Mismatched parameter range", "Min and Max parameters in a range group must use identical slider bounds, step, and display unit.")
			continue
		}
		if !minParameter.Value.Valid() || !minParameter.Value.IsKnown() || !maxParameter.Value.Valid() || !maxParameter.Value.IsKnown() {
			continue
		}

		minValue, minErr := strconv.ParseFloat(minParameter.Value.AsString(), 64)
		maxValue, maxErr := strconv.ParseFloat(maxParameter.Value.AsString(), 64)
		if minErr != nil || maxErr != nil || !isFinite(minValue) || !isFinite(maxValue) {
			continue
		}
		if minValue > maxValue {
			addParameterRangeDiagnostic(
				minParameter,
				"Invalid parameter range",
				"Minimum value must be less than or equal to maximum value.",
			)
		}
	}
}

func parameterSliderBounds(
	parameter *types.Parameter,
	styling codersdk.PreviewParameterStyling,
) (float64, float64) {
	var min *float64
	var max *float64
	if styling.SliderMin != nil {
		min = styling.SliderMin
	}
	if styling.SliderMax != nil {
		max = styling.SliderMax
	}

	for _, validation := range parameter.Validations {
		if validation.Min != nil {
			value := float64(*validation.Min)
			if min == nil || value > *min {
				min = &value
			}
		}
		if validation.Max != nil {
			value := float64(*validation.Max)
			if max == nil || value < *max {
				max = &value
			}
		}
	}

	if min == nil {
		returnMin := 0.0
		min = &returnMin
	}
	if max == nil {
		returnMax := 100.0
		max = &returnMax
	}
	return *min, *max
}

func parameterSliderStep(styling codersdk.PreviewParameterStyling) float64 {
	if styling.Step == nil {
		return 1
	}
	return *styling.Step
}

func equalFloat(left, right float64) bool {
	return isFinite(left) && isFinite(right) &&
		math.Abs(left-right) <= 1e-8*math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func isInteger(value float64) bool {
	return math.Abs(value-math.Round(value)) <= 1e-8
}

func isAlignedToStep(value, origin, step float64) bool {
	if step <= 0 {
		return false
	}
	steps := (value - origin) / step
	return math.Abs(steps-math.Round(steps)) <= 1e-8
}

func addParameterRangeDiagnostic(parameter *types.Parameter, summary, detail string) {
	var subject *hcl.Range
	if parameter.Source != nil {
		r := parameter.Source.HCLBlock().TypeRange
		subject = &r
	}

	diagnostic := &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  summary,
		Detail:   detail,
		Subject:  subject,
	}
	parameter.Diagnostics = append(parameter.Diagnostics, types.DiagnosticCode(diagnostic, "range_validation"))
}
