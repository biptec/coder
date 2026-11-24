import type { Meta, StoryObj } from "@storybook/react-vite";
import { useMemo, useState } from "react";
import { expect, screen, userEvent, within } from "storybook/test";
import { DetailedError } from "#/api/errors";
import type { Preset, PreviewParameter } from "#/api/typesGenerated";
import { chromatic } from "#/testHelpers/chromatic";
import { MockTemplate, MockUserOwner } from "#/testHelpers/entities";
import { CreateWorkspacePageView } from "./CreateWorkspacePageView";
import { mergePresetMutableParameterNames } from "./presetUtils";

const meta: Meta<typeof CreateWorkspacePageView> = {
	title: "Pages/CreateWorkspacePageView",
	parameters: { chromatic },
	component: CreateWorkspacePageView,
	render: function Render(args) {
		const names = useMemo(
			() => mergePresetMutableParameterNames(new Set(), args.parameters),
			[args.parameters],
		);
		return (
			<CreateWorkspacePageView {...args} presetMutableParameterNames={names} />
		);
	},
	args: {
		autofillParameters: [],
		diagnostics: [],
		defaultName: "",
		defaultOwner: MockUserOwner,
		externalAuth: [],
		externalAuthPollingState: {},
		hasAllRequiredExternalAuth: true,
		mode: "form",
		parameters: [],
		presetMutableParameterNames: new Set(),
		permissions: {
			createWorkspaceForAny: true,
			canUpdateTemplate: false,
		},
		presets: [],
		sendMessage: () => {},
		template: MockTemplate,
	},
};

export default meta;
type Story = StoryObj<typeof CreateWorkspacePageView>;

export const WebsocketError: Story = {
	args: {
		error: new DetailedError(
			"Websocket connection for dynamic parameters unexpectedly closed.",
			"Refresh the page to reset the form.",
		),
	},
};

export const WithViewSourceButton: Story = {
	args: {
		canUpdateTemplate: true,
		versionName: "foobar-template-version",
		template: {
			...MockTemplate,
			organization_name: "default",
			name: "docker-template",
		},
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const viewSourceLink = canvas.getByRole("link", { name: /view source/i });
		expect(viewSourceLink).toBeInTheDocument();
		expect(viewSourceLink).toHaveAttribute(
			"href",
			"/templates/default/docker-template/versions/foobar-template-version/edit",
		);
	},
};

export const ViewSourceButtonHiddenWithoutPermission: Story = {
	args: {
		canUpdateTemplate: false,
		versionName: "foobar-template-version",
	},
	play: async () => {
		expect(
			screen.queryByRole("link", { name: /view source/i }),
		).not.toBeInTheDocument();
	},
};
const parameterInput: PreviewParameter = {
	name: "workspace_name",
	display_name: "Workspace Name",
	description: "A friendly name for your workspace",
	type: "string",
	form_type: "input",
	value: { valid: true, value: "" },
	default_value: { valid: true, value: "my-workspace" },
	diagnostics: [],
	styling: { placeholder: "Enter workspace name" },
	mutable: true,
	icon: "",
	options: [],
	validations: [],
	required: true,
	order: 0,
	ephemeral: false,
};

const parameterDropdown: PreviewParameter = {
	name: "instance_type",
	display_name: "Instance Type",
	description: "The type of cloud instance to provision",
	type: "string",
	form_type: "dropdown",
	value: { valid: true, value: "t3.medium" },
	default_value: { valid: true, value: "t3.medium" },
	diagnostics: [],
	styling: { placeholder: "Select an instance type" },
	mutable: true,
	icon: "/emojis/1f4bb.png",
	options: [
		{
			name: "t3.micro",
			description: "1 vCPU, 1 GB RAM - Free tier eligible",
			value: { valid: true, value: "t3.micro" },
			icon: "/emojis/1f7e2.png",
		},
		{
			name: "t3.small",
			description: "2 vCPU, 2 GB RAM",
			value: { valid: true, value: "t3.small" },
			icon: "/emojis/1f7e1.png",
		},
		{
			name: "t3.medium",
			description: "2 vCPU, 4 GB RAM",
			value: { valid: true, value: "t3.medium" },
			icon: "/emojis/1f7e0.png",
		},
		{
			name: "t3.large",
			description: "2 vCPU, 8 GB RAM",
			value: { valid: true, value: "t3.large" },
			icon: "/emojis/1f534.png",
		},
	],
	validations: [],
	required: true,
	order: 1,
	ephemeral: false,
};

const parameterSlider: PreviewParameter = {
	name: "cpu_count",
	display_name: "CPU Count",
	description: "Number of CPU cores to allocate",
	type: "number",
	form_type: "slider",
	value: { valid: true, value: "4" },
	default_value: { valid: true, value: "2" },
	diagnostics: [],
	styling: {},
	mutable: true,
	icon: "",
	options: [],
	validations: [],
	required: true,
	order: 2,
	ephemeral: false,
};

const rangeMinParameter: PreviewParameter = {
	...parameterSlider,
	name: "cpu_min",
	display_name: "CPU",
	value: { valid: true, value: "2" },
	default_value: { valid: true, value: "2" },
	styling: {
		slider_min: 0.25,
		slider_max: 8,
		step: 0.25,
		display_unit: "vCPU",
		range_group: "cpu",
		range_role: "min",
		auto_max_multiplier: 2,
	},
	diagnostics: [
		{
			severity: "error",
			summary: "Invalid parameter range",
			detail: "Minimum value must be less than or equal to maximum value.",
			extra: { code: "range_validation" },
		},
	],
};

const rangeMaxParameter: PreviewParameter = {
	...rangeMinParameter,
	name: "cpu_max",
	display_name: "CPU Max",
	value: { valid: true, value: "1" },
	default_value: { valid: true, value: "1" },
	styling: {
		...rangeMinParameter.styling,
		range_role: "max",
		auto_max_multiplier: undefined,
	},
	diagnostics: [],
};

const parameterSwitch: PreviewParameter = {
	name: "enable_gpu",
	display_name: "Enable GPU",
	description: "Attach a GPU to the workspace for ML workloads",
	type: "bool",
	form_type: "switch",
	value: { valid: true, value: "false" },
	default_value: { valid: true, value: "false" },
	diagnostics: [],
	styling: {},
	mutable: true,
	icon: "",
	options: [],
	validations: [],
	required: false,
	order: 3,
	ephemeral: false,
};

const parameterRadio: PreviewParameter = {
	name: "region",
	display_name: "Region",
	description: "The geographic region for your workspace",
	type: "string",
	form_type: "radio",
	value: { valid: true, value: "us-west-2" },
	default_value: { valid: true, value: "us-west-2" },
	diagnostics: [],
	styling: {},
	mutable: false,
	icon: "",
	options: [
		{
			name: "US West (Oregon)",
			description: "us-west-2",
			value: { valid: true, value: "us-west-2" },
			icon: "/emojis/1f1fa-1f1f8.png",
		},
		{
			name: "US East (N. Virginia)",
			description: "us-east-1",
			value: { valid: true, value: "us-east-1" },
			icon: "/emojis/1f1fa-1f1f8.png",
		},
		{
			name: "EU West (Ireland)",
			description: "eu-west-1",
			value: { valid: true, value: "eu-west-1" },
			icon: "/emojis/1f1ea-1f1fa.png",
		},
	],
	validations: [],
	required: true,
	order: 4,
	ephemeral: false,
};

const parameterMultiSelect: PreviewParameter = {
	name: "ides",
	display_name: "IDEs",
	description: "Select which IDEs to pre-install",
	type: "list(string)",
	form_type: "multi-select",
	value: { valid: true, value: '["vscode", "cursor"]' },
	default_value: { valid: true, value: "[]" },
	diagnostics: [],
	styling: {},
	mutable: true,
	icon: "",
	options: [
		{
			name: "VS Code",
			description: "Visual Studio Code",
			value: { valid: true, value: "vscode" },
			icon: "/icon/code.svg",
		},
		{
			name: "Cursor",
			description: "Cursor IDE",
			value: { valid: true, value: "cursor" },
			icon: "/icon/cursor.svg",
		},
		{
			name: "JetBrains",
			description: "JetBrains IDEs",
			value: { valid: true, value: "jetbrains" },
			icon: "/icon/jetbrains.svg",
		},
		{
			name: "Neovim",
			description: "Neovim editor",
			value: { valid: true, value: "neovim" },
			icon: "/icon/neovim.svg",
		},
	],
	validations: [],
	required: false,
	order: 5,
	ephemeral: false,
};

const parameterTextarea: PreviewParameter = {
	name: "custom_env_vars",
	display_name: "Environment Variables",
	description:
		"Additional environment variables to set in the workspace (one per line, KEY=value format)",
	type: "string",
	form_type: "textarea",
	value: { valid: true, value: "" },
	default_value: { valid: true, value: "" },
	diagnostics: [],
	styling: { placeholder: "NODE_ENV=development\nDEBUG=true" },
	mutable: true,
	icon: "/emojis/1f4dd.png",
	options: [],
	validations: [],
	required: false,
	order: 6,
	ephemeral: false,
};

const gpuLargePreset: Preset = {
	ID: "preset-1",
	Name: "GPU Large",
	Description: "GPU Large preset",
	Parameters: [
		{ Name: "instance_type", Value: "t3.large" },
		{ Name: "enable_gpu", Value: "true" },
	],
	Default: false,
	DesiredPrebuildInstances: null,
	Icon: "/emojis/1f4bb.png",
};

const cpuSmallPreset: Preset = {
	ID: "preset-2",
	Name: "CPU Small",
	Description: "CPU Small preset",
	Parameters: [{ Name: "instance_type", Value: "t3.micro" }],
	Default: false,
	DesiredPrebuildInstances: null,
	Icon: "/emojis/1f4bc.png",
};

const urlPreset: Preset = {
	ID: "preset-url",
	Name: "URL Preset",
	Description: "The URL-specified preset",
	Parameters: [{ Name: "instance_type", Value: "t3.large" }],
	Default: false,
	DesiredPrebuildInstances: null,
	Icon: "/emojis/1f534.png",
};

const parameterCheckbox: PreviewParameter = {
	name: "auto_stop",
	display_name: "Auto-stop",
	description: "Automatically stop workspace after inactivity",
	type: "bool",
	form_type: "checkbox",
	value: { valid: true, value: "true" },
	default_value: { valid: true, value: "true" },
	diagnostics: [],
	styling: {},
	mutable: true,
	icon: "",
	options: [],
	validations: [],
	required: false,
	order: 7,
	ephemeral: false,
};

export const WithParameters: Story = {
	args: {
		parameters: [
			parameterInput,
			parameterDropdown,
			parameterSlider,
			parameterSwitch,
			parameterRadio,
			parameterMultiSelect,
			parameterTextarea,
			parameterCheckbox,
		],
	},
	parameters: {
		docs: {
			description: {
				story:
					"This story demonstrates a workspace creation form with presets and a variety of parameter types including text inputs, dropdowns, sliders, switches, radio buttons, multi-select, textarea, and checkboxes.",
			},
		},
	},
};

export const WithInvalidRangeParameters: Story = {
	args: {
		parameters: [rangeMinParameter, rangeMaxParameter],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const minInput = canvas.getByRole("spinbutton", { name: /Min/ });
		const maxInput = canvas.getByRole("spinbutton", { name: /Max/ });

		await expect(
			canvas.getByText(
				"Minimum value must be less than or equal to maximum value.",
			),
		).toBeVisible();
		await expect(minInput).toBeEnabled();
		await expect(maxInput).toBeEnabled();
		await expect(
			canvas.getByRole("button", { name: /create workspace/i }),
		).toBeDisabled();
	},
};

export const WithTooLongPrefilledName: Story = {
	args: {
		defaultName: "this-name-is-way-too-long-and-exceeds-the-limit",
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await expect(
			canvas.findByText(/Workspace Name cannot be longer than 32 characters/i),
		).resolves.toBeVisible();
	},
};

export const WithPresets: Story = {
	args: {
		presets: [
			{
				ID: "preset-1",
				Name: "Preset 1",
				Description: "Preset 1 description",
				Parameters: [{ Name: "workspace_name", Value: "my-workspace" }],
				Default: false,
				DesiredPrebuildInstances: null,
				Icon: "/emojis/1f4bb.png",
			},
			{
				ID: "preset-2",
				Name: "Preset 2",
				Description: "Preset 2 description",
				Parameters: [{ Name: "workspace_name", Value: "my-workspace-2" }],
				Default: false,
				DesiredPrebuildInstances: null,
				Icon: "/emojis/1f4bc.png",
			},
		],
		parameters: [
			{
				...parameterInput,
				styling: { ...parameterInput.styling, preset_mutable: true },
			},
			parameterDropdown,
		],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const parameterField = within(
			canvas.getByTestId("parameter-field-workspace_name"),
		);
		const input = parameterField.getByRole("textbox");

		await userEvent.click(canvas.getByRole("button", { name: "Preset 1" }));

		await expect(input).toHaveValue("my-workspace");
		await expect(input).toBeEnabled();
	},
};

export const WithConditionalDockerPresets: Story = {
	args: {
		presets: [
			{
				...cpuSmallPreset,
				ID: "micro",
				Name: "Micro",
				Parameters: [{ Name: "enable_docker", Value: "false" }],
			},
			{
				...cpuSmallPreset,
				ID: "medium",
				Name: "Medium",
				Parameters: [
					{ Name: "enable_docker", Value: "true" },
					{ Name: "docker_cpu", Value: "4" },
				],
			},
		],
	},
	render: function Render(args) {
		const [knownNames] = useState(
			() => new Set(["enable_docker", "docker_cpu"]),
		);
		const [values, setValues] = useState<Record<string, string>>({
			enable_docker: "true",
			docker_cpu: "2",
		});
		const parameters = useMemo<PreviewParameter[]>(() => {
			const enableDocker: PreviewParameter = {
				...parameterCheckbox,
				name: "enable_docker",
				display_name: "Enable Docker",
				value: { valid: true, value: values.enable_docker },
				styling: { preset_mutable: true },
			};
			const dockerCpu: PreviewParameter = {
				...parameterSlider,
				name: "docker_cpu",
				display_name: "Docker CPU",
				value: { valid: true, value: values.docker_cpu },
				styling: { preset_mutable: true },
			};
			return values.enable_docker === "true"
				? [enableDocker, dockerCpu]
				: [enableDocker];
		}, [values]);
		return (
			<CreateWorkspacePageView
				{...args}
				parameters={parameters}
				presetMutableParameterNames={knownNames}
				sendMessage={(inputs) => {
					setValues((previous) => ({ ...previous, ...inputs }));
				}}
			/>
		);
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(canvas.getByRole("button", { name: "Micro" }));
		await expect(
			canvas.queryByTestId("parameter-field-docker_cpu"),
		).not.toBeInTheDocument();
		await expect(canvas.getByRole("button", { name: "Medium" })).toBeVisible();
		await userEvent.click(canvas.getByRole("button", { name: "Medium" }));
		const docker = await canvas.findByTestId("parameter-field-docker_cpu");
		await expect(within(docker).getByRole("spinbutton")).toHaveValue(4);
		await expect(within(docker).getByRole("spinbutton")).toBeEnabled();
	},
};

export const WithUrlPreset: Story = {
	args: {
		presets: [gpuLargePreset, cpuSmallPreset],
		urlPreset: gpuLargePreset,
		parameters: [
			{
				...parameterDropdown,
				styling: { ...parameterDropdown.styling, preset_mutable: true },
			},
			{
				...parameterSwitch,
				styling: { ...parameterSwitch.styling, preset_mutable: true },
			},
		],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		expect(
			canvas.getByRole("button", { name: /GPU Large/i }),
		).toBeInTheDocument();
	},
};

export const WithLegacyLockedPreset: Story = {
	args: {
		presets: [gpuLargePreset],
		urlPreset: gpuLargePreset,
		parameters: [parameterDropdown],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(
			canvas.getByRole("switch", { name: /show preset parameters/i }),
		);
		const parameterField = within(
			canvas.getByTestId("parameter-field-instance_type"),
		);
		await expect(parameterField.getByRole("combobox")).toBeDisabled();
	},
};

export const WithPrebuildPreset: Story = {
	args: {
		presets: [
			{
				...gpuLargePreset,
				DesiredPrebuildInstances: 1,
			},
		],
		urlPreset: {
			...gpuLargePreset,
			DesiredPrebuildInstances: 1,
		},
		parameters: [parameterDropdown],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(
			canvas.getByRole("switch", { name: /show preset parameters/i }),
		);
		const parameterField = within(
			canvas.getByTestId("parameter-field-instance_type"),
		);
		await expect(parameterField.getByRole("combobox")).toBeDisabled();
	},
};

export const WithUrlPresetNotFound: Story = {
	args: {
		presets: [gpuLargePreset],
		urlPresetError:
			'Preset "gpu-large" not found on template version "test-version". Check that the preset name matches exactly (names are case-sensitive).',
		parameters: [parameterDropdown],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		expect(
			canvas.getByText(/Preset "gpu-large" not found on template version/i),
		).toBeVisible();
	},
};

export const WithUrlPresetAndIgnoredParams: Story = {
	args: {
		presets: [gpuLargePreset],
		urlPreset: gpuLargePreset,
		hasIgnoredUrlParams: true,
		parameters: [parameterDropdown],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		expect(canvas.getAllByText(/param\.\*/i).length).toBeGreaterThan(0);
	},
};

export const WithUrlPresetOverridesDefault: Story = {
	args: {
		presets: [
			{
				ID: "preset-default",
				Name: "Default Preset",
				Description: "The default preset",
				Parameters: [{ Name: "instance_type", Value: "t3.micro" }],
				Default: true,
				DesiredPrebuildInstances: null,
				Icon: "/emojis/1f7e2.png",
			},
			urlPreset,
		],
		urlPreset,
		parameters: [
			{
				...parameterDropdown,
				styling: { ...parameterDropdown.styling, preset_mutable: true },
			},
		],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		expect(
			canvas.getByRole("button", { name: /URL Preset/i }),
		).toBeInTheDocument();
	},
};
