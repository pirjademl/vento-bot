package utils

func TruncateToTokenBudget(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "\n... [truncated]"
}
