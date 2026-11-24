package parameterstyling_test

import (
	"fmt"
	"testing"

	"github.com/aquasecurity/trivy/pkg/iac/terraform"
	terraformcontext "github.com/aquasecurity/trivy/pkg/iac/terraform/context"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"

	"github.com/coder/coder/v2/coderd/parameterstyling"
	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
)

func TestPreviewParameterStyling(t *testing.T) {
	t.Parallel()

	defaults := codersdk.PreviewParameterStyling{
		Placeholder: ptr.Ref("placeholder"),
		Disabled:    ptr.Ref(true),
	}

	block := parameterBlock(t, `{
		"step": 0.25,
		"preset_mutable": true,
		"slider_min": 0.25,
		"slider_max": 8,
		"display_unit": "vCPU",
		"range_group": "cpu",
		"range_role": "min",
		"auto_max_multiplier": 2
	}`)

	got := parameterstyling.PreviewParameterStyling(block, defaults)
	require.Equal(t, defaults.Placeholder, got.Placeholder)
	require.Equal(t, defaults.Disabled, got.Disabled)
	require.Equal(t, ptr.Ref(0.25), got.Step)
	require.True(t, got.PresetMutable)
	require.Equal(t, ptr.Ref(0.25), got.SliderMin)
	require.Equal(t, ptr.Ref(float64(8)), got.SliderMax)
	require.Equal(t, "vCPU", got.DisplayUnit)
	require.Equal(t, "cpu", got.RangeGroup)
	require.Equal(t, "min", got.RangeRole)
	require.Equal(t, ptr.Ref(float64(2)), got.AutoMaxMultiplier)
}

func TestPreviewParameterStylingFromJSONEncode(t *testing.T) {
	t.Parallel()

	config := []byte(`data "coder_parameter" "test" {
	styling = jsonencode({
		step = 0.25
		slider_min = 0.25
		slider_max = 8
		display_unit = "vCPU"
		range_group = "cpu"
		range_role = "min"
	})
}`)
	file, diags := hclsyntax.ParseConfig(config, "test.tf", hcl.Pos{Line: 1, Column: 1})
	require.False(t, diags.HasErrors(), "%v", diags)

	body, ok := file.Body.(*hclsyntax.Body)
	require.True(t, ok)
	require.Len(t, body.Blocks, 1)

	ctx := terraformcontext.NewContext(&hcl.EvalContext{
		Functions: map[string]function.Function{
			"jsonencode": stdlib.JSONEncodeFunc,
		},
	}, nil)
	block := terraform.NewBlock(body.Blocks[0].AsHCLBlock(), ctx, nil, nil, "", nil)

	got := parameterstyling.PreviewParameterStyling(block, codersdk.PreviewParameterStyling{})
	require.Equal(t, ptr.Ref(0.25), got.Step)
	require.Equal(t, ptr.Ref(0.25), got.SliderMin)
	require.Equal(t, ptr.Ref(float64(8)), got.SliderMax)
	require.Equal(t, "vCPU", got.DisplayUnit)
	require.Equal(t, "cpu", got.RangeGroup)
	require.Equal(t, "min", got.RangeRole)
}

func TestPreviewParameterStylingIgnoresInvalidJSON(t *testing.T) {
	t.Parallel()

	defaults := codersdk.PreviewParameterStyling{
		Placeholder: ptr.Ref("placeholder"),
		Step:        ptr.Ref(float64(1)),
	}

	got := parameterstyling.PreviewParameterStyling(
		parameterBlock(t, `{"step":0.25,"slider_min":"invalid"}`),
		defaults,
	)
	require.Equal(t, defaults, got)
	require.Equal(t, defaults, parameterstyling.PreviewParameterStyling(nil, defaults))
}

func parameterBlock(t *testing.T, styling string) *terraform.Block {
	t.Helper()

	config := fmt.Sprintf(`data "coder_parameter" "test" {
	styling = %q
}`, styling)
	file, diags := hclsyntax.ParseConfig([]byte(config), "test.tf", hcl.Pos{Line: 1, Column: 1})
	require.False(t, diags.HasErrors(), "%v", diags)

	body, ok := file.Body.(*hclsyntax.Body)
	require.True(t, ok)
	require.Len(t, body.Blocks, 1)

	return terraform.NewBlock(body.Blocks[0].AsHCLBlock(), nil, nil, nil, "", nil)
}
