package jobs

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGenerateQuestionImageArgs_Kind(t *testing.T) {
	args := GenerateQuestionImageArgs{QuestionID: uuid.New()}
	require.Equal(t, "generate_question_image", args.Kind())
}

func TestGenerateQuestionImageInsertOpts(t *testing.T) {
	opts := GenerateQuestionImageInsertOpts()
	require.Equal(t, QueueAI, opts.Queue)
	require.Equal(t, 3, opts.MaxAttempts)
	require.True(t, opts.UniqueOpts.ByArgs)
}
