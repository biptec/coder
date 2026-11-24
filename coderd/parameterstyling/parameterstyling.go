// Package parameterstyling reads the JSON styling metadata attached to
// dynamic template parameters.
package parameterstyling

import (
	"encoding/json"

	"github.com/aquasecurity/trivy/pkg/iac/terraform"
	"github.com/zclconf/go-cty/cty"

	"github.com/coder/coder/v2/codersdk"
)

// PreviewParameterStyling returns the styling metadata from a parameter's
// source block, preserving values already extracted by the preview package.
// Invalid or unavailable styling JSON is ignored, matching preview parsing.
func PreviewParameterStyling(
	block *terraform.Block,
	defaults codersdk.PreviewParameterStyling,
) codersdk.PreviewParameterStyling {
	if block == nil {
		return defaults
	}

	attribute := block.GetAttribute("styling")
	if attribute == nil {
		return defaults
	}

	value := attribute.Value()
	if !value.IsWhollyKnown() || !value.Type().Equals(cty.String) {
		return defaults
	}

	parsed := defaults
	if err := json.Unmarshal([]byte(value.AsString()), &parsed); err != nil {
		return defaults
	}

	return parsed
}
