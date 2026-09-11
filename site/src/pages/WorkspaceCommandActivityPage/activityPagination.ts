export const getClampedActivityPage = (
	page: number,
	totalPages: number,
	isPlaceholderData: boolean,
): number | undefined => {
	if (isPlaceholderData || page <= totalPages) return undefined;
	return totalPages;
};
