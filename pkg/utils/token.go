package utils

// EstimateTokens provides a rough token count estimate.
// Uses the heuristic of ~4 characters per token for English text,
// ~2 characters per token for CJK text (blended: ~3.5 chars/token).
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}

	cjkCount := 0
	totalRunes := 0
	for _, r := range text {
		totalRunes++
		if isCJK(r) {
			cjkCount++
		}
	}

	if totalRunes == 0 {
		return 0
	}

	cjkRatio := float64(cjkCount) / float64(totalRunes)
	charsPerToken := 4.0 - (cjkRatio * 2.0)
	if charsPerToken < 1.5 {
		charsPerToken = 1.5
	}

	return int(float64(len(text)) / charsPerToken)
}

// EstimateMessagesTokens estimates the total tokens in a slice of message contents.
func EstimateMessagesTokens(contents []string) int {
	total := 0
	for _, c := range contents {
		total += EstimateTokens(c)
	}
	return total
}

// TruncateToTokens truncates text to approximately maxTokens.
func TruncateToTokens(text string, maxTokens int) string {
	if EstimateTokens(text) <= maxTokens {
		return text
	}

	approxChars := maxTokens * 3
	if approxChars > len(text) {
		return text
	}

	runes := []rune(text)
	if approxChars > len(runes) {
		return text
	}
	return string(runes[:approxChars]) + "\n... [truncated]"
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF) ||
		(r >= 0x3000 && r <= 0x303F) ||
		(r >= 0xFF00 && r <= 0xFFEF)
}
