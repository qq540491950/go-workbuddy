// Guardrails: layered defense applied to user input and model output —
// sensitive-secret redaction and prompt-injection heuristics. System prompts
// alone are not guardrails; these checks run on both directions.
package agentkit

import (
	"regexp"
	"strings"
)

var secretPatterns = []*regexp.Regexp{
	// OpenAI / DeepSeek / generic "sk-" keys
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),
	// AWS access keys
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	// GitHub tokens
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
	// Slack tokens
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
	// Google API keys
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`),
	// Anthropic keys
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`),
	// Private key blocks
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	// Generic high-entropy hex secrets (32+ hex chars labelled as key/token/secret)
	regexp.MustCompile(`(?i)\b(?:key|token|secret|password)\s*[:=]\s*["']?[0-9a-f]{32,64}\b`),
}

const redactedPlaceholder = "[REDACTED]"

// SanitizeSecrets replaces secret-looking substrings with a placeholder.
func SanitizeSecrets(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, redactedPlaceholder)
	}
	return s
}

var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+(instructions|prompts?|rules)`),
	regexp.MustCompile(`(?i)reveal|print|repeat\s+(your|the)\s+(system\s+)?(prompt|instructions)`),
	regexp.MustCompile(`(?i)(system\s+prompt|开发提示词|系统提示词).{0,20}(是什么|泄露|输出|打印)`),
	regexp.MustCompile(`(?i)(泄露|输出|打印|导出).{0,10}(系统提示词|system prompt|开发提示词)`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+(a|an|no longer)`),
}

// InjectionRisk reports whether the text matches prompt-injection heuristics
// and returns the first matched pattern description.
func InjectionRisk(s string) (bool, string) {
	for _, re := range injectionPatterns {
		if loc := re.FindString(s); loc != "" {
			return true, strings.TrimSpace(loc)
		}
	}
	return false, ""
}

// GuardrailResult summarizes what the guardrail did to a piece of text.
type GuardrailResult struct {
	Text        string
	Redacted    bool
	InjectRisk  bool
	InjectMatch string
}

// ApplyInputGuardrail sanitizes user input and flags injection attempts.
func ApplyInputGuardrail(text string) GuardrailResult {
	sanitized := SanitizeSecrets(text)
	risk, match := InjectionRisk(sanitized)
	return GuardrailResult{
		Text:        sanitized,
		Redacted:    sanitized != text,
		InjectRisk:  risk,
		InjectMatch: match,
	}
}

// ApplyOutputGuardrail sanitizes model output before it reaches the UI.
func ApplyOutputGuardrail(text string) string {
	return SanitizeSecrets(text)
}
