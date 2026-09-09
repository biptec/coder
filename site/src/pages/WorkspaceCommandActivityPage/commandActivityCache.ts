import type {
	WorkspaceCommandActivity,
	WorkspaceCommandActivityRequest,
	WorkspaceCommandActivityResponse,
} from "#/api/typesGenerated";

/**
 * Applies a single durable command delta to a cached REST page.
 *
 * Returning undefined means the query shape cannot be updated safely from one
 * row alone (for example non-default sorting or a status transition that moves
 * a previously hidden activity into the result).
 * The caller should schedule one debounced REST resync in that case.
 */
export const updateCommandActivityCache = (
	data: WorkspaceCommandActivityResponse,
	command: WorkspaceCommandActivity,
	request: WorkspaceCommandActivityRequest,
): WorkspaceCommandActivityResponse | undefined => {
	const dataWithTool = withAvailableTool(data, command.tool);
	const page = request.page ?? 1;
	const pageSize = request.page_size ?? data.page_size;
	const sortBy = request.sort_by ?? "started";
	const sortDirection = request.sort_direction ?? "desc";
	const fastPath =
		page === 1 && sortBy === "started" && sortDirection === "desc";
	const existingIndex = dataWithTool.activity.findIndex(
		(item) => item.id === command.id,
	);
	const matches = commandMatchesRequest(command, request);

	if (existingIndex >= 0) {
		if (!matches || !fastPath) return undefined;
		const activity = [...dataWithTool.activity];
		activity[existingIndex] = command;
		return { ...dataWithTool, activity };
	}

	if (!matches) return dataWithTool;
	// A START event is the only event that adds a brand-new row to an ordinary
	// started-desc first page. A FINISH event for an unseen row either belongs
	// on another page or has just entered a status/duration filter; resync it.
	if (!fastPath || command.status !== "running") return undefined;

	const totalCount = dataWithTool.total_count + 1;
	const deletableCount = dataWithTool.deletable_count + 1;
	return {
		...dataWithTool,
		activity: [command, ...dataWithTool.activity].slice(0, pageSize),
		total_count: totalCount,
		deletable_count: deletableCount,
		total_pages: Math.ceil(totalCount / pageSize),
	};
};

const withAvailableTool = (
	data: WorkspaceCommandActivityResponse,
	tool: string | undefined,
): WorkspaceCommandActivityResponse => {
	if (!tool || data.available_tools.includes(tool)) return data;
	return {
		...data,
		available_tools: [...data.available_tools, tool].sort(),
	};
};

export const commandMatchesRequest = (
	command: WorkspaceCommandActivity,
	request: WorkspaceCommandActivityRequest,
): boolean => {
	if (
		request.id &&
		!command.id.toLocaleLowerCase().includes(request.id.toLocaleLowerCase())
	) {
		return false;
	}
	if (request.statuses?.length && !request.statuses.includes(command.status)) {
		return false;
	}
	if (
		request.tools?.length &&
		(!command.tool || !request.tools.includes(command.tool))
	) {
		return false;
	}
	if (request.sources?.length && !request.sources.includes(command.source)) {
		return false;
	}
	if (request.search) {
		const needle = request.search.toLocaleLowerCase();
		const haystack =
			`${command.command} ${command.argv?.join(" ") ?? ""}`.toLocaleLowerCase();
		if (!haystack.includes(needle)) return false;
	}

	const started = new Date(command.started_at).getTime();
	if (
		request.started_after &&
		started < new Date(request.started_after).getTime()
	) {
		return false;
	}
	if (
		request.started_before &&
		started > new Date(request.started_before).getTime()
	) {
		return false;
	}
	const finished = command.finished_at
		? new Date(command.finished_at).getTime()
		: Date.now();
	const duration = Math.max(0, finished - started);
	if (
		request.duration_min_ms !== undefined &&
		duration < request.duration_min_ms
	) {
		return false;
	}
	if (
		request.duration_max_ms !== undefined &&
		duration > request.duration_max_ms
	) {
		return false;
	}
	if (
		request.exit_code !== undefined &&
		command.exit_code !== request.exit_code
	) {
		return false;
	}
	return true;
};
