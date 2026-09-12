import type { Meta, StoryObj } from "@storybook/react-vite";
import type { FC } from "react";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";
import { reactRouterParameters } from "storybook-addon-remix-react-router";
import {
	workspaceByIdKey,
	workspaceByOwnerAndNameKey,
	workspacesKey,
	workspaceVolumeCopyVolumes,
} from "#/api/queries/workspaces";
import type {
	Workspace,
	WorkspaceVolumeCopyOperation,
	WorkspaceVolumeCopyVolume,
} from "#/api/typesGenerated";
import { DashboardContext } from "#/modules/dashboard/DashboardProvider";
import type { WorkspacePermissions } from "#/modules/workspaces/permissions";
import {
	MockAppearanceConfig,
	MockBuildInfo,
	MockDefaultOrganization,
	MockEntitlements,
	MockStoppedWorkspace,
	MockWorkspace,
} from "#/testHelpers/entities";
import WorkspaceVolumeCopyPage from "./WorkspaceVolumeCopyPage";

const sourceStopped: Workspace = {
	...MockStoppedWorkspace,
	id: "source-workspace",
	name: "network-idm",
	owner_name: "coder-admin",
};

const sourceRunning: Workspace = {
	...MockWorkspace,
	id: sourceStopped.id,
	name: sourceStopped.name,
	owner_name: sourceStopped.owner_name,
};

const destination: Workspace = {
	...MockStoppedWorkspace,
	id: "destination-workspace",
	name: "identity-management",
	owner_name: "developer",
};

const destinationRunning: Workspace = {
	...MockWorkspace,
	id: destination.id,
	name: destination.name,
	owner_name: destination.owner_name,
};

const additionalDestinations: Workspace[] = Array.from(
	{ length: 14 },
	(_, index) => ({
		...MockStoppedWorkspace,
		id: `destination-workspace-${index}`,
		name: `workspace-${String(index + 1).padStart(2, "0")}`,
		owner_name: index % 2 === 0 ? "developer" : "coder-admin",
	}),
);

const homeVolume: WorkspaceVolumeCopyVolume = {
	key: "home",
	display_name: "Home",
	mount_path: "/home/coder",
	capacity: "20Gi",
	excluded_paths: [
		".ssh/id_ed25519_workspace",
		".ssh/id_ed25519_workspace.pub",
		".ssh/config.d/00-coder-workspace-github.conf",
		".local/state/coder-command-activity",
		".local/state/coder",
		".developer-workspace-seeded",
	],
};

const runningLiveOperation: WorkspaceVolumeCopyOperation = {
	id: "volume-copy-operation",
	created_at: "2026-09-06T19:30:00Z",
	updated_at: "2026-09-06T19:31:00Z",
	initiator_id: "user-id",
	source_workspace_id: sourceRunning.id,
	destination_workspace_id: destination.id,
	allow_source_running: true,
	volumes: [{ key: "home", overwrite: false }],
	status: "running",
	started_at: "2026-09-06T19:30:05Z",
};

const completedLiveOperation: WorkspaceVolumeCopyOperation = {
	...runningLiveOperation,
	updated_at: "2026-09-06T19:32:00Z",
	status: "succeeded",
	completed_at: "2026-09-06T19:32:00Z",
};

const volumeCopyPermissions: WorkspacePermissions = {
	readWorkspace: true,
	shareWorkspace: true,
	updateWorkspace: true,
	volumeCopyWorkspace: true,
	updateWorkspaceVersion: true,
	deleteFailedWorkspace: true,
};

const withVolumeCopyDashboard = (Story: FC) => (
	<DashboardContext.Provider
		value={{
			entitlements: MockEntitlements,
			experiments: [],
			appearance: {
				...MockAppearanceConfig,
				workspace_volume_copy_enabled: true,
			},
			buildInfo: MockBuildInfo,
			organizations: [MockDefaultOrganization],
			showOrganizations: false,
			canViewOrganizationSettings: false,
		}}
	>
		<Story />
	</DashboardContext.Provider>
);

const meta = {
	title: "pages/WorkspaceVolumeCopyPage",
	component: WorkspaceVolumeCopyPage,
	decorators: [withVolumeCopyDashboard],
	parameters: {
		layout: "fullscreen",
	},
} satisfies Meta<typeof WorkspaceVolumeCopyPage>;

export default meta;
type Story = StoryObj<typeof meta>;

const workspaceQueries = (
	source: Workspace,
	operation?: WorkspaceVolumeCopyOperation,
	listedWorkspaces: Workspace[] = [source, destination],
	selectedDestination: Workspace = destination,
) => [
	{
		key: workspaceByOwnerAndNameKey(source.owner_name, source.name),
		data: source,
	},
	{
		key: ["workspaces", source.id, "permissions"],
		data: volumeCopyPermissions,
	},
	{
		key: workspaceVolumeCopyVolumes(source.id).queryKey,
		data: [homeVolume],
	},
	{
		key: workspacesKey({ limit: 25, offset: 0, q: "" }),
		data: { workspaces: listedWorkspaces, count: listedWorkspaces.length },
	},
	{
		key: workspaceByIdKey(selectedDestination.id),
		data: selectedDestination,
	},
	{
		key: ["workspaces", selectedDestination.id, "permissions"],
		data: volumeCopyPermissions,
	},
	{
		key: workspaceVolumeCopyVolumes(selectedDestination.id).queryKey,
		data: [homeVolume],
	},
	...(operation
		? [
				{
					key: ["workspace-volume-copies", operation.id],
					data: operation,
				},
			]
		: []),
];

const routerParameters = (source: Workspace, operationId?: string) =>
	reactRouterParameters({
		location: {
			pathParams: {
				username: `@${source.owner_name}`,
				workspace: source.name,
			},
			...(operationId ? { searchParams: { operation: operationId } } : {}),
		},
		routing: {
			path: "/:username/:workspace/volume-copy",
		},
	});

export const StoppedSource: Story = {
	parameters: {
		reactRouter: routerParameters(sourceStopped),
		queries: workspaceQueries(sourceStopped),
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(
			canvas.getByRole("button", { name: "Select destination workspace" }),
		);
		await userEvent.click(
			screen.getByText("developer/identity-management", { exact: true }),
		);

		await waitFor(() => {
			expect(canvas.getByText("Home", { exact: true })).toBeVisible();
			expect(
				canvas.getByRole("button", { name: "Copy volumes" }),
			).toBeEnabled();
		});

		await userEvent.click(
			canvas.getByRole("checkbox", { name: "Overwrite existing files" }),
		);
		expect(
			canvas.getByRole("checkbox", { name: "Overwrite existing files" }),
		).toBeChecked();
	},
};

export const RunningDestination: Story = {
	parameters: {
		reactRouter: routerParameters(sourceStopped),
		queries: workspaceQueries(
			sourceStopped,
			undefined,
			[sourceStopped, destinationRunning],
			destinationRunning,
		),
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(
			canvas.getByRole("button", { name: "Select destination workspace" }),
		);
		await userEvent.click(
			screen.getByText("developer/identity-management", { exact: true }),
		);

		await waitFor(() => {
			expect(
				canvas.getByText("Destination workspace is running."),
			).toBeVisible();
			expect(
				canvas.getByRole("button", { name: "Copy volumes" }),
			).toBeEnabled();
		});
	},
};

export const DestinationDropdownOpen: Story = {
	parameters: {
		reactRouter: routerParameters(sourceStopped),
		queries: workspaceQueries(sourceStopped, undefined, [
			sourceStopped,
			destination,
			...additionalDestinations,
		]),
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(
			canvas.getByRole("button", { name: "Select destination workspace" }),
		);
		expect(
			screen.getByPlaceholderText("Search owner or workspace..."),
		).toBeVisible();
	},
};

export const RunningSource: Story = {
	parameters: {
		reactRouter: routerParameters(sourceRunning),
		queries: workspaceQueries(sourceRunning),
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(
			canvas.getByRole("button", { name: "Select destination workspace" }),
		);
		await userEvent.click(
			screen.getByText("developer/identity-management", { exact: true }),
		);

		await waitFor(() => {
			const sourceSection = canvas
				.getByRole("heading", { name: "Source" })
				.closest("section");
			expect(sourceSection).not.toBeNull();
			expect(
				within(sourceSection as HTMLElement).getByText(
					"Source workspace is running.",
				),
			).toBeVisible();
			expect(
				canvas.getByRole("button", { name: "Copy volumes" }),
			).toBeEnabled();
			expect(
				canvas.queryByRole("checkbox", {
					name: "Allow copying while source workspace is running",
				}),
			).not.toBeInTheDocument();
		});
	},
};

export const ReloadedRunningLiveCopy: Story = {
	parameters: {
		reactRouter: routerParameters(sourceRunning, runningLiveOperation.id),
		queries: workspaceQueries(sourceRunning, runningLiveOperation),
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		expect(
			await canvas.findByText("Source workspace is running."),
		).toBeVisible();
		expect(
			canvas.queryByRole("checkbox", {
				name: "Allow copying while source workspace is running",
			}),
		).not.toBeInTheDocument();
	},
};

export const CompletedLiveCopy: Story = {
	parameters: {
		reactRouter: routerParameters(sourceStopped, completedLiveOperation.id),
		queries: workspaceQueries(sourceStopped, completedLiveOperation),
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		const successAlert = await canvas.findByRole("alert");
		expect(successAlert).toHaveTextContent("Persistent volume copy completed.");
		expect(successAlert.querySelectorAll("svg")).toHaveLength(1);
		expect(
			canvas.queryByRole("checkbox", {
				name: "Allow copying while source workspace is running",
			}),
		).not.toBeInTheDocument();
	},
};
