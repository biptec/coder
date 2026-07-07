import {
	type FC,
	Profiler,
	type ReactNode,
	useEffect,
	useRef,
	useState,
} from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { toast } from "sonner";
import type { UrlTransform } from "streamdown";
import { chatPromptsQuery, refreshChatContext } from "#/api/queries/chats";
import type * as TypesGen from "#/api/typesGenerated";
import type { AgentChatSendShortcut } from "#/api/typesGenerated";
import { cn } from "#/utils/cn";
import { useChatDraftAttachments } from "../hooks/useChatDraftAttachments";
import { chatWidthClass, useChatFullWidth } from "../hooks/useChatFullWidth";
import { useFileAttachments } from "../hooks/useFileAttachments";
import {
	useWorkspaceFileUploads,
	type WorkspaceFileUpload,
} from "../hooks/useWorkspaceFileUploads";
import {
	getChatFileURL,
	isWorkspaceFileReferencePart,
} from "../utils/chatAttachments";
import { getProviderForModelOption } from "../utils/modelOptions";
import type { ChatDetailError } from "../utils/usageLimitMessage";
import {
	AgentChatInput,
	type AttachedWorkspaceInfo,
	type ChatMessageInputRef,
	isUploadInProgress,
	type UploadState,
} from "./AgentChatInput";
import { ConversationTimeline } from "./ChatConversation/ConversationTimeline";
import { getLatestContextUsage } from "./ChatConversation/chatHelpers";
import {
	selectChatStatus,
	selectHasStreamState,
	selectIsAwaitingFirstStreamChunk,
	selectMessagesByID,
	selectOrderedMessageIDs,
	selectQueuedMessages,
	useChatSelector,
	type useChatStore,
} from "./ChatConversation/chatStore";
import { LiveStreamTail } from "./ChatConversation/LiveStreamTail";
import {
	buildSubagentMaps,
	getPendingToolCallIDs,
	parseMessagesWithMergedTools,
} from "./ChatConversation/messageParsing";
import { useOnRenderProfiler } from "./ChatConversation/useOnRenderProfiler";
import type { ModelSelectorOption } from "./ChatElements";

type ChatStoreHandle = ReturnType<typeof useChatStore>["store"];

const isChatMessage = (
	message: TypesGen.ChatMessage | undefined,
): message is TypesGen.ChatMessage => Boolean(message);

interface ChatPageTimelineProps {
	store: ChatStoreHandle;
	persistedError: ChatDetailError | undefined;
	onEditUserMessage?: (
		messageId: number,
		text: string,
		fileBlocks?: readonly TypesGen.ChatMessagePart[],
	) => void;
	editingMessageId?: number | null;
	onImplementPlan?: () => Promise<void> | void;
	onSendAskUserQuestionResponse?: (message: string) => Promise<void> | void;
	urlTransform?: UrlTransform;
	mcpServers?: readonly TypesGen.MCPServerConfig[];
}

export const ChatPageTimeline: FC<ChatPageTimelineProps> = ({
	store,
	persistedError,
	onEditUserMessage,
	editingMessageId,
	onImplementPlan,
	onSendAskUserQuestionResponse,
	urlTransform,
	mcpServers,
}) => {
	const [chatFullWidth] = useChatFullWidth();
	const messagesByID = useChatSelector(store, selectMessagesByID);
	const orderedMessageIDs = useChatSelector(store, selectOrderedMessageIDs);
	const chatStatus = useChatSelector(store, selectChatStatus);
	const hasStream = useChatSelector(store, selectHasStreamState);
	const isAwaitingFirstStreamChunk = useChatSelector(
		store,
		selectIsAwaitingFirstStreamChunk,
	);
	const isChatCompleted = !hasStream && chatStatus !== "pending";

	const messages = orderedMessageIDs
		.map((messageID) => {
			const message = messagesByID.get(messageID);
			if (!message && process.env.NODE_ENV !== "production") {
				console.warn(
					`[ChatPageContent] orderedMessageIDs contains ID ${messageID} ` +
						"not found in messagesByID. This may indicate a store/cache " +
						"desync bug.",
				);
			}
			return message;
		})
		.filter(isChatMessage);
	const pendingToolCallIDs = getPendingToolCallIDs(messages, chatStatus);
	const parsedMessages = parseMessagesWithMergedTools(messages, {
		pendingToolCallIDs,
	});
	const { titles: subagentTitles, variants: subagentVariants } =
		buildSubagentMaps(parsedMessages);
	const onRenderProfiler = useOnRenderProfiler();

	return (
		<Profiler id="AgentChat" onRender={onRenderProfiler}>
			<div
				data-testid="chat-timeline-wrapper"
				className={cn(
					"mx-auto flex w-full flex-col py-6",
					chatWidthClass(chatFullWidth),
				)}
			>
				{/* VNC sessions for completed agents may already be
					   terminated, so inline desktop previews are disabled
					   via showDesktopPreviews={false} to avoid a perpetual
					   "disconnected" state. The MonitorIcon variant still
					   renders correctly. */}
				<ConversationTimeline
					parsedMessages={parsedMessages}
					subagentTitles={subagentTitles}
					subagentVariants={subagentVariants}
					onEditUserMessage={onEditUserMessage}
					editingMessageId={editingMessageId}
					onImplementPlan={onImplementPlan}
					onSendAskUserQuestionResponse={onSendAskUserQuestionResponse}
					isChatCompleted={isChatCompleted}
					hasActiveStream={hasStream}
					isAwaitingFirstStreamChunk={isAwaitingFirstStreamChunk}
					urlTransform={urlTransform}
					mcpServers={mcpServers}
					showDesktopPreviews={false}
				/>
				<LiveStreamTail
					store={store}
					persistedError={persistedError}
					isTranscriptEmpty={parsedMessages.length === 0}
					subagentTitles={subagentTitles}
					subagentVariants={subagentVariants}
					urlTransform={urlTransform}
					mcpServers={mcpServers}
				/>
			</div>
		</Profiler>
	);
};

export type PendingAttachment = {
	fileId: string;
	mediaType: string;
};

export type PendingWorkspaceUpload = {
	path: string;
	name: string;
	size: number;
	mediaType: string;
	// The workspace whose filesystem holds the bytes, echoed back to
	// the server which rejects references from a stale binding.
	workspaceId: string;
};

export type SendChatMessageOptions = {
	message: string;
	attachments?: readonly PendingAttachment[];
	workspaceUploads?: readonly PendingWorkspaceUpload[];
};

interface ChatPageInputProps {
	// Organization that owns this chat. Used to scope file uploads.
	organizationId: string | undefined;
	store: ChatStoreHandle;
	compressionThreshold: number | undefined;
	onSend: (options: SendChatMessageOptions) => Promise<void> | void;
	sendShortcut: AgentChatSendShortcut;
	onDeleteQueuedMessage: (id: number) => Promise<void>;
	onPromoteQueuedMessage: (id: number) => Promise<void>;
	onInterrupt: () => void;
	isInputDisabled: boolean;
	isSendPending: boolean;
	isInterruptPending: boolean;
	hasModelOptions: boolean;
	selectedModel: string;
	onModelChange: (modelID: string) => void;
	modelOptions: readonly ModelSelectorOption[];
	modelSelectorPlaceholder: string;
	modelSelectorHelp?: ReactNode;
	canConfigureAgentSetup: boolean;
	providerCount?: number;
	modelCount?: number;
	unsupportedProviderNames?: readonly string[];
	aiGatewayDisabled?: boolean;
	planModeEnabled?: boolean;
	onPlanModeToggle?: (enabled: boolean) => void;
	isModelCatalogLoading?: boolean;
	// Imperative editor handle plus the one-time initial draft,
	// owned by the conversation component.
	inputRef?: React.Ref<ChatMessageInputRef>;
	initialValue?: string;
	initialEditorState?: string;
	remountKey?: number;
	onContentChange?: (
		content: string,
		serializedEditorState: string,
		hasFileReferences: boolean,
	) => void;
	isEditing: boolean;
	editingQueuedMessageID: number | null;
	onStartQueueEdit: (
		id: number,
		text: string,
		fileBlocks: readonly TypesGen.ChatMessagePart[],
	) => void;
	onCancelQueueEdit: () => void;
	isEditingHistoryMessage: boolean;
	onCancelHistoryEdit: () => void;
	// File parts from the message being edited, converted to
	// File objects and pre-populated into attachments.
	editingFileBlocks?: readonly TypesGen.ChatMessagePart[];
	// MCP server picker state.
	mcpServers?: readonly TypesGen.MCPServerConfig[];
	selectedMCPServerIds?: readonly string[];
	onMCPSelectionChange?: (ids: string[]) => void;
	onMCPAuthComplete?: (serverId: string) => void;
	// Pinned workspace-context state for the chat, surfaced by the
	// context indicator (dirty marker and pinned resources).
	chatContext?: TypesGen.ChatContext;
	workspaceOptions: readonly TypesGen.Workspace[];
	chatOrganizationId?: string;
	selectedWorkspaceId: string | null;
	onWorkspaceChange?: (workspaceId: string | null) => void;
	isWorkspaceLoading: boolean;
	workspace?: TypesGen.Workspace;
	workspaceAgent?: TypesGen.WorkspaceAgent;
	chatId?: string;
	sshCommand?: string;
	attachedWorkspace?: AttachedWorkspaceInfo;
	folder?: string;
}

export const ChatPageInput: FC<ChatPageInputProps> = ({
	organizationId,
	store,
	compressionThreshold,
	onSend,
	sendShortcut,
	onDeleteQueuedMessage,
	onPromoteQueuedMessage,
	onInterrupt,
	isInputDisabled,
	isSendPending,
	isInterruptPending,
	hasModelOptions,
	selectedModel,
	onModelChange,
	modelOptions,
	modelSelectorPlaceholder,
	modelSelectorHelp,
	canConfigureAgentSetup,
	providerCount,
	modelCount,
	unsupportedProviderNames,
	aiGatewayDisabled,
	planModeEnabled,
	onPlanModeToggle,
	isModelCatalogLoading = false,
	inputRef,
	initialValue,
	initialEditorState,
	remountKey,
	onContentChange,
	isEditing,
	editingQueuedMessageID,
	onStartQueueEdit,
	onCancelQueueEdit,
	isEditingHistoryMessage,
	onCancelHistoryEdit,
	editingFileBlocks,
	mcpServers,
	selectedMCPServerIds,
	onMCPSelectionChange,
	onMCPAuthComplete,
	chatContext,
	workspaceOptions,
	chatOrganizationId,
	selectedWorkspaceId,
	onWorkspaceChange,
	isWorkspaceLoading,
	workspace,
	workspaceAgent,
	chatId,
	sshCommand,
	attachedWorkspace,
	folder,
}) => {
	const messagesByID = useChatSelector(store, selectMessagesByID);
	const orderedMessageIDs = useChatSelector(store, selectOrderedMessageIDs);
	const hasStreamState = useChatSelector(store, selectHasStreamState);
	const chatStatus = useChatSelector(store, selectChatStatus);
	const queuedMessages = useChatSelector(store, selectQueuedMessages);

	const messages = orderedMessageIDs
		.map((messageID) => {
			const message = messagesByID.get(messageID);
			if (!message && process.env.NODE_ENV !== "production") {
				console.warn(
					`[ChatPageContent] orderedMessageIDs contains ID ${messageID} ` +
						"not found in messagesByID. This may indicate a store/cache " +
						"desync bug.",
				);
			}
			return message;
		})
		.filter(isChatMessage);
	// Source the composer's prompt-history cycle from the dedicated /prompts endpoint.
	const { data: promptsData } = useQuery(chatPromptsQuery(chatId ?? ""));
	const userPromptHistory: readonly string[] =
		promptsData?.prompts.map((prompt) => prompt.text) ?? [];

	const rawUsage = getLatestContextUsage(messages);
	const latestContextUsage =
		rawUsage || chatContext
			? {
					...(rawUsage ?? {}),
					compressionThreshold,
					context: chatContext,
				}
			: rawUsage;
	const queryClient = useQueryClient();
	const refreshContextMutation = useMutation(
		refreshChatContext(queryClient, chatId ?? ""),
	);
	const handleRefreshContext = chatId
		? () =>
				refreshContextMutation.mutate(undefined, {
					onSuccess: () => toast.success("Context refreshed."),
					onError: () => toast.error("Failed to refresh context."),
				})
		: undefined;
	const composeAttachments = useChatDraftAttachments(organizationId, chatId, {
		provider: getProviderForModelOption(modelOptions, selectedModel),
	});
	// Scope on the chat's bound workspace ID (known with the chat
	// record) rather than the async-loaded workspace object, so a
	// rebind resets immediately and query resolution never does.
	const composeWorkspaceUploads = useWorkspaceFileUploads(
		chatId,
		selectedWorkspaceId ?? undefined,
	);
	const editWorkspaceUploads = useWorkspaceFileUploads(
		chatId,
		selectedWorkspaceId ?? undefined,
	);
	// Workspace file references preserved from the message being
	// edited. They are already uploaded; editing only re-references
	// them (or drops them when the chip is removed).
	const [preservedWorkspaceUploads, setPreservedWorkspaceUploads] = useState<
		readonly WorkspaceFileUpload[]
	>([]);
	const editAttachments = useFileAttachments(organizationId, {
		provider: getProviderForModelOption(modelOptions, selectedModel),
	});
	const {
		setAttachments: setEditAttachments,
		setPreviewUrls: setEditPreviewUrls,
		setUploadStates: setEditUploadStates,
		resetAttachments: resetEditAttachments,
	} = editAttachments;
	const wasEditingRef = useRef(isEditing);
	const modeAttachments = isEditing ? editAttachments : composeAttachments;
	const {
		attachments,
		textContents,
		uploadStates,
		previewUrls,
		handleAttach,
		handleRemoveAttachment,
	} = modeAttachments;

	// Edit attachments are scoped to the chat being edited, not the compose
	// draft. Clear them when navigation changes the chat scope.
	const editScopeRef = useRef({ organizationId, chatId });

	const { reset: resetEditWorkspaceUploads } = editWorkspaceUploads;

	useEffect(() => {
		const previous = editScopeRef.current;
		const scopeChanged =
			previous.organizationId !== organizationId || previous.chatId !== chatId;
		editScopeRef.current = { organizationId, chatId };
		if (scopeChanged) {
			resetEditAttachments();
			setPreservedWorkspaceUploads([]);
		}
	}, [organizationId, chatId, resetEditAttachments]);

	// Preserved references point at files inside the bound workspace's
	// filesystem. Rebinding or clearing the workspace mid-edit makes
	// them unreadable for the agent, so drop them (regular attachments
	// are chat files and survive workspace changes). Scope tracks the
	// chat's bound workspace ID, not the async-loaded workspace
	// object, which resolves after mount without a rebind.
	const editWorkspaceScopeRef = useRef(selectedWorkspaceId);
	useEffect(() => {
		if (editWorkspaceScopeRef.current === selectedWorkspaceId) {
			return;
		}
		editWorkspaceScopeRef.current = selectedWorkspaceId;
		setPreservedWorkspaceUploads([]);
	}, [selectedWorkspaceId]);

	// Pre-populate the edit bucket from existing file blocks only
	// while explicitly editing a message. Hydration is keyed on the
	// blocks reference plus the workspace binding: a failed edit
	// submission restores the same array on rollback (same key, no
	// re-hydration, so uploads added mid-edit survive), while
	// rebinding the workspace away and back re-runs preservation so
	// references valid for the restored binding reappear. Editing a
	// different message passes a fresh array and re-hydrates.
	const hydratedEditBlocksRef = useRef<{
		blocks: readonly TypesGen.ChatMessagePart[] | null;
		workspaceId: string | null;
	} | null>(null);
	useEffect(() => {
		if (!isEditing) {
			return;
		}
		if (
			hydratedEditBlocksRef.current !== null &&
			hydratedEditBlocksRef.current.blocks === (editingFileBlocks ?? null) &&
			hydratedEditBlocksRef.current.workspaceId === selectedWorkspaceId
		) {
			return;
		}
		hydratedEditBlocksRef.current = {
			blocks: editingFileBlocks ?? null,
			workspaceId: selectedWorkspaceId,
		};
		// Workspace file references from the edited message become
		// preserved (already-uploaded) entries so they survive the
		// edit unless explicitly removed. Only references uploaded to
		// the currently bound workspace qualify: after a rebind the
		// old paths are unreadable for the agent and the server
		// rejects them. Compare against the chat's bound workspace ID
		// (available with the chat record) rather than the
		// async-loaded workspace object, whose late resolution would
		// otherwise drop references hydrated before it arrived.
		resetEditWorkspaceUploads();
		setPreservedWorkspaceUploads(
			(editingFileBlocks ?? [])
				.filter(isWorkspaceFileReferencePart)
				.filter(
					(part) =>
						selectedWorkspaceId !== null &&
						part.workspace_file_workspace_id === selectedWorkspaceId,
				)
				.map(
					(part, i): WorkspaceFileUpload => ({
						id: `preserved-${i}-${part.workspace_file_path}`,
						file: new File([], part.workspace_file_name, {
							type:
								part.workspace_file_media_type || "application/octet-stream",
						}),
						status: "uploaded",
						response: {
							path: part.workspace_file_path,
							name: part.workspace_file_name,
							size: part.workspace_file_size,
							media_type:
								part.workspace_file_media_type || "application/octet-stream",
							workspace_id: part.workspace_file_workspace_id,
						},
					}),
				),
		);
		if (!editingFileBlocks || editingFileBlocks.length === 0) {
			setEditAttachments([]);
			setEditUploadStates(new Map());
			setEditPreviewUrls(new Map());
			return;
		}
		const fileBlocks = editingFileBlocks.filter(
			(b): b is TypesGen.ChatFilePart => b.type === "file",
		);
		const files = fileBlocks.map((block, i) => {
			const mt = block.media_type ?? "application/octet-stream";
			const ext = mt === "text/plain" ? "txt" : (mt.split("/")[1] ?? "png");
			// Empty File used as a Map key only, its content is never
			// read because the existing file_id is reused at send time.
			return new File([], `attachment-${i}.${ext}`, { type: mt });
		});
		setEditAttachments(files);
		setEditPreviewUrls(
			new Map(
				files.map((f, i) => [f, getChatFileURL(fileBlocks[i].file_id ?? "")]),
			),
		);
		const newUploadStates = new Map<File, UploadState>();
		for (const [i, file] of files.entries()) {
			const block = fileBlocks[i];
			if (block.file_id) {
				newUploadStates.set(file, {
					status: "uploaded",
					fileId: block.file_id,
				});
			}
		}
		setEditUploadStates(newUploadStates);
	}, [
		isEditing,
		editingFileBlocks,
		selectedWorkspaceId,
		setEditAttachments,
		setEditPreviewUrls,
		setEditUploadStates,
		resetEditWorkspaceUploads,
	]);

	// Exiting edit mode should only clear the edit bucket. Compose draft
	// attachments must survive canceling or completing an edit.
	useEffect(() => {
		if (isEditing) {
			wasEditingRef.current = true;
			return;
		}
		if (!wasEditingRef.current) {
			return;
		}
		// History edits clear isEditing before the edit mutation
		// settles and restore it on failure. Defer cleanup until the
		// submission resolves so a failed edit keeps its uploads and
		// attachments for retry.
		if (isSendPending) {
			return;
		}
		wasEditingRef.current = false;
		hydratedEditBlocksRef.current = null;
		resetEditAttachments();
		resetEditWorkspaceUploads();
		setPreservedWorkspaceUploads([]);
	}, [
		isEditing,
		isSendPending,
		resetEditAttachments,
		resetEditWorkspaceUploads,
	]);

	const isStreaming =
		hasStreamState || chatStatus === "running" || chatStatus === "pending";

	// The workspace upload affordance requires an existing chat bound
	// to a workspace whose agent is connected; the agent writes the
	// bytes into its home directory.
	const canUploadWorkspaceFiles = Boolean(
		chatId && workspace && workspaceAgent?.status === "connected",
	);
	const modeWorkspaceUploads = isEditing
		? editWorkspaceUploads
		: composeWorkspaceUploads;
	const visibleWorkspaceUploads = isEditing
		? [...preservedWorkspaceUploads, ...editWorkspaceUploads.uploads]
		: composeWorkspaceUploads.uploads;
	const handleRemoveWorkspaceUpload = (id: string) => {
		if (
			isEditing &&
			preservedWorkspaceUploads.some((upload) => upload.id === id)
		) {
			setPreservedWorkspaceUploads((current) =>
				current.filter((upload) => upload.id !== id),
			);
			return;
		}
		modeWorkspaceUploads.remove(id);
	};

	const inputElement = (
		<AgentChatInput
			onSend={(message) => {
				void (async () => {
					const hasActiveUploads =
						attachments.some((file) =>
							isUploadInProgress(uploadStates.get(file)),
						) ||
						visibleWorkspaceUploads.some(
							(upload) => upload.status === "uploading",
						);
					if (hasActiveUploads) {
						toast.warning("Wait for file uploads to finish before sending.");
						return;
					}
					// Collect uploaded attachment metadata for the optimistic
					// transcript builder while keeping the server payload
					// shape unchanged downstream.
					const pendingAttachments: PendingAttachment[] = [];
					let skippedErrors = 0;
					for (const file of attachments) {
						const state = uploadStates.get(file);
						if (state?.status === "error") {
							skippedErrors++;
							continue;
						}
						if (state?.status === "uploaded" && state.fileId) {
							pendingAttachments.push({
								fileId: state.fileId,
								mediaType: file.type || "application/octet-stream",
							});
						}
					}
					const pendingWorkspaceUploads: PendingWorkspaceUpload[] = [];
					let skippedWorkspaceErrors = 0;
					for (const upload of visibleWorkspaceUploads) {
						if (upload.status === "error") {
							skippedWorkspaceErrors++;
							continue;
						}
						if (upload.status === "uploaded" && upload.response) {
							pendingWorkspaceUploads.push({
								path: upload.response.path,
								name: upload.response.name,
								size: upload.response.size,
								mediaType: upload.response.media_type,
								workspaceId: upload.response.workspace_id,
							});
						}
					}
					if (skippedErrors > 0) {
						toast.warning(
							`${skippedErrors} attachment${skippedErrors > 1 ? "s" : ""} could not be sent (upload failed)`,
						);
					}
					if (skippedWorkspaceErrors > 0) {
						toast.warning(
							`${skippedWorkspaceErrors} workspace file${skippedWorkspaceErrors > 1 ? "s" : ""} could not be sent (upload failed)`,
						);
					}
					const attachmentsArg =
						pendingAttachments.length > 0 ? pendingAttachments : undefined;
					const workspaceUploadsArg =
						pendingWorkspaceUploads.length > 0
							? pendingWorkspaceUploads
							: undefined;
					try {
						await onSend({
							message,
							attachments: attachmentsArg,
							workspaceUploads: workspaceUploadsArg,
						});
					} catch {
						// Attachments preserved for retry on failure.
						return;
					}
					if (isEditing) {
						editAttachments.resetAttachments();
						resetEditWorkspaceUploads();
						setPreservedWorkspaceUploads([]);
					} else {
						composeAttachments.resetAttachments();
						composeWorkspaceUploads.reset();
					}
				})();
			}}
			sendShortcut={sendShortcut}
			attachments={attachments}
			onAttach={handleAttach}
			onRemoveAttachment={handleRemoveAttachment}
			uploadStates={uploadStates}
			previewUrls={previewUrls}
			textContents={textContents}
			workspaceUploads={{
				uploads: visibleWorkspaceUploads,
				onAttach: canUploadWorkspaceFiles
					? modeWorkspaceUploads.attach
					: undefined,
				onRemove: handleRemoveWorkspaceUpload,
			}}
			inputRef={inputRef}
			initialValue={initialValue}
			initialEditorState={initialEditorState}
			remountKey={remountKey}
			onContentChange={onContentChange}
			queuedMessages={queuedMessages}
			onDeleteQueuedMessage={onDeleteQueuedMessage}
			onPromoteQueuedMessage={onPromoteQueuedMessage}
			editingQueuedMessageID={editingQueuedMessageID}
			onStartQueueEdit={onStartQueueEdit}
			onCancelQueueEdit={onCancelQueueEdit}
			isEditingHistoryMessage={isEditingHistoryMessage}
			onCancelHistoryEdit={onCancelHistoryEdit}
			userPromptHistory={userPromptHistory}
			isDisabled={isInputDisabled}
			isLoading={isSendPending}
			isStreaming={isStreaming}
			onInterrupt={onInterrupt}
			isInterruptPending={isInterruptPending}
			contextUsage={latestContextUsage}
			onRefreshContext={handleRefreshContext}
			isRefreshingContext={refreshContextMutation.isPending}
			hasModelOptions={hasModelOptions}
			selectedModel={selectedModel}
			onModelChange={onModelChange}
			modelOptions={modelOptions}
			modelSelectorPlaceholder={modelSelectorPlaceholder}
			planModeEnabled={planModeEnabled}
			onPlanModeToggle={onPlanModeToggle}
			isModelCatalogLoading={isModelCatalogLoading}
			workspaceOptions={workspaceOptions}
			chatOrganizationId={chatOrganizationId}
			selectedWorkspaceId={selectedWorkspaceId}
			onWorkspaceChange={onWorkspaceChange}
			isWorkspaceLoading={isWorkspaceLoading}
			mcpServers={mcpServers}
			selectedMCPServerIds={selectedMCPServerIds}
			onMCPSelectionChange={onMCPSelectionChange}
			onMCPAuthComplete={onMCPAuthComplete}
			workspace={workspace}
			workspaceAgent={workspaceAgent}
			chatId={chatId}
			sshCommand={sshCommand}
			attachedWorkspace={attachedWorkspace}
			folder={folder}
			canConfigureAgentSetup={canConfigureAgentSetup}
			providerCount={providerCount}
			modelCount={modelCount}
			unsupportedProviderNames={unsupportedProviderNames}
			aiGatewayDisabled={aiGatewayDisabled}
		/>
	);

	if (!modelSelectorHelp) {
		return inputElement;
	}

	return (
		<div>
			{inputElement}
			<div className="px-3 pt-1 text-2xs text-content-secondary">
				{modelSelectorHelp}
			</div>
		</div>
	);
};
