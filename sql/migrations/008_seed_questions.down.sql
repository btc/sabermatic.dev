DELETE FROM questions
WHERE source = 'seed'
  AND NOT EXISTS (
    SELECT 1 FROM interview_sessions WHERE question_id = questions.id
  );
