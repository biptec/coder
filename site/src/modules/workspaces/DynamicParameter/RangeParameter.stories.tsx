import type { Meta, StoryObj } from "@storybook/react-vite";
import { type FC, useState } from "react";
import { expect, userEvent, within } from "storybook/test";
import type { PreviewParameter } from "#/api/typesGenerated";
import { MockPreviewParameter } from "#/testHelpers/entities";
import { RangeParameter, type RangeParameterProps } from "./RangeParameter";

const minParameter: PreviewParameter = {
	...MockPreviewParameter,
	name: "cpu_min",
	display_name: "CPU",
	type: "number",
	form_type: "slider",
	value: { valid: true, value: "0.5" },
	default_value: { valid: true, value: "0.5" },
	styling: {
		step: 0.25,
		slider_min: 0.25,
		slider_max: 8,
		display_unit: "vCPU",
		range_group: "cpu",
		range_role: "min",
		auto_max_multiplier: 2,
	},
};

const maxParameter: PreviewParameter = {
	...minParameter,
	name: "cpu_max",
	display_name: "CPU Max",
	value: { valid: true, value: "2" },
	default_value: { valid: true, value: "2" },
	styling: {
		...minParameter.styling,
		range_role: "max",
		auto_max_multiplier: undefined,
	},
};

const InteractiveRangeParameter: FC<RangeParameterProps> = ({
	minValue,
	maxValue,
	onChange,
	...props
}) => {
	const [values, setValues] = useState({ min: minValue, max: maxValue });

	return (
		<RangeParameter
			{...props}
			minValue={values.min}
			maxValue={values.max}
			onChange={(min, max) => {
				onChange(min, max);
				setValues({ min, max });
			}}
		/>
	);
};

const meta: Meta<typeof InteractiveRangeParameter> = {
	title: "modules/workspaces/DynamicParameter/RangeParameter",
	component: InteractiveRangeParameter,
	args: {
		minParameter,
		maxParameter,
		minValue: "0.5",
		maxValue: "2",
		onChange: () => {},
	},
};

export default meta;
type Story = StoryObj<typeof InteractiveRangeParameter>;

export const AutomaticMaximum: Story = {
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const minInput = canvas.getByRole("spinbutton", { name: /Min/ });
		const maxInput = canvas.getByRole("spinbutton", { name: /Max/ });

		await expect(canvas.getByRole("slider", { name: "CPU Min" })).toBeVisible();
		await expect(canvas.getByRole("slider", { name: "CPU Max" })).toBeVisible();
		await expect(canvas.getAllByRole("slider")).toHaveLength(2);
		await userEvent.clear(minInput);
		await userEvent.type(minInput, "1.5");

		await expect(minInput).toHaveValue(1.5);
		await expect(maxInput).toHaveValue(3);

		await userEvent.clear(minInput);
		await userEvent.type(minInput, "0.75");
		await expect(minInput).toHaveValue(0.75);
		await expect(maxInput).toHaveValue(1.5);
	},
};

export const ManualMaximum: Story = {
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const minInput = canvas.getByRole("spinbutton", { name: /Min/ });
		const maxInput = canvas.getByRole("spinbutton", { name: /Max/ });

		await userEvent.clear(maxInput);
		await userEvent.type(maxInput, "4");
		await expect(maxInput).toHaveValue(4);

		await userEvent.clear(minInput);
		await userEvent.type(minInput, "2");
		await expect(minInput).toHaveValue(2);
		await expect(maxInput).toHaveValue(4);

		await userEvent.clear(maxInput);
		await userEvent.type(maxInput, "0.75");
		await expect(maxInput).toHaveValue(0.75);
		await expect(minInput).toHaveValue(2);
		await expect(canvas.getByRole("alert")).toHaveTextContent(
			"Min must be less than or equal to Max.",
		);

		await userEvent.clear(maxInput);
		await userEvent.type(maxInput, "4");
		await expect(maxInput).toHaveValue(4);
		await expect(minInput).toHaveValue(2);
		await expect(canvas.queryByRole("alert")).not.toBeInTheDocument();
	},
};
