ALTER TABLE annotations
    ADD CONSTRAINT annotations_evaluation_message_type_unique
    UNIQUE (evaluation_id, message_id, annotation_type);
