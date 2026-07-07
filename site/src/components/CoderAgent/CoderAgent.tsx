import { type FC, useEffect, useState } from "react";
import {
	selectOrderedMessageIDs,
	useChatSelector,
} from "#/pages/AgentsPage/components/ChatConversation/chatStore";
import { CoderAgentButton } from "./CoderAgentButton";
import { CoderAgentPanel } from "./CoderAgentPanel";
import { useCoderAgentContext } from "./CoderAgentProvider";

export const CoderAgent: FC = () => {
	const {
		enabled,
		open,
		toggle,
		close,
		chatId,
		chatTitle,
		store,
		persistedError,
		sendMessage,
		startNewChat,
		isThinking,
		isSendPending,
		modelOptions,
		selectedModel,
		setSelectedModel,
		hasModelOptions,
		modelSelectorPlaceholder,
		isModelCatalogLoading,
	} = useCoderAgentContext();

	const orderedMessageIDs = useChatSelector(store, selectOrderedMessageIDs);
	const messageCount = orderedMessageIDs.length;

	// Track how many messages the user has seen so the unread
	// indicator only pulses for genuinely new messages.
	const [seenCount, setSeenCount] = useState(0);

	useEffect(() => {
		if (open) {
			setSeenCount(messageCount);
		}
	}, [open, messageCount]);

	if (!enabled) {
		return null;
	}

	return (
		<>
			<CoderAgentPanel
				open={open}
				onClose={close}
				onNewChat={startNewChat}
				onSendMessage={sendMessage}
				isThinking={isThinking}
				isSendPending={isSendPending}
				chatId={chatId}
				chatTitle={chatTitle}
				store={store}
				persistedError={persistedError}
				modelOptions={modelOptions}
				selectedModel={selectedModel}
				onModelChange={setSelectedModel}
				hasModelOptions={hasModelOptions}
				modelSelectorPlaceholder={modelSelectorPlaceholder}
				isModelCatalogLoading={isModelCatalogLoading}
			/>
			<CoderAgentButton
				open={open}
				onToggle={toggle}
				isThinking={isThinking}
				hasUnread={messageCount > seenCount && !open}
			/>
		</>
	);
};
