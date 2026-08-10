import { useEffect, useRef, useState } from "react";
import { watchChatGit } from "#/api/api";
import type {
	WorkspaceAgentGitClientMessage,
	WorkspaceAgentGitDiffProgress,
	WorkspaceAgentGitServerMessage,
	WorkspaceAgentRepoChanges,
	WorkspaceAgentStatus,
} from "#/api/typesGenerated";
import { createReconnectingWebSocket } from "#/utils/reconnectingWebSocket";

// Compile-time guard: ensures the bailout comparison in setRepositories
// covers every data field. If WorkspaceAgentRepoChanges gains a new
// field, this will error until the comparison is updated.
type _ComparedRepoFields = Omit<
	WorkspaceAgentRepoChanges,
	"repo_root" | "removed"
>;
const _repoFieldGuard: Record<keyof _ComparedRepoFields, true> = {
	branch: true,
	remote_origin: true,
	unified_diff: true,
	diff_truncated: true,
};

interface UseGitWatcherOptions {
	chatId: string | undefined;
	agentStatus: WorkspaceAgentStatus | undefined;
}

interface UseGitWatcherResult {
	/** Current repo state, keyed by repo root path. */
	repositories: ReadonlyMap<string, WorkspaceAgentRepoChanges>;
	/**
	 * Repo roots seen with a non-empty unified_diff during this chat.
	 * Survives reconnects; evicted on `removed: true`, cleared on
	 * chatId change. Consumers should intersect with `repositories`.
	 */
	everDirty: ReadonlySet<string>;
	/** Progressive diff state, keyed by repo root path. */
	progress: ReadonlyMap<string, WorkspaceAgentGitDiffProgress>;
	/** Whether the WebSocket is currently connected. */
	isConnected: boolean;
	/** Whether the watcher has received repository state for this chat. */
	hasReceivedChanges: boolean;
	/** Send a refresh request. Returns true if sent, false if disconnected. */
	refresh: () => boolean;
}

export function useGitWatcher({
	chatId,
	agentStatus,
}: UseGitWatcherOptions): UseGitWatcherResult {
	const [repositories, setRepositories] = useState<
		ReadonlyMap<string, WorkspaceAgentRepoChanges>
	>(new Map());
	const [everDirty, setEverDirty] = useState<ReadonlySet<string>>(
		() => new Set(),
	);
	const [progress, setProgress] = useState<
		ReadonlyMap<string, WorkspaceAgentGitDiffProgress>
	>(new Map());
	const [isConnected, setIsConnected] = useState(false);
	const [hasReceivedChanges, setHasReceivedChanges] = useState(false);

	const socketRef = useRef<WebSocket | null>(null);
	// Chat-scoped state (everDirty) resets on chatId change but
	// must survive agentStatus flaps on the same chat.
	// https://react.dev/reference/react/useState#storing-information-from-previous-renders
	const [lastChatId, setLastChatId] = useState<string | undefined>(chatId);
	if (lastChatId !== chatId) {
		setLastChatId(chatId);
		setEverDirty((prev) => (prev.size === 0 ? prev : new Set()));
	}

	const sendMessage = (msg: WorkspaceAgentGitClientMessage): boolean => {
		const socket = socketRef.current;
		if (socket && socket.readyState === WebSocket.OPEN) {
			socket.send(JSON.stringify(msg));
			return true;
		}
		return false;
	};

	const refresh = (): boolean => {
		return sendMessage({ type: "refresh" });
	};

	useEffect(() => {
		if (!chatId || agentStatus !== "connected") {
			return;
		}

		const activeChatId = chatId;

		const dispose = createReconnectingWebSocket({
			connect() {
				const socket = watchChatGit(activeChatId);
				socketRef.current = socket;

				socket.addEventListener("message", (event) => {
					// Ignore messages from superseded connections.
					if (socketRef.current !== socket) {
						return;
					}
					let data: WorkspaceAgentGitServerMessage;
					try {
						data = JSON.parse(
							String((event as MessageEvent).data),
						) as WorkspaceAgentGitServerMessage;
					} catch {
						// Ignore unparsable messages.
						return;
					}

					if (data.type === "changes") {
						setHasReceivedChanges(true);
						if (data.repositories) {
							setRepositories((prev) => {
								let changed = false;
								const next = new Map(prev);
								for (const repo of data.repositories!) {
									if (repo.removed) {
										if (next.has(repo.repo_root)) {
											next.delete(repo.repo_root);
											changed = true;
										}
									} else {
										const existing = next.get(repo.repo_root);
										// Progressive clients may already hold more diff data than the
										// capped final compatibility snapshot. Never replace that full
										// stream with the shorter legacy payload.
										const resolvedRepo =
											repo.diff_truncated &&
											existing?.unified_diff &&
											existing.unified_diff.length >
												(repo.unified_diff?.length ?? 0)
												? { ...repo, unified_diff: existing.unified_diff }
												: repo;
										if (
											!existing ||
											existing.branch !== resolvedRepo.branch ||
											existing.remote_origin !== resolvedRepo.remote_origin ||
											existing.unified_diff !== resolvedRepo.unified_diff ||
											existing.diff_truncated !== resolvedRepo.diff_truncated
										) {
											next.set(repo.repo_root, resolvedRepo);
											changed = true;
										}
									}
								}
								return changed ? next : prev;
							});
							setEverDirty((prev) => {
								let changed = false;
								const next = new Set(prev);
								for (const repo of data.repositories!) {
									if (repo.removed) {
										if (next.delete(repo.repo_root)) {
											changed = true;
										}
									} else if (repo.unified_diff && !next.has(repo.repo_root)) {
										next.add(repo.repo_root);
										changed = true;
									}
								}
								return changed ? next : prev;
							});
							setProgress((prev) => {
								let changed = false;
								const next = new Map(prev);
								for (const repo of data.repositories!) {
									if (repo.removed && next.delete(repo.repo_root)) {
										changed = true;
									}
								}
								return changed ? next : prev;
							});
						}
					} else if (data.type === "progress" && data.progress) {
						const update = data.progress;
						setHasReceivedChanges(true);
						setRepositories((prev) => {
							const next = new Map(prev);
							const existing = next.get(update.repo_root);
							const currentDiff = update.reset
								? ""
								: (existing?.unified_diff ?? "");
							const unifiedDiff =
								currentDiff + (update.unified_diff_chunk ?? "");
							next.set(update.repo_root, {
								...existing,
								repo_root: update.repo_root,
								branch: update.branch ?? existing?.branch ?? "",
								remote_origin: update.remote_origin ?? existing?.remote_origin,
								unified_diff: unifiedDiff || undefined,
							});
							return next;
						});
						if (update.unified_diff_chunk) {
							setEverDirty((prev) => {
								if (prev.has(update.repo_root)) {
									return prev;
								}
								const next = new Set(prev);
								next.add(update.repo_root);
								return next;
							});
						}
						setProgress((prev) => {
							const next = new Map(prev);
							if (update.complete) {
								next.delete(update.repo_root);
							} else {
								next.set(update.repo_root, update);
							}
							return next;
						});
					} else if (data.type === "error") {
						console.warn("[useGitWatcher] server error:", data.message);
					}
				});

				return socket;
			},

			onOpen() {
				setIsConnected(true);
			},

			onDisconnect() {
				setIsConnected(false);
				setHasReceivedChanges(false);
				setProgress(new Map());
				socketRef.current = null;
			},

			// 30s cap instead of the utility default 10s. The git
			// endpoint may be slow to respond after a workspace wakes.
			maxMs: 30_000,
		});

		return () => {
			// Reset connection-scoped state only. `everDirty` is
			// chat-scoped and persists across reconnects.
			dispose();
			setIsConnected(false);
			setHasReceivedChanges(false);
			setRepositories(new Map());
			setProgress(new Map());
			socketRef.current = null;
		};
	}, [chatId, agentStatus]);

	return {
		repositories,
		everDirty,
		progress,
		isConnected,
		hasReceivedChanges,
		refresh,
	};
}
