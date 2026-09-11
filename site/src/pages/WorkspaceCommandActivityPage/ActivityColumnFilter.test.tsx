import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	InputOutputColumnFilter,
	MultiSelectColumnFilter,
} from "./ActivityColumnFilter";

describe("MultiSelectColumnFilter", () => {
	it("keeps multi-select changes local until Apply and does not close after each click", async () => {
		const user = userEvent.setup();
		const onApply = vi.fn();
		render(
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
		render(
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

describe("InputOutputColumnFilter", () => {
	it("applies search and display settings together while keeping one content source selected", async () => {
		const user = userEvent.setup();
		const onApply = vi.fn();
		render(
			<InputOutputColumnFilter
				value=""
				placeholder="Search input / output..."
				options={{
					showInput: true,
					showOutput: true,
					showEnvironment: false,
					showFullContent: false,
					useColors: false,
				}}
				onApply={onApply}
			/>,
		);

		await user.click(
			screen.getByRole("button", { name: "Filter Input / Output" }),
		);
		await user.type(
			screen.getByPlaceholderText("Search input / output..."),
			"needle",
		);
		await user.click(screen.getByRole("checkbox", { name: "Output" }));
		await user.click(screen.getByRole("checkbox", { name: "Input" }));
		expect(screen.getByRole("checkbox", { name: "Input" })).toBeChecked();
		await user.click(
			screen.getByRole("checkbox", { name: "Show environment variables" }),
		);
		await user.click(
			screen.getByRole("checkbox", { name: "Show full content" }),
		);
		await user.click(screen.getByRole("checkbox", { name: "Use colors" }));
		await user.click(screen.getByRole("button", { name: "Apply" }));

		expect(onApply).toHaveBeenCalledWith("needle", {
			showInput: true,
			showOutput: false,
			showEnvironment: true,
			showFullContent: true,
			useColors: true,
		});
	});
});
