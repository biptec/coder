import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";
import { SelectionSummary } from "./SelectionSummary";

const meta: Meta<typeof SelectionSummary> = {
	title: "pages/TemplateBuilder/SelectionSummary",
	component: SelectionSummary,
	args: {
		maxReachedStep: 3,
		onNavigateStep: fn(),
		onNavigateModule: fn(),
	},
};

export default meta;
type Story = StoryObj<typeof SelectionSummary>;

export const NoSelection: Story = {
	args: {
		currentStep: 0,
		maxReachedStep: 1,
		selectedTemplate: undefined,
		selectedModules: undefined,
	},
};

export const BaseTemplateStep: Story = {
	args: {
		currentStep: 1,
		maxReachedStep: 1,
		selectedTemplate: undefined,
		selectedModules: undefined,
	},
};

export const WithBaseTemplate: Story = {
	args: {
		currentStep: 1,
		maxReachedStep: 1,
		selectedTemplate: {
			name: "Docker Containers",
			iconUrl: "/icon/docker.svg",
		},
	},
};

export const ModulesStep: Story = {
	args: {
		currentStep: 2,
		maxReachedStep: 2,
		selectedTemplate: {
			name: "Docker Containers",
			iconUrl: "/icon/docker.svg",
		},
		selectedModules: undefined,
	},
};

export const WithModules: Story = {
	args: {
		currentStep: 2,
		selectedTemplate: {
			name: "Docker Containers",
			iconUrl: "/icon/docker.svg",
		},
		selectedModules: [
			{
				id: "jetbrains",
				name: "JetBrains",
				iconUrl: "/icon/jetbrains.svg",
			},
			{
				id: "jetbrains-toolbox",
				name: "JetBrains Toolbox",
				iconUrl: "/icon/jetbrains-toolbox.svg",
			},
			{
				id: "cursor",
				name: "Cursor IDE",
				iconUrl: "/icon/cursor.svg",
			},
			{
				id: "claude-code",
				name: "Claude Code",
				iconUrl: "/icon/claude.svg",
			},
			{
				id: "filebrowser",
				name: "File browser",
				iconUrl: "/icon/filebrowser.svg",
			},
			{
				id: "git-clone",
				name: "Git clone",
				iconUrl: "/icon/git.svg",
			},
			{
				id: "devcontainers",
				name: "Devcontainers",
				iconUrl: "/icon/devcontainers.svg",
			},
		],
	},
};

export const WithLongNameModule: Story = {
	args: {
		currentStep: 2,
		selectedTemplate: {
			name: "Docker Containers",
			iconUrl: "/icon/docker.svg",
		},
		selectedModules: [
			{
				id: "git-commit-signing",
				name: "A module with a name long enough to cause the text inside the ModuleSelection component to wrap to the next line, showing that the icon on the left remains top-aligned with the first line of the module name",
				iconUrl: "/icon/git.svg",
			},
		],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);

		// Long module names must not be truncated and must remain visible in
		// the sidebar. The row is a single navigation button.
		const moduleBtn = await canvas.findByRole("button", {
			name: /^Configure A module/i,
		});
		await expect(moduleBtn).toBeVisible();
	},
};

export const ManyModules: Story = {
	args: {
		currentStep: 2,
		selectedTemplate: {
			name: "Docker Containers",
			iconUrl: "/icon/docker.svg",
		},
		selectedModules: Array.from({ length: 12 }, (_, i) => ({
			id: `module-${i}`,
			name: `Module ${i + 1}`,
			iconUrl: "/icon/docker.svg",
		})),
	},
};

export const Customizations: Story = {
	args: {
		currentStep: 3,
		selectedTemplate: {
			name: "Docker Containers",
			iconUrl: "/icon/docker.svg",
		},
		selectedModules: [
			{ id: "claude-code", name: "Claude Code", iconUrl: "/icon/claude.svg" },
			{ id: "cursor", name: "Cursor IDE", iconUrl: "/icon/cursor.svg" },
		],
	},
};

export const BackwardNavigation: Story = {
	// User advanced to step 3, then clicked back to step 1. Step 3 should
	// still look reachable and clickable; nothing beyond max-reached should
	// appear as an upcoming step.
	args: {
		currentStep: 1,
		maxReachedStep: 3,
		selectedTemplate: {
			name: "Docker Containers",
			iconUrl: "/icon/docker.svg",
		},
		selectedModules: [
			{ id: "claude-code", name: "Claude Code", iconUrl: "/icon/claude.svg" },
		],
	},
	play: async ({ canvasElement }) => {
		// Both dividers must be rendered in the completed variant because
		// the user has walked past both. Regression check for the divider
		// colour flipping back to grey after backward navigation.
		const dividers = canvasElement.querySelectorAll(
			"[class*='border-border-success']",
		);
		await expect(dividers.length).toBeGreaterThanOrEqual(2);
	},
};

export const UpcomingStepsInert: Story = {
	// User is on step 1 with nothing selected yet. Steps 2 and 3 must be
	// rendered without a button so they are not clickable and have no
	// hover treatment.
	args: {
		currentStep: 1,
		maxReachedStep: 1,
		selectedTemplate: undefined,
		selectedModules: undefined,
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const modulesLabel = canvas.getByText("Modules");
		const customizationsLabel = canvas.getByText("Customizations");
		// Neither label should be inside a button element.
		await expect(modulesLabel.closest("button")).toBeNull();
		await expect(customizationsLabel.closest("button")).toBeNull();
	},
};

export const NavigationClicks: Story = {
	args: {
		currentStep: 3,
		selectedTemplate: {
			name: "Docker Containers",
			iconUrl: "/icon/docker.svg",
		},
		selectedModules: [
			{ id: "claude-code", name: "Claude Code", iconUrl: "/icon/claude.svg" },
			{ id: "cursor", name: "Cursor IDE", iconUrl: "/icon/cursor.svg" },
		],
	},
	play: async ({ args, canvasElement }) => {
		const canvas = within(canvasElement);

		const baseTemplateBtn = await canvas.findByRole("button", {
			name: "Go to Base Template",
		});
		await userEvent.click(baseTemplateBtn);
		await expect(args.onNavigateStep).toHaveBeenCalledWith("base-infra");

		const selectedBaseBtn = await canvas.findByRole("button", {
			name: "Configure Docker Containers",
		});
		await userEvent.click(selectedBaseBtn);
		await expect(args.onNavigateStep).toHaveBeenCalledWith("base-parameters");

		const modulesBtn = await canvas.findByRole("button", {
			name: "Go to Modules",
		});
		await userEvent.click(modulesBtn);
		await expect(args.onNavigateStep).toHaveBeenCalledWith("module-select");

		const customizationsBtn = await canvas.findByRole("button", {
			name: "Go to Customizations",
		});
		await userEvent.click(customizationsBtn);
		await expect(args.onNavigateStep).toHaveBeenCalledWith("customizations");

		const moduleBtn = await canvas.findByRole("button", {
			name: "Configure Claude Code",
		});
		await userEvent.click(moduleBtn);
		await expect(args.onNavigateModule).toHaveBeenCalledWith("claude-code");
	},
};
