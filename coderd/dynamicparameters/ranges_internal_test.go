package dynamicparameters

import (
	"fmt"
	"testing"

	"github.com/aquasecurity/trivy/pkg/iac/terraform"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/require"

	"github.com/coder/preview/types"
	"github.com/coder/terraform-provider-coder/v2/provider"
)

func TestValidateParameterRanges(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameter(t, "cpu_min", "min", "0.25", 0.25, 8, 0.25),
			rangeParameter(t, "cpu_max", "max", "2", 0.25, 8, 0.25),
		}

		validateParameterRanges(parameters)

		for _, parameter := range parameters {
			require.False(t, hcl.Diagnostics(parameter.Diagnostics).HasErrors())
		}
	})

	t.Run("minimum greater than maximum", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameter(t, "cpu_min", "min", "2", 0.25, 8, 0.25),
			rangeParameter(t, "cpu_max", "max", "1", 0.25, 8, 0.25),
		}

		validateParameterRanges(parameters)

		require.True(t, hcl.Diagnostics(parameters[0].Diagnostics).HasErrors())
		require.False(t, hcl.Diagnostics(parameters[1].Diagnostics).HasErrors())
		require.Equal(t, "Invalid parameter range", parameters[0].Diagnostics[len(parameters[0].Diagnostics)-1].Summary)
	})

	t.Run("value outside slider bounds", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameter(t, "cpu_min", "min", "0", 0.25, 8, 0.25),
			rangeParameter(t, "cpu_max", "max", "2", 0.25, 8, 0.25),
		}

		validateParameterRanges(parameters)

		require.True(t, hcl.Diagnostics(parameters[0].Diagnostics).HasErrors())
		require.Equal(t, "Value below slider minimum", parameters[0].Diagnostics[0].Summary)
	})

	t.Run("value does not align to step", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameter(t, "cpu_min", "min", "0.3", 0.25, 8, 0.25),
			rangeParameter(t, "cpu_max", "max", "2", 0.25, 8, 0.25),
		}

		validateParameterRanges(parameters)

		require.True(t, hcl.Diagnostics(parameters[0].Diagnostics).HasErrors())
		require.Equal(t, "Value does not match slider step", parameters[0].Diagnostics[0].Summary)
	})

	t.Run("default step is enforced when styling omits step", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameterWithoutStep(t, "cpu_min", "min", "0.3", 0.25, 8),
			rangeParameterWithoutStep(t, "cpu_max", "max", "1.25", 0.25, 8),
		}

		validateParameterRanges(parameters)

		require.Equal(t, "Value does not match slider step", parameters[0].Diagnostics[0].Summary)
		require.False(t, hcl.Diagnostics(parameters[1].Diagnostics).HasErrors())
	})

	t.Run("omitted and explicit default steps are compatible", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameterWithoutStep(t, "cpu_min", "min", "0.25", 0.25, 8),
			rangeParameter(t, "cpu_max", "max", "2.25", 0.25, 8, 1),
		}

		validateParameterRanges(parameters)

		for _, parameter := range parameters {
			require.False(t, hcl.Diagnostics(parameter.Diagnostics).HasErrors())
		}
	})

	t.Run("range metadata tolerates floating point representation noise", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameterWithStyling(t, "cpu_min", "0.1", `{"range_group":"cpu","range_role":"min","slider_min":0.1,"slider_max":8,"step":0.1}`),
			rangeParameterWithStyling(t, "cpu_max", "1.1", `{"range_group":"cpu","range_role":"max","slider_min":0.10000000001,"slider_max":8,"step":0.1}`),
		}

		validateParameterRanges(parameters)

		for _, parameter := range parameters {
			require.False(t, hcl.Diagnostics(parameter.Diagnostics).HasErrors())
		}
	})

	t.Run("incomplete group", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameter(t, "cpu_min", "min", "0.25", 0.25, 8, 0.25),
		}

		validateParameterRanges(parameters)

		require.True(t, hcl.Diagnostics(parameters[0].Diagnostics).HasErrors())
		require.Equal(t, "Incomplete parameter range", parameters[0].Diagnostics[len(parameters[0].Diagnostics)-1].Summary)
	})

	t.Run("mismatched slider bounds", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameter(t, "cpu_min", "min", "0.5", 0.25, 8, 0.25),
			rangeParameter(t, "cpu_max", "max", "2", 0.5, 8, 0.25),
		}

		validateParameterRanges(parameters)

		require.True(t, hcl.Diagnostics(parameters[0].Diagnostics).HasErrors())
		require.True(t, hcl.Diagnostics(parameters[1].Diagnostics).HasErrors())
		require.Equal(t, "Mismatched parameter range", parameters[0].Diagnostics[len(parameters[0].Diagnostics)-1].Summary)
	})

	t.Run("automatic maximum multiplier must be at least one", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameter(t, "cpu_min", "min", "0.25", 0.25, 8, 0.25, 0.5),
			rangeParameter(t, "cpu_max", "max", "2", 0.25, 8, 0.25),
		}

		validateParameterRanges(parameters)

		require.True(t, hcl.Diagnostics(parameters[0].Diagnostics).HasErrors())
		require.Equal(t, "Invalid automatic maximum multiplier", parameters[0].Diagnostics[0].Summary)
	})

	t.Run("fractional sliders use styling bounds instead of numeric validation", func(t *testing.T) {
		t.Parallel()
		parameters := []types.Parameter{
			rangeParameter(t, "cpu_min", "min", "0.25", 0.25, 8, 0.25),
			rangeParameter(t, "cpu_max", "max", "2", 0.25, 8, 0.25),
		}
		min := int64(0)
		parameters[0].Validations = []*types.ParameterValidation{{Min: &min}}

		validateParameterRanges(parameters)

		require.True(t, hcl.Diagnostics(parameters[0].Diagnostics).HasErrors())
		require.Equal(t, "Fractional slider values cannot use validation blocks", parameters[0].Diagnostics[0].Summary)
	})
}

func rangeParameter(
	t *testing.T,
	name string,
	role string,
	value string,
	sliderMin float64,
	sliderMax float64,
	step float64,
	autoMaxMultiplier ...float64,
) types.Parameter {
	t.Helper()

	styling := fmt.Sprintf(
		`{"range_group":"cpu","range_role":%q,"slider_min":%g,"slider_max":%g,"step":%g}`,
		role,
		sliderMin,
		sliderMax,
		step,
	)
	if len(autoMaxMultiplier) > 0 {
		styling = fmt.Sprintf(
			`{"range_group":"cpu","range_role":%q,"slider_min":%g,"slider_max":%g,"step":%g,"auto_max_multiplier":%g}`,
			role,
			sliderMin,
			sliderMax,
			step,
			autoMaxMultiplier[0],
		)
	}
	return rangeParameterWithStyling(t, name, value, styling)
}

func rangeParameterWithoutStep(
	t *testing.T,
	name string,
	role string,
	value string,
	sliderMin float64,
	sliderMax float64,
) types.Parameter {
	t.Helper()
	styling := fmt.Sprintf(
		`{"range_group":"cpu","range_role":%q,"slider_min":%g,"slider_max":%g}`,
		role,
		sliderMin,
		sliderMax,
	)
	return rangeParameterWithStyling(t, name, value, styling)
}

func rangeParameterWithStyling(t *testing.T, name, value, styling string) types.Parameter {
	t.Helper()
	config := fmt.Sprintf(`data "coder_parameter" %q {
	styling = %q
}`, name, styling)
	file, diags := hclsyntax.ParseConfig([]byte(config), "test.tf", hcl.Pos{Line: 1, Column: 1})
	require.False(t, diags.HasErrors(), "%v", diags)

	body, ok := file.Body.(*hclsyntax.Body)
	require.True(t, ok)
	require.Len(t, body.Blocks, 1)
	source := terraform.NewBlock(body.Blocks[0].AsHCLBlock(), nil, nil, nil, "", nil)

	return types.Parameter{
		ParameterData: types.ParameterData{
			Name:     name,
			Type:     "number",
			FormType: provider.ParameterFormTypeSlider,
			Source:   source,
		},
		Value: types.StringLiteral(value),
	}
}
