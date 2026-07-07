import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";
import { createMockFile } from "#/testHelpers/files";
import type { WorkspaceFileUpload } from "../hooks/useWorkspaceFileUploads";
import { WorkspaceUploadPreview } from "./WorkspaceUploadPreview";

const uploadedEntry = (name: string, size = 4096): WorkspaceFileUpload => ({
	id: `uploaded-${name}`,
	file: createMockFile(name, "application/zip"),
	status: "uploaded",
	response: {
		path: `/home/coder/.coder/chats/chat-1/files/${name}`,
		name,
		size,
		media_type: "application/zip",
		workspace_id: "ws-1",
	},
});

const meta: Meta<typeof WorkspaceUploadPreview> = {
	title: "pages/AgentsPage/WorkspaceUploadPreview",
	component: WorkspaceUploadPreview,
	args: {
		onRemove: fn(),
	},
};

export default meta;
type Story = StoryObj<typeof WorkspaceUploadPreview>;

export const Uploaded: Story = {
	args: {
		uploads: [uploadedEntry("design-handoff.zip")],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		expect(canvas.getByText("design-handoff.zip")).toBeInTheDocument();
		expect(canvas.getByText("4.1 kB in workspace")).toBeInTheDocument();
	},
};

export const Uploading: Story = {
	args: {
		uploads: [
			{
				id: "uploading-1",
				file: createMockFile("dataset.tar.gz", "application/gzip"),
				status: "uploading",
			},
		],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		expect(canvas.getByText("Uploading to workspace...")).toBeInTheDocument();
	},
};

export const UploadError: Story = {
	args: {
		uploads: [
			{
				id: "error-1",
				file: createMockFile("broken.zip", "application/zip"),
				status: "error",
				error: "Failed to upload file to workspace agent.",
			},
		],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		expect(
			canvas.getByText("Failed to upload file to workspace agent."),
		).toBeInTheDocument();
	},
};

export const MixedStates: Story = {
	args: {
		uploads: [
			uploadedEntry("release.zip", 2 * 1024 * 1024),
			{
				id: "uploading-2",
				file: createMockFile("video.mp4", "video/mp4"),
				status: "uploading",
			},
			{
				id: "error-2",
				file: createMockFile("huge.iso", "application/x-iso9660-image"),
				status: "error",
				error: "Upload failed",
			},
		],
	},
};

export const RemoveUploadedKeepsBytesCopy: Story = {
	args: {
		uploads: [uploadedEntry("design-handoff.zip")],
	},
	play: async ({ args, canvasElement }) => {
		const canvas = within(canvasElement);
		const removeButton = canvas.getByRole("button", {
			name: "Remove design-handoff.zip",
		});
		await userEvent.click(removeButton);
		expect(args.onRemove).toHaveBeenCalledWith("uploaded-design-handoff.zip");
	},
};
