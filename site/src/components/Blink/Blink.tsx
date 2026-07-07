import { type FC, useEffect, useState } from "react";
import { BlinkButton } from "./BlinkButton";
import { BlinkPanel } from "./BlinkPanel";
import { useBlinkContext } from "./BlinkProvider";

export const Blink: FC = () => {
	const {
		enabled,
		open,
		toggle,
		close,
		messages,
		sendMessage,
		startNewChat,
		isThinking,
	} = useBlinkContext();

	// Track how many messages the user has seen so the unread
	// indicator only pulses for genuinely new messages.
	const [seenCount, setSeenCount] = useState(0);

	useEffect(() => {
		if (open) {
			setSeenCount(messages.length);
		}
	}, [open, messages.length]);

	if (!enabled) {
		return null;
	}

	return (
		<>
			<BlinkPanel
				open={open}
				onClose={close}
				onNewChat={startNewChat}
				messages={messages}
				onSendMessage={sendMessage}
				isThinking={isThinking}
			/>
			<BlinkButton
				open={open}
				onToggle={toggle}
				isThinking={isThinking}
				hasUnread={messages.length > seenCount && !open}
			/>
		</>
	);
};
