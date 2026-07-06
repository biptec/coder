import { useEffect, useRef } from "react";
import { useQuery } from "react-query";
import { chatDebugRuns } from "#/api/queries/chats";

/**
 * Backs Debug tab visibility when full debug logging is off (errors are
 * captured by default). Fetches once on mount, then refetches exactly once
 * when a turn transitions from in-flight to a terminal status.
 *
 * The backend always commits an errors-only capture run before it surfaces
 * a terminal chat status, so a single refetch on that edge is sufficient;
 * no polling loop is needed. See chatdebug.captureDeferredError and its
 * callers in coderd/x/chatd/chatdebug/recorder.go.
 */
export const useDebugRunsExistence = (
	chatId: string | undefined,
	chatTurnInFlight: boolean,
) => {
	const query = useQuery({
		...chatDebugRuns(chatId ?? ""),
		enabled: Boolean(chatId),
		refetchInterval: false,
	});

	const hasDebugRuns = (query.data?.length ?? 0) > 0;
	const wasInFlightRef = useRef(chatTurnInFlight);

	useEffect(() => {
		const turnJustFinished = wasInFlightRef.current && !chatTurnInFlight;
		wasInFlightRef.current = chatTurnInFlight;
		if (turnJustFinished && !hasDebugRuns) {
			void query.refetch();
		}
	}, [chatTurnInFlight, hasDebugRuns, query.refetch]);

	return { hasDebugRuns };
};
