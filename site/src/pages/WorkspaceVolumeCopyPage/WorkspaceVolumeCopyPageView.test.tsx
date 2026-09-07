import { render, screen } from "@testing-library/react";
import type { WorkspaceVolumeCopyOperation } from "#/api/typesGenerated";
import { OperationStatus } from "./WorkspaceVolumeCopyPageView";

const operation: WorkspaceVolumeCopyOperation = {
	id: "volume-copy-operation",
	created_at: "2026-09-06T19:30:00Z",
	updated_at: "2026-09-06T19:32:00Z",
	initiator_id: "user-id",
	source_workspace_id: "source-workspace",
	destination_workspace_id: "destination-workspace",
	allow_source_running: true,
	volumes: [{ key: "home", overwrite: false }],
	status: "succeeded",
	started_at: "2026-09-06T19:30:05Z",
	completed_at: "2026-09-06T19:32:00Z",
};

describe("OperationStatus", () => {
	it("uses only the Alert success icon", () => {
		render(<OperationStatus operation={operation} />);

		const alert = screen.getByRole("alert");
		expect(alert).toHaveTextContent("Persistent volume copy completed.");
		expect(alert.querySelectorAll("svg")).toHaveLength(1);
	});

	it("uses only the Alert error icon", () => {
		render(
			<OperationStatus
				operation={{
					...operation,
					status: "failed",
					error: "copy failed",
				}}
			/>,
		);

		const alert = screen.getByRole("alert");
		expect(alert).toHaveTextContent("Persistent volume copy failed.");
		expect(alert).toHaveTextContent("copy failed");
		expect(alert.querySelectorAll("svg")).toHaveLength(1);
	});
});
