import type { Meta, StoryObj } from "@storybook/react-vite";
import { type FC, useState } from "react";
import { expect, userEvent, within } from "storybook/test";
import type { PreviewParameter } from "#/api/typesGenerated";
import { MockPreviewParameter } from "#/testHelpers/entities";
import { DynamicParameter } from "./DynamicParameter";

const cpuParameter: PreviewParameter = {
	...MockPreviewParameter,
	name: "cpu",
	display_name: "CPU",
	type: "number",
	form_type: "slider",
	value: { valid: true, value: "1.25" },
	default_value: { valid: true, value: "1.25" },
	styling: {
		step: 0.25,
		slider_min: 0.25,
		slider_max: 8,
		display_unit: "vCPU",
	},
};

const InteractiveNumericSlider: FC<{ parameter: PreviewParameter }> = ({
	parameter,
}) => {
	const [value, setValue] = useState(parameter.value.value);

	return (
		<DynamicParameter parameter={parameter} value={value} onChange={setValue} />
	);
};

const meta: Meta<typeof InteractiveNumericSlider> = {
	title: "modules/workspaces/DynamicParameter/NumericSlider",
	component: InteractiveNumericSlider,
	args: {
		parameter: cpuParameter,
	},
};

export default meta;
type Story = StoryObj<typeof InteractiveNumericSlider>;

export const EditableValue: Story = {
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const input = canvas.getByRole("spinbutton");

		await expect(input).toHaveValue(1.25);
		await expect(canvas.getByRole("slider", { name: "CPU" })).toBeVisible();
		await expect(canvas.getAllByRole("slider")).toHaveLength(1);
		await userEvent.clear(input);
		await userEvent.type(input, "2.5");

		await expect(input).toHaveValue(2.5);
		await expect(canvas.getByRole("slider")).toHaveAttribute(
			"aria-valuenow",
			"2.5",
		);
	},
};
