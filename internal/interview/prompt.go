package interview

import "github.com/btc/drill/internal/interview/prompt"

// PromptBuilder is a type alias for prompt.Builder, preserving backward
// compatibility. The implementation was extracted to internal/interview/prompt
// to break the import cycle between backend and interview.
type PromptBuilder = prompt.Builder

// NewInterviewerPrompt delegates to the extracted prompt package.
func NewInterviewerPrompt() *PromptBuilder {
	return prompt.NewInterviewerPrompt()
}
