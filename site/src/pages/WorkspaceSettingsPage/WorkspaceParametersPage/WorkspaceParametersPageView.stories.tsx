import type { Meta, StoryObj } from "@storybook/react-vite";
import { useState } from "react";
import {
	expect,
	fireEvent,
	fn,
	userEvent,
	waitFor,
	within,
} from "storybook/test";
import type { PreviewParameter } from "#/api/typesGenerated";
import { MockPreviewParameter, MockWorkspace } from "#/testHelpers/entities";
import { WorkspaceParametersPageView } from "./WorkspaceParametersPageView";

const diskParameter = (
	name: string,
	displayName: string,
	value: string,
	monotonic: "increasing" | "decreasing" | null = "increasing",
): PreviewParameter => ({
	...MockPreviewParameter,
	name,
	display_name: displayName,
	description: "",
	type: "number",
	form_type: "slider",
	mutable: true,
	required: true,
	ephemeral: false,
	value: { valid: true, value },
	default_value: { valid: true, value },
	options: [],
	diagnostics: [],
	validations: [
		{
			validation_min: 1,
			validation_max: 80,
			validation_monotonic: monotonic,
			validation_error: "",
			validation_regex: null,
		},
	],
	styling: { display_unit: "GiB" },
});

const home = diskParameter("home_disk_size", "Home disk", "20");
const docker = diskParameter("docker_data_disk_size", "Docker data disk", "32");
const meta = {
	title: "pages/WorkspaceParametersPage/WorkspaceParametersPageView",
	component: WorkspaceParametersPageView,
	args: {
		workspace: MockWorkspace,
		parameters: [home, docker],
		autofillParameters: [
			{ name: home.name, value: "20", source: "active_build" },
			{ name: docker.name, value: "32", source: "active_build" },
		],
		diagnostics: [],
		canChangeVersions: true,
		isSubmitting: false,
		submitLabel: "Update and restart",
		onCancel: fn(),
		onSubmit: fn(),
		sendMessage: fn(),
		templateVersionId: undefined,
	},
	render: function Render(args) {
		const [parameters, setParameters] = useState(args.parameters);
		return (
			<WorkspaceParametersPageView
				{...args}
				parameters={parameters}
				sendMessage={(values) => {
					args.sendMessage(values);
					setParameters((previous) =>
						previous.map((parameter) => ({
							...parameter,
							value: {
								valid: true,
								value: values[parameter.name] ?? parameter.value.value,
							},
						})),
					);
				}}
			/>
		);
	},
} satisfies Meta<typeof WorkspaceParametersPageView>;

export default meta;
type Story = StoryObj<typeof meta>;

export const ExistingDiskCannotShrink: Story = {
	play: async ({ canvasElement, args }) => {
		const canvas = within(canvasElement);
		const homeField = within(
			canvas.getByTestId("parameter-field-home_disk_size"),
		);
		const dockerField = within(
			canvas.getByTestId("parameter-field-docker_data_disk_size"),
		);
		const input = homeField.getByRole("spinbutton");
		const thumb = homeField.getByRole("slider", { name: "Home disk" });
		const submit = canvas.getByRole("button", { name: "Update and restart" });

		await expect(input).toHaveAttribute("min", "20");
		await expect(thumb).toHaveAttribute("aria-valuemin", "20");
		await expect(dockerField.getByRole("spinbutton")).toHaveAttribute(
			"min",
			"32",
		);
		await expect(dockerField.getByRole("slider")).toHaveAttribute(
			"aria-valuemin",
			"32",
		);
		await userEvent.clear(input);
		await userEvent.type(input, "40");
		await waitFor(() => expect(submit).toBeEnabled());
		await expect(thumb).toHaveAttribute("aria-valuemin", "20");
		await userEvent.clear(input);
		await userEvent.type(input, "25");
		await waitFor(() => expect(submit).toBeEnabled());
		await expect(input).toHaveValue(25);
		await userEvent.click(thumb);
		await userEvent.keyboard("{Home}");
		await expect(input).toHaveValue(20);

		await userEvent.clear(input);
		await userEvent.type(input, "10");
		await waitFor(() =>
			expect(args.sendMessage).toHaveBeenLastCalledWith(
				expect.objectContaining({ home_disk_size: "10" }),
			),
		);
		await expect(submit).toBeDisabled();
		await expect(homeField.getByRole("alert")).toHaveTextContent("20");
		await fireEvent.submit(canvas.getByTestId("form"));
		await expect(args.onSubmit).not.toHaveBeenCalled();

		await userEvent.clear(input);
		await userEvent.type(input, "24");
		await waitFor(() => expect(submit).toBeEnabled());
		await expect(thumb).toHaveAttribute("aria-valuemin", "20");
		await userEvent.click(submit);
		await waitFor(() =>
			expect(args.onSubmit).toHaveBeenCalledWith(
				expect.objectContaining({
					rich_parameter_values: expect.arrayContaining([
						{ name: home.name, value: "24" },
					]),
				}),
			),
		);
	},
};

export const MigrationTemplateAllowsShrink: Story = {
	args: {
		parameters: [diskParameter(home.name, "Home disk", "20", null)],
		autofillParameters: [
			{ name: home.name, value: "20", source: "active_build" },
		],
	},
	play: async ({ canvasElement, args }) => {
		const canvas = within(canvasElement);
		const input = canvas.getByRole("spinbutton");
		await expect(input).toHaveAttribute("min", "1");
		await expect(canvas.getByRole("slider")).toHaveAttribute(
			"aria-valuemin",
			"1",
		);
		await userEvent.clear(input);
		await userEvent.type(input, "10");
		const submit = canvas.getByRole("button", { name: "Update and restart" });
		await waitFor(() => expect(submit).toBeEnabled());
		await expect(canvas.queryByRole("alert")).not.toBeInTheDocument();
		await userEvent.click(submit);
		await waitFor(() => expect(args.onSubmit).toHaveBeenCalled());
	},
};

export const DiskAtMaximum: Story = {
	args: {
		parameters: [diskParameter(home.name, "Home disk", "80")],
		autofillParameters: [
			{ name: home.name, value: "80", source: "active_build" },
		],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const input = canvas.getByRole("spinbutton");
		await expect(input).toHaveAttribute("min", "80");
		await expect(input).toHaveAttribute("max", "80");
		await expect(input).toHaveValue(80);
		await expect(canvas.queryByRole("slider")).not.toBeInTheDocument();
		await expect(
			canvas.getByRole("button", { name: "Update and restart" }),
		).toBeEnabled();
	},
};

export const MonotonicDecrease: Story = {
	args: {
		parameters: [diskParameter(home.name, "Quota", "20", "decreasing")],
		autofillParameters: [
			{ name: home.name, value: "20", source: "active_build" },
		],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const input = canvas.getByRole("spinbutton");
		await expect(input).toHaveAttribute("max", "20");
		await expect(canvas.getByRole("slider")).toHaveAttribute(
			"aria-valuemax",
			"20",
		);
		await userEvent.clear(input);
		await userEvent.type(input, "30");
		await expect(canvas.getByRole("alert")).toHaveTextContent("20");
		await expect(
			canvas.getByRole("button", { name: "Update and restart" }),
		).toBeDisabled();
	},
};
