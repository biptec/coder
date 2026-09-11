import type {
	WorkspaceCommandActivity,
	WorkspaceCommandActivityRequest,
	WorkspaceCommandActivityResponse,
	WorkspaceMCPRequestActivity,
} from "#/api/typesGenerated";
import { activityHistoricalRange } from "./activityDisplay";

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
		const previous = dataWithTool.activity[existingIndex];
		// A running→finished transition can move a pinned row off page one and
		// requires pulling the next completed row from the server. Resync instead
		// of pretending the current page has enough information to place it.
		if (previous.status !== command.status) return undefined;
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

export const updateMCPRequestActivityCache = (
	data: WorkspaceCommandActivityResponse,
	requestActivity: WorkspaceMCPRequestActivity,
	request: WorkspaceCommandActivityRequest,
): WorkspaceCommandActivityResponse => {
	if (!request.include_idle) return data;

	const requests = [...(data.mcp_requests ?? [])];
	const existingIndex = requests.findIndex(
		(item) => item.id === requestActivity.id,
	);
	if (existingIndex >= 0) requests[existingIndex] = requestActivity;
	else requests.push(requestActivity);
	requests.sort(
		(a, b) =>
			new Date(a.started_at).getTime() - new Date(b.started_at).getTime() ||
			a.id.localeCompare(b.id),
	);

	// Keep exactly the spans needed by the cached page instead of trimming to an
	// arbitrary pageSize multiplier. Arbitrary trimming makes historical Idle
	// disappear on every live MCP event and then reappear after REST resync.
	const active = requests.filter((item) => !item.finished_at);
	const completed = requests.filter((item) => item.finished_at);
	const latestFinished = completed.reduce<
		WorkspaceMCPRequestActivity | undefined
	>((latest, item) => {
		if (!latest) return item;
		return new Date(item.finished_at ?? 0).getTime() >
			new Date(latest.finished_at ?? 0).getTime()
			? item
			: latest;
	}, undefined);

	const retained = new Map<string, WorkspaceMCPRequestActivity>();
	for (const item of active) retained.set(item.id, item);
	if (latestFinished) retained.set(latestFinished.id, latestFinished);

	if ((request.sort_by ?? "started") === "started") {
		const range = activityHistoricalRange(data.activity);
		if (range) {
			let previous: WorkspaceMCPRequestActivity | undefined;
			let previousFinished = Number.NEGATIVE_INFINITY;
			for (const item of completed) {
				const started = new Date(item.started_at).getTime();
				const finished = new Date(item.finished_at ?? 0).getTime();
				if (started <= range.rangeEnd && finished >= range.rangeStart) {
					retained.set(item.id, item);
					continue;
				}
				if (finished < range.rangeStart && finished > previousFinished) {
					previous = item;
					previousFinished = finished;
				}
			}
			if (previous) retained.set(previous.id, previous);
		}
	}

	const bounded = [...retained.values()].sort(
		(a, b) =>
			new Date(a.started_at).getTime() - new Date(b.started_at).getTime() ||
			a.id.localeCompare(b.id),
	);
	return { ...data, mcp_requests: bounded };
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
		const environment = Object.entries(command.environment ?? {})
			.map(([key, value]) => `${key}=${value}`)
			.join(" ");
		const haystack =
			`${command.command} ${command.argv?.join(" ") ?? ""} ${environment} ${command.output ?? ""}`.toLocaleLowerCase();
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
