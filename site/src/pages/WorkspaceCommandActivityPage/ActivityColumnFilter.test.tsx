import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderComponent } from "#/testHelpers/renderHelpers";
import { MultiSelectColumnFilter } from "./ActivityColumnFilter";

describe("MultiSelectColumnFilter", () => {
	it("keeps multi-select changes local until Apply and does not close after each click", async () => {
		const user = userEvent.setup();
		const onApply = vi.fn();
		renderComponent(
			<MultiSelectColumnFilter
				label="Tool"
				options={[
					{ value: "exec", label: "exec" },
					{ value: "process_output", label: "process_output" },
					{ value: "read_file", label: "read_file" },
				]}
				selected={null}
				nullMeansAll
				searchPlaceholder="Search tools..."
				summary="All"
				onApply={onApply}
			/>,
		);

		await user.click(screen.getByRole("button", { name: "Filter Tool" }));
		await user.click(screen.getByText("process_output"));
		await user.click(screen.getByText("read_file"));

		expect(onApply).not.toHaveBeenCalled();
		expect(screen.getByRole("button", { name: "Apply" })).toBeVisible();
		expect(screen.getByPlaceholderText("Search tools...")).toBeVisible();

		await user.click(screen.getByRole("button", { name: "Apply" }));
		expect(onApply).toHaveBeenCalledTimes(1);
		expect(onApply).toHaveBeenCalledWith(["exec"]);
	});

	it("discards draft changes when the popup is cancelled", async () => {
		const user = userEvent.setup();
		const onApply = vi.fn();
		renderComponent(
			<MultiSelectColumnFilter
				label="Status"
				options={[
					{ value: "running", label: "Running" },
					{ value: "succeeded", label: "Succeeded" },
				]}
				selected={["running", "succeeded"]}
				searchPlaceholder="Search statuses..."
				summary="2/2"
				onApply={onApply}
			/>,
		);

		await user.click(screen.getByRole("button", { name: "Filter Status" }));
		await user.click(screen.getByText("Succeeded"));
		await user.click(screen.getByRole("button", { name: "Cancel" }));
		expect(onApply).not.toHaveBeenCalled();
	});
});
