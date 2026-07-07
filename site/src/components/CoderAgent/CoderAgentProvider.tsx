import {
	createContext,
	type FC,
	type PropsWithChildren,
	useCallback,
	useContext,
	useEffect,
	useState,
} from "react";
import {
	useInfiniteQuery,
	useMutation,
	useQuery,
	useQueryClient,
} from "react-query";
import {
	chat,
	chatMessagesForInfiniteScroll,
	chatModelConfigs,
	chatModels,
	createChat,
	createChatMessage,
	userChatProviderConfigs,
} from "#/api/queries/chats";
import type * as TypesGen from "#/api/typesGenerated";
import { useAuthenticated } from "#/hooks/useAuthenticated";
import {
	type ChatStore,
	selectChatStatus,
	useChatSelector,
} from "#/pages/AgentsPage/components/ChatConversation/chatStore";
import { useChatStore } from "#/pages/AgentsPage/components/ChatConversation/useChatStore";
import type { ModelSelectorOption } from "#/pages/AgentsPage/components/ChatElements";
import {
	getModelSelectorPlaceholder,
	resolveModelOptionId,
	resolveModelSelector,
} from "#/pages/AgentsPage/utils/modelOptions";
import type { ChatDetailError } from "#/pages/AgentsPage/utils/usageLimitMessage";

interface CoderAgentContextValue {
	enabled: boolean;
	open: boolean;
	toggle: () => void;
	close: () => void;
	chatId: string | null;
	chatTitle: string | undefined;
	store: ChatStore;
	persistedError: ChatDetailError | undefined;
	sendMessage: (text: string) => void;
	startNewChat: () => void;
	isThinking: boolean;
	// True while a create-chat or send-message request is in flight.
	isSendPending: boolean;
	// Model selector state, mirroring the agents chat page wiring.
	modelOptions: readonly ModelSelectorOption[];
	selectedModel: string;
	setSelectedModel: (id: string) => void;
	hasModelOptions: boolean;
	modelSelectorPlaceholder: string;
	isModelCatalogLoading: boolean;
}

const CoderAgentContext = createContext<CoderAgentContextValue | null>(null);

const CHAT_ID_STORAGE_KEY = "coder_agent_chat_id";

// Same key the agents chat page uses, so the panel and the full
// page share the user's last model choice.
const LAST_MODEL_CONFIG_ID_STORAGE_KEY = "agents.last-model-config-id";

// Key used to store an error from a failed chat creation, before any
// chat ID exists.
const PENDING_CHAT_ERROR_KEY = "pending";

function readLocalStorage(key: string, fallback: string): string {
	try {
		return localStorage.getItem(key) ?? fallback;
	} catch {
		return fallback;
	}
}

function writeLocalStorage(key: string, value: string | null): void {
	try {
		if (value === null) {
			localStorage.removeItem(key);
		} else {
			localStorage.setItem(key, value);
		}
	} catch {
		// Storage may be unavailable in some contexts.
	}
}

export const CoderAgentProvider: FC<
	PropsWithChildren<{ forceEnabled?: boolean }>
> = ({ children, forceEnabled }) => {
	const [enabled] = useState(
		() =>
			forceEnabled ||
			readLocalStorage("coder_agent_enabled", "false") === "true",
	);
	const [open, setOpen] = useState(false);
	const [chatId, setChatIdState] = useState<string | null>(
		() => readLocalStorage(CHAT_ID_STORAGE_KEY, "") || null,
	);

	const queryClient = useQueryClient();
	const { user } = useAuthenticated();
	const organizationId = user.organization_ids[0];

	const setChatId = useCallback((id: string | null) => {
		setChatIdState(id);
		writeLocalStorage(CHAT_ID_STORAGE_KEY, id);
	}, []);

	// Error reasons keyed by chat ID, matching the callback contract
	// that useChatStore expects.
	const [errorReasons, setErrorReasons] = useState<
		Record<string, ChatDetailError>
	>({});
	const setChatErrorReason = useCallback(
		(chatID: string, reason: ChatDetailError) => {
			setErrorReasons((prev) => ({ ...prev, [chatID]: reason }));
		},
		[],
	);
	const clearChatErrorReason = useCallback((chatID: string) => {
		setErrorReasons((prev) => {
			if (!(chatID in prev)) {
				return prev;
			}
			const next = { ...prev };
			delete next[chatID];
			return next;
		});
	}, []);
	const persistedError = errorReasons[chatId ?? PENDING_CHAT_ERROR_KEY];

	const chatQuery = useQuery({
		...chat(chatId ?? ""),
		enabled: Boolean(chatId),
		retry: false,
	});
	const chatMessagesQuery = useInfiniteQuery({
		...chatMessagesForInfiniteScroll(chatId ?? ""),
		enabled: Boolean(chatId),
	});

	// The stored chat may have been deleted since the last visit.
	// Drop the stale ID so the next send creates a fresh chat.
	const chatLoadFailed = chatQuery.isError || chatMessagesQuery.isError;
	useEffect(() => {
		if (chatId && chatLoadFailed) {
			setChatId(null);
		}
	}, [chatId, chatLoadFailed, setChatId]);

	// Flatten the infinite pages into a single chronological list,
	// deduplicated by ID. Mirrors the wiring in AgentChatPage.
	const chatMessagesList: TypesGen.ChatMessage[] | undefined = (() => {
		const pages = chatMessagesQuery.data?.pages;
		if (!pages) {
			return undefined;
		}
		const all = pages.flatMap((p) => p.messages);
		const byID = new Map(all.map((m) => [m.id, m]));
		const deduped = Array.from(byID.values());
		deduped.sort((a, b) => a.id - b.id);
		return deduped;
	})();

	// Queued messages are only in the first page (most recent).
	const chatQueuedMessages = chatMessagesQuery.data?.pages[0]?.queued_messages;

	// Synthetic ChatMessagesResponse for backward compat with
	// useChatStore, matching the shape built in AgentChatPage.
	const chatMessagesData: TypesGen.ChatMessagesResponse | undefined =
		chatMessagesList
			? {
					messages: chatMessagesList,
					queued_messages: chatQueuedMessages ?? [],
					has_more: chatMessagesQuery.data?.pages.at(-1)?.has_more ?? false,
				}
			: undefined;

	const { store } = useChatStore({
		chatID: chatId ?? undefined,
		chatMessages: chatMessagesList,
		chatRecord: chatQuery.data,
		chatMessagesData,
		chatQueuedMessages,
		setChatErrorReason,
		clearChatErrorReason,
	});

	const { isPending: isCreatePending, mutateAsync: createChatAsync } =
		useMutation(createChat(queryClient));
	const { isPending: isSendPending, mutateAsync: createMessageAsync } =
		useMutation(createChatMessage(queryClient, chatId ?? ""));

	// Model selector, wired the same way as the agents chat page.
	const chatModelsQuery = useQuery(chatModels());
	const chatModelConfigsQuery = useQuery(chatModelConfigs());
	const userProviderConfigsQuery = useQuery(userChatProviderConfigs());
	const {
		options: modelOptions,
		isModelCatalogLoading,
		modelCatalog,
		hasConfiguredModels,
	} = resolveModelSelector(
		chatModelConfigsQuery,
		chatModelsQuery,
		userProviderConfigsQuery,
	);
	const [selectedModel, setSelectedModel] = useState(() =>
		readLocalStorage(LAST_MODEL_CONFIG_ID_STORAGE_KEY, ""),
	);
	// Validate the user's choice against current options, falling back
	// to the chat's last model or the first available option.
	const effectiveSelectedModel = (() => {
		const resolvedSelectedModel = resolveModelOptionId(
			selectedModel,
			modelOptions,
		);
		if (resolvedSelectedModel) {
			return resolvedSelectedModel;
		}
		const resolvedChatModel = resolveModelOptionId(
			chatQuery.data?.last_model_config_id,
			modelOptions,
		);
		if (resolvedChatModel) {
			return resolvedChatModel;
		}
		return modelOptions[0]?.id ?? "";
	})();
	const hasModelOptions = modelOptions.length > 0;
	const modelSelectorPlaceholder = getModelSelectorPlaceholder(
		modelOptions,
		isModelCatalogLoading,
		hasConfiguredModels,
		modelCatalog,
	);

	// The store's status is hydrated from REST and kept fresh by the
	// WebSocket, so it is the authoritative source for the thinking
	// indicator.
	const chatStatus = useChatSelector(store, selectChatStatus);
	const isThinking =
		isCreatePending ||
		isSendPending ||
		chatStatus === "running" ||
		chatStatus === "pending";

	const toggle = useCallback(() => {
		setOpen((prev) => !prev);
	}, []);

	const close = useCallback(() => {
		setOpen(false);
	}, []);

	const sendMessage = useCallback(
		(text: string) => {
			const content: TypesGen.ChatInputPart[] = [{ type: "text", text }];
			const modelConfigId = effectiveSelectedModel || undefined;
			void (async () => {
				try {
					if (chatId) {
						await createMessageAsync({
							content,
							model_config_id: modelConfigId,
						});
					} else {
						const created = await createChatAsync({
							organization_id: organizationId,
							content,
							model_config_id: modelConfigId,
							labels: { "coder-agent": "true" },
							client_type: "ui",
						});
						clearChatErrorReason(PENDING_CHAT_ERROR_KEY);
						setChatId(created.id);
					}
					if (modelConfigId) {
						writeLocalStorage(LAST_MODEL_CONFIG_ID_STORAGE_KEY, modelConfigId);
					}
				} catch (error) {
					const target = chatId ?? PENDING_CHAT_ERROR_KEY;
					setChatErrorReason(target, {
						kind: "generic",
						message:
							error instanceof Error
								? error.message
								: "Failed to send message.",
					});
				}
			})();
		},
		[
			chatId,
			clearChatErrorReason,
			createChatAsync,
			createMessageAsync,
			effectiveSelectedModel,
			organizationId,
			setChatErrorReason,
			setChatId,
		],
	);

	const startNewChat = useCallback(() => {
		clearChatErrorReason(PENDING_CHAT_ERROR_KEY);
		setChatId(null);
	}, [clearChatErrorReason, setChatId]);

	return (
		<CoderAgentContext.Provider
			value={{
				enabled,
				open,
				toggle,
				close,
				chatId,
				chatTitle: chatQuery.data?.title,
				store,
				persistedError,
				sendMessage,
				startNewChat,
				isThinking,
				isSendPending: isCreatePending || isSendPending,
				modelOptions,
				selectedModel: effectiveSelectedModel,
				setSelectedModel,
				hasModelOptions,
				modelSelectorPlaceholder,
				isModelCatalogLoading,
			}}
		>
			{children}
		</CoderAgentContext.Provider>
	);
};

export function useCoderAgentContext(): CoderAgentContextValue {
	const ctx = useContext(CoderAgentContext);
	if (!ctx) {
		throw new Error(
			"useCoderAgentContext must be used within a CoderAgentProvider",
		);
	}
	return ctx;
}
