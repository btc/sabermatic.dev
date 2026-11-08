package jobs

import (
	"errors"

	"github.com/riverqueue/river/rivertype"

	"github.com/btc/drill/internal/ai"
)

// terminalLLMError reports whether an LLM call error should end the job with
// no further retries.
//
//   - A refusal: the same request is not expected to succeed on retry.
//   - A max_tokens stop on the second or later attempt: each stop is billed
//     for the full max_tokens of output, and a retry sends the same prompt
//     with the same limit and effort, so one retry is allowed (thinking
//     length varies between runs) and a second stop ends the job.
//
// row may be nil (jobs built directly in tests), which counts as the first
// attempt.
func terminalLLMError(err error, row *rivertype.JobRow) bool {
	if ai.IsRefusal(err) {
		return true
	}
	return errors.Is(err, ai.ErrMaxTokens) && row != nil && row.Attempt >= 2
}
